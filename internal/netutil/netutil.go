// Package netutil builds HTTP clients that can reach the outside world
// through an HTTP(S) or SOCKS5 proxy.
package netutil

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

// Options configures a client.
type Options struct {
	Proxy       string        // http://, https://, socks5:// or socks5h:// URL; empty = direct
	Timeout     time.Duration // whole-request timeout
	InsecureTLS bool          // skip certificate verification
	ServerName  string        // override the TLS server name (SNI)
}

// NewClient returns an HTTP client for the options.
func NewClient(o Options) (*http.Client, error) {
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	tr := &http.Transport{
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: o.Timeout,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConnsPerHost:   2,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: o.InsecureTLS, ServerName: o.ServerName, MinVersion: tls.VersionTLS12},
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	tr.DialContext = dialer.DialContext
	if o.Proxy != "" {
		u, err := url.Parse(o.Proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy: %w", err)
		}
		switch u.Scheme {
		case "http", "https":
			tr.Proxy = http.ProxyURL(u)
		case "socks5", "socks5h":
			var auth *proxy.Auth
			if u.User != nil {
				pw, _ := u.User.Password()
				auth = &proxy.Auth{User: u.User.Username(), Password: pw}
			}
			d, err := proxy.SOCKS5("tcp", u.Host, auth, dialer)
			if err != nil {
				return nil, err
			}
			cd, ok := d.(proxy.ContextDialer)
			if !ok {
				return nil, fmt.Errorf("socks5 dialer does not support contexts")
			}
			tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return cd.DialContext(ctx, network, addr)
			}
		default:
			return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
		}
	}
	return &http.Client{Transport: tr, Timeout: o.Timeout}, nil
}
