package server

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// liveEvery is how often a snapshot is pushed to every open panel.
const liveEvery = 2 * time.Second

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// liveSnapshot is what every connected panel receives.
type liveSnapshot struct {
	TS     int64  `json:"ts"`
	Speed  any    `json:"speed"`
	Online any    `json:"online"`
	Sig    string `json:"sig"` // changes whenever who is online (or from how many IPs) changes
}

func (s *Server) liveJSON(r *http.Request) ([]byte, error) {
	return s.cached("live", liveEvery-300*time.Millisecond, func() (any, error) {
		snap := liveSnapshot{TS: time.Now().Unix(), Speed: s.app.Speeds(r.Context(), 200)}
		online := s.app.Live.Online(s.app.Env().OnlineWin)
		ips := 0
		parts := make([]string, 0, len(online))
		for _, o := range online {
			ips += len(o.Conns)
			parts = append(parts, fmt.Sprintf("%d:%d", o.UserID, len(o.Conns)))
		}
		sort.Strings(parts)
		snap.Online = map[string]int{"users": len(online), "ips": ips}
		snap.Sig = strings.Join(parts, ",")
		return snap, nil
	})
}

// handleLive upgrades to a WebSocket and pushes a snapshot every few seconds.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		fail(w, http.StatusUpgradeRequired, "WebSocket required")
		return
	}
	// cookies are sent on cross-site WebSocket requests, so the origin must match
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			fail(w, http.StatusForbidden, "origin not allowed")
			return
		}
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	hj, ok := w.(http.Hijacker)
	if key == "" || !ok {
		fail(w, http.StatusBadRequest, "cannot upgrade")
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	sum := sha1.Sum([]byte(key + wsGUID))
	_, _ = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
		base64.StdEncoding.EncodeToString(sum[:]))
	if rw.Flush() != nil {
		return
	}

	closed := make(chan struct{})
	pong := make(chan []byte, 1)
	go func() {
		defer close(closed)
		wsDrain(rw.Reader, conn, pong)
	}()

	send := func() bool {
		b, err := s.liveJSON(r)
		if err != nil {
			return true
		}
		return wsWrite(conn, 0x1, b) == nil
	}
	if !send() {
		return
	}
	tick := time.NewTicker(liveEvery)
	defer tick.Stop()
	for {
		select {
		case <-closed:
			return
		case <-r.Context().Done():
			return
		case p := <-pong:
			if wsWrite(conn, 0xA, p) != nil {
				return
			}
		case <-tick.C:
			// the session can end while the socket stays open
			if c, err := r.Cookie(cookieName); err != nil {
				return
			} else if _, ok := s.app.Auth.Verify(c.Value); !ok {
				return
			}
			if !send() {
				return
			}
		}
	}
}

func wsWrite(conn net.Conn, opcode byte, payload []byte) error {
	hdr := []byte{0x80 | opcode}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 1<<16:
		hdr = append(hdr, 126, 0, 0)
		binary.BigEndian.PutUint16(hdr[2:], uint16(n))
	default:
		hdr = append(hdr, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
	}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(hdr); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

// wsDrain reads client frames until the socket closes. It answers pings and
// ignores everything else; a client has nothing to say to this endpoint.
func wsDrain(br *bufio.Reader, conn net.Conn, pong chan<- []byte) {
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
		var h [2]byte
		if _, err := io.ReadFull(br, h[:]); err != nil {
			return
		}
		opcode := h[0] & 0x0f
		masked := h[1]&0x80 != 0
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(br, b[:]); err != nil {
				return
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(br, b[:]); err != nil {
				return
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if !masked || n > 1<<20 {
			return // clients must mask, and nothing legitimate is large
		}
		var mask [4]byte
		if _, err := io.ReadFull(br, mask[:]); err != nil {
			return
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(br, payload); err != nil {
			return
		}
		switch opcode {
		case 0x8:
			return
		case 0x9:
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
			if len(payload) > 125 {
				payload = payload[:125]
			}
			select {
			case pong <- payload:
			default:
			}
		}
	}
}
