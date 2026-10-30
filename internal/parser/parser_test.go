package parser

import (
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		name string
		line string
		ok   bool
		want Entry
	}{
		{
			name: "standard",
			line: "2024/05/01 10:20:30.123456 from 1.2.3.4:5555 accepted tcp:example.com:443 [inbound-443 >> direct] email: user1",
			ok:   true,
			want: Entry{SrcIP: "1.2.3.4", Network: "tcp", Host: "example.com", Port: 443, InTag: "inbound-443", OutTag: "direct", Email: "user1"},
		},
		{
			name: "ipv6 and arrow tags",
			line: "2024/05/01 10:20:30 from tcp:[2001:db8::1]:5555 accepted udp:8.8.8.8:53 [in -> out] email: user2@x",
			ok:   true,
			want: Entry{SrcIP: "2001:db8::1", Network: "udp", Host: "8.8.8.8", Port: 53, InTag: "in", OutTag: "out", Email: "user2@x"},
		},
		{
			name: "uppercase host",
			line: "2024/05/01 10:20:30.5 from 9.9.9.9:1 accepted tcp:WWW.Example.COM:80 [a >> b] email: u",
			ok:   true,
			want: Entry{SrcIP: "9.9.9.9", Network: "tcp", Host: "www.example.com", Port: 80, InTag: "a", OutTag: "b", Email: "u"},
		},
		{name: "no email", line: "2024/05/01 10:20:30 from 1.2.3.4:5 accepted tcp:a.com:443 [a >> b]", ok: false},
		{name: "api traffic", line: "2024/05/01 10:20:30 from 127.0.0.1:5 accepted tcp:127.0.0.1:62789 [api -> api] email: x", ok: false},
		{name: "rejected", line: "2024/05/01 10:20:30 from 1.2.3.4:5 rejected  proxy/vless/encoding: invalid user", ok: false},
		{name: "source is not an address", line: "2024/05/01 10:20:30 from d tcp:127.0.0.1:62789 accepted tcp:a.com:443 [a >> b] email: u", ok: false},
		{name: "garbage", line: "hello world this is not a log line", ok: false},
		{name: "short", line: "x", ok: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseLine(c.line, loc)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !ok {
				return
			}
			got.Time = time.Time{}
			if got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseTime(t *testing.T) {
	e, ok := ParseLine("2024/05/01 10:20:30.250000 from 1.2.3.4:5 accepted tcp:a.com:443 [a >> b] email: u", time.UTC)
	if !ok {
		t.Fatal("not parsed")
	}
	want := time.Date(2024, 5, 1, 10, 20, 30, 250000000, time.UTC)
	if !e.Time.Equal(want) {
		t.Fatalf("time = %v, want %v", e.Time, want)
	}
}

func BenchmarkParseLine(b *testing.B) {
	line := "2024/05/01 10:20:30.123456 from 1.2.3.4:5555 accepted tcp:example.com:443 [inbound-443 >> direct] email: user1"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ParseLine(line, time.UTC)
	}
}
