// Package xraystats reads live per-user counters from Xray's StatsService
// (gRPC over plain HTTP/2) and turns them into transfer speeds.
//
// The gRPC wire format is small enough to speak directly: one unary call is
// an HTTP/2 POST whose body is a 5-byte frame header followed by a protobuf
// message, and whose status arrives in the trailers.
package xraystats

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

// Querier returns the cumulative upload and download bytes per user e-mail.
type Querier interface {
	Query(ctx context.Context) (map[string][2]int64, error)
}

// Client talks to Xray's API inbound.
type Client struct {
	Addr string // host:port, usually 127.0.0.1:<api port>
	http *http.Client
}

// NewClient creates a client for an API address.
func NewClient(addr string) *Client {
	tr := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	return &Client{Addr: addr, http: &http.Client{Transport: tr, Timeout: 5 * time.Second}}
}

const method = "/xray.app.stats.command.StatsService/QueryStats"

// Query fetches every user counter ("user>>>EMAIL>>>traffic>>>uplink").
func (c *Client) Query(ctx context.Context) (map[string][2]int64, error) {
	body := frame(encodeRequest("user>>>"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.Addr+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Te", "trailers")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if st := resp.Trailer.Get("Grpc-Status"); st != "" && st != "0" {
		return nil, fmt.Errorf("xray api: %s (%s)", resp.Trailer.Get("Grpc-Message"), st)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xray api: HTTP %d", resp.StatusCode)
	}
	if st := resp.Header.Get("Grpc-Status"); st != "" && st != "0" {
		return nil, fmt.Errorf("xray api: %s (%s)", resp.Header.Get("Grpc-Message"), st)
	}
	msg, err := unframe(data)
	if err != nil {
		return nil, err
	}
	return decodeResponse(msg)
}

// ---------------------------------------------------------------- wire format

func frame(msg []byte) []byte {
	out := make([]byte, 5+len(msg))
	binary.BigEndian.PutUint32(out[1:5], uint32(len(msg)))
	copy(out[5:], msg)
	return out
}

func unframe(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil // an empty reply means no counters yet
	}
	if len(b) < 5 {
		return nil, errors.New("xray api: short response")
	}
	if b[0] != 0 {
		return nil, errors.New("xray api: compressed responses are not supported")
	}
	n := int(binary.BigEndian.Uint32(b[1:5]))
	if len(b)-5 < n {
		return nil, errors.New("xray api: truncated response")
	}
	return b[5 : 5+n], nil
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func readVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

// encodeRequest builds QueryStatsRequest{pattern: p}.
func encodeRequest(pattern string) []byte {
	b := []byte{0x0a}
	b = appendVarint(b, uint64(len(pattern)))
	return append(b, pattern...)
}

// decodeResponse parses QueryStatsResponse{stat: [Stat{name, value}]} into
// per-user [up, down] pairs.
func decodeResponse(msg []byte) (map[string][2]int64, error) {
	out := map[string][2]int64{}
	for len(msg) > 0 {
		tag, n := readVarint(msg)
		if n == 0 {
			return nil, errors.New("xray api: malformed response")
		}
		msg = msg[n:]
		wire := tag & 7
		switch wire {
		case 2: // length-delimited
			l, n := readVarint(msg)
			if n == 0 || int(l) > len(msg)-n {
				return nil, errors.New("xray api: malformed response")
			}
			field := msg[n : n+int(l)]
			msg = msg[n+int(l):]
			if tag>>3 == 1 {
				name, value, err := decodeStat(field)
				if err != nil {
					return nil, err
				}
				email, dir, ok := parseName(name)
				if !ok {
					continue
				}
				c := out[email]
				if dir == "uplink" {
					c[0] = value
				} else {
					c[1] = value
				}
				out[email] = c
			}
		case 0:
			_, n := readVarint(msg)
			msg = msg[n:]
		case 1:
			if len(msg) < 8 {
				return nil, errors.New("xray api: malformed response")
			}
			msg = msg[8:]
		case 5:
			if len(msg) < 4 {
				return nil, errors.New("xray api: malformed response")
			}
			msg = msg[4:]
		default:
			return nil, errors.New("xray api: unsupported field")
		}
	}
	return out, nil
}

func decodeStat(b []byte) (name string, value int64, err error) {
	for len(b) > 0 {
		tag, n := readVarint(b)
		if n == 0 {
			return "", 0, errors.New("xray api: malformed stat")
		}
		b = b[n:]
		switch tag & 7 {
		case 2:
			l, n := readVarint(b)
			if n == 0 || int(l) > len(b)-n {
				return "", 0, errors.New("xray api: malformed stat")
			}
			if tag>>3 == 1 {
				name = string(b[n : n+int(l)])
			}
			b = b[n+int(l):]
		case 0:
			v, n := readVarint(b)
			if n == 0 {
				return "", 0, errors.New("xray api: malformed stat")
			}
			if tag>>3 == 2 {
				value = int64(v)
			}
			b = b[n:]
		default:
			return "", 0, errors.New("xray api: unsupported stat field")
		}
	}
	return name, value, nil
}

// parseName splits "user>>>alice>>>traffic>>>uplink".
func parseName(name string) (email, dir string, ok bool) {
	parts := strings.Split(name, ">>>")
	if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
		return "", "", false
	}
	if parts[3] != "uplink" && parts[3] != "downlink" {
		return "", "", false
	}
	return parts[1], parts[3], true
}

// ---------------------------------------------------------------- discovery

// FindAPI reads the generated Xray configuration and returns the address of
// its API inbound (the one tagged "api").
func FindAPI(paths ...string) (string, bool) {
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cfg struct {
			Inbounds []struct {
				Tag    string          `json:"tag"`
				Listen string          `json:"listen"`
				Port   json.RawMessage `json:"port"`
			} `json:"inbounds"`
		}
		if json.Unmarshal(b, &cfg) != nil {
			continue
		}
		for _, in := range cfg.Inbounds {
			if in.Tag != "api" {
				continue
			}
			port, err := strconv.Atoi(strings.Trim(string(in.Port), `"`))
			if err != nil || port <= 0 {
				continue
			}
			host := in.Listen
			if host == "" || host == "0.0.0.0" {
				host = "127.0.0.1"
			}
			return net.JoinHostPort(host, strconv.Itoa(port)), true
		}
	}
	return "", false
}
