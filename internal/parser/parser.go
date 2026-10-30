// Package parser turns Xray access.log lines into structured entries.
//
// Typical lines:
//
//	2024/05/01 10:20:30.123456 from 1.2.3.4:5555 accepted tcp:example.com:443 [inbound-443 >> direct] email: user1
//	2024/05/01 10:20:30 from tcp:[2001:db8::1]:5555 accepted udp:8.8.8.8:53 [in -> out] email: user2
package parser

import (
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Entry is one accepted connection from the access log.
type Entry struct {
	Time    time.Time
	SrcIP   string
	Network string // tcp or udp
	Host    string // destination host name or IP, lower-case
	Port    int
	InTag   string
	OutTag  string
	Email   string
}

// ParseLine parses a single access.log line. ok is false for lines that are
// not accepted-connection records (rejections, warnings, malformed lines) and
// for internal API traffic.
func ParseLine(line string, loc *time.Location) (e Entry, ok bool) {
	if len(line) < 20 {
		return e, false
	}
	t, rest, ok := parseTime(line, loc)
	if !ok {
		return e, false
	}
	e.Time = t

	// " from <src> accepted <network>:<host>:<port> [..] email: <name>"
	rest = strings.TrimLeft(rest, " ")
	if !strings.HasPrefix(rest, "from ") {
		return e, false
	}
	rest = rest[5:]
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return e, false
	}
	src := rest[:sp]
	rest = strings.TrimLeft(rest[sp:], " ")
	if !strings.HasPrefix(rest, "accepted ") {
		return e, false
	}
	rest = rest[9:]

	_, src = splitNetwork(src)
	srcIP, _ := splitHostPort(src)
	if _, err := netip.ParseAddr(srcIP); err != nil {
		return e, false // not a client address: a different kind of log line
	}
	e.SrcIP = srcIP

	sp = strings.IndexByte(rest, ' ')
	dst := rest
	if sp >= 0 {
		dst = rest[:sp]
		rest = rest[sp+1:]
	} else {
		rest = ""
	}
	e.Network, dst = splitNetwork(dst)
	if e.Network == "" {
		e.Network = "tcp"
	}
	host, port := splitHostPort(dst)
	if host == "" {
		return e, false
	}
	e.Host = strings.ToLower(host)
	e.Port = port

	if i := strings.IndexByte(rest, '['); i >= 0 {
		if j := strings.IndexByte(rest[i:], ']'); j > 0 {
			e.InTag, e.OutTag = splitTags(rest[i+1 : i+j])
		}
	}
	if i := strings.Index(rest, "email:"); i >= 0 {
		e.Email = strings.TrimSpace(rest[i+6:])
	}
	if e.Email == "" || e.InTag == "api" {
		return e, false
	}
	return e, true
}

func splitTags(s string) (in, out string) {
	for _, sep := range []string{" >> ", " -> "} {
		if i := strings.Index(s, sep); i >= 0 {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+len(sep):])
		}
	}
	return strings.TrimSpace(s), ""
}

func splitNetwork(s string) (network, rest string) {
	if strings.HasPrefix(s, "tcp:") {
		return "tcp", s[4:]
	}
	if strings.HasPrefix(s, "udp:") {
		return "udp", s[4:]
	}
	return "", s
}

// splitHostPort splits "host:port", "[v6]:port" or a bare host.
func splitHostPort(s string) (host string, port int) {
	if s == "" {
		return "", 0
	}
	if s[0] == '[' {
		j := strings.IndexByte(s, ']')
		if j < 0 {
			return "", 0
		}
		host = s[1:j]
		if j+2 < len(s) && s[j+1] == ':' {
			port, _ = strconv.Atoi(s[j+2:])
		}
		return host, port
	}
	if strings.Count(s, ":") > 1 { // bare IPv6 without port
		return s, 0
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		port, _ = strconv.Atoi(s[i+1:])
		return s[:i], port
	}
	return s, 0
}

// parseTime reads "YYYY/MM/DD HH:MM:SS[.ffffff]" without allocating.
func parseTime(s string, loc *time.Location) (t time.Time, rest string, ok bool) {
	if len(s) < 19 || s[4] != '/' || s[7] != '/' || s[10] != ' ' || s[13] != ':' || s[16] != ':' {
		return t, "", false
	}
	y, ok1 := atoi(s[0:4])
	mo, ok2 := atoi(s[5:7])
	d, ok3 := atoi(s[8:10])
	h, ok4 := atoi(s[11:13])
	mi, ok5 := atoi(s[14:16])
	se, ok6 := atoi(s[17:19])
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) {
		return t, "", false
	}
	end := 19
	nsec := 0
	if end < len(s) && s[end] == '.' {
		end++
		digits := 0
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			if digits < 9 {
				nsec = nsec*10 + int(s[end]-'0')
				digits++
			}
			end++
		}
		for ; digits < 9; digits++ {
			nsec *= 10
		}
	}
	if loc == nil {
		loc = time.Local
	}
	return time.Date(y, time.Month(mo), d, h, mi, se, nsec, loc), s[end:], true
}

func atoi(s string) (int, bool) {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}
