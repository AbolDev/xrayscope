package netutil

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPProxyIsUsed(t *testing.T) {
	var seen string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.String()
		io.WriteString(w, "via proxy")
	}))
	defer proxy.Close()
	c, err := NewClient(Options{Proxy: proxy.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get("http://example.invalid/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "via proxy" || seen != "http://example.invalid/hello" {
		t.Fatalf("body=%q seen=%q", b, seen)
	}
}

// A tiny SOCKS5 server that accepts one connection and records the target.
func TestSOCKS5ProxyIsUsed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	target := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 262)
		conn.Read(buf[:2]) // version, nmethods
		conn.Read(buf[:int(buf[1])])
		conn.Write([]byte{5, 0})
		conn.Read(buf[:4]) // ver cmd rsv atyp
		var host string
		if buf[3] == 3 {
			conn.Read(buf[:1])
			n := int(buf[0])
			conn.Read(buf[:n])
			host = string(buf[:n])
		}
		conn.Read(buf[:2]) // port
		target <- host
		conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0}) // connection refused, stop here
	}()
	c, err := NewClient(Options{Proxy: "socks5://" + ln.Addr().String(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get("http://remote.example.invalid/"); err == nil {
		t.Fatal("expected the refused proxy connection to fail the request")
	}
	select {
	case h := <-target:
		if h != "remote.example.invalid" {
			t.Fatalf("socks target = %q", h)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the request did not go through the SOCKS5 proxy")
	}
}

func TestBadProxyRejected(t *testing.T) {
	for _, p := range []string{"ftp://x", "://bad"} {
		if _, err := NewClient(Options{Proxy: p}); err == nil {
			t.Errorf("proxy %q accepted", p)
		}
	}
}
