package health

import (
	"testing"
	"time"

	"xrayscope/internal/parser"
)

func TestInbounds(t *testing.T) {
	m := New()
	now := time.Now()
	add := func(tag, src, host, user string) {
		m.Observe(parser.Entry{Time: now, SrcIP: src, Host: host, InTag: tag, Email: user}, now)
	}
	add("in-443", "203.0.113.9", "example.com", "a")
	add("in-443", "203.0.113.9", "142.250.1.1", "a")
	add("in-ws", "104.21.3.9", "example.com", "b") // a Cloudflare edge as the client
	add("in-ws", "104.21.3.9", "example.com", "c")
	got := m.Inbounds()
	if len(got) != 2 {
		t.Fatalf("inbounds = %+v", got)
	}
	by := map[string]Inbound{}
	for _, x := range got {
		by[x.Tag] = x
	}
	if x := by["in-443"]; x.Requests != 2 || x.IPDest != 1 || x.CDNSrc != 0 || x.Users != 1 {
		t.Fatalf("in-443 = %+v", x)
	}
	if x := by["in-ws"]; x.CDNSrc != 2 || x.Users != 2 {
		t.Fatalf("in-ws = %+v", x)
	}
}

func TestClockFixer(t *testing.T) {
	saved := 0
	f := NewClockFixer(New(), 0, func(m int) { saved = m })
	now := time.Now()
	var last parser.Entry
	for i := 0; i < 600; i++ {
		at := now.Add(time.Duration(i) * 250 * time.Millisecond)
		last = parser.Entry{Time: at.Add(-210 * time.Minute), SrcIP: "203.0.113.1", Host: "a.com", InTag: "in", Email: "u"}
		f.Fix(&last, at)
	}
	if f.Shift() != -210 || saved != -210 {
		t.Fatalf("shift = %d saved = %d", f.Shift(), saved)
	}
	if d := now.Add(599 * 250 * time.Millisecond).Sub(last.Time); d > time.Second || d < -time.Second {
		t.Fatalf("corrected time is %v off", d)
	}
}

func TestClock(t *testing.T) {
	now := time.Now()
	feed := func(lag func(i int) time.Duration) ClockState {
		var c Clock
		for i := 0; i < 300; i++ {
			at := now.Add(time.Duration(i) * time.Second)
			c.observe(at.Add(lag(i)), at)
		}
		return c.state()
	}
	// a log written 3:30 behind (UTC read as Tehran time)
	if st := feed(func(int) time.Duration { return -3*time.Hour - 30*time.Minute + 2*time.Second }); !st.Sure || st.OffsetMin != -210 {
		t.Fatalf("shifted log: %+v", st)
	}
	// a correct log, lines read a second after being written
	if st := feed(func(int) time.Duration { return -time.Second }); !st.Sure || st.OffsetMin != 0 {
		t.Fatalf("correct log: %+v", st)
	}
	// catching up on a backlog: lags spread over hours, nothing is concluded
	if st := feed(func(i int) time.Duration { return -time.Duration(300-i) * time.Minute }); st.Sure {
		t.Fatalf("backlog must not be conclusive: %+v", st)
	}
	// a burst of two-hour-old lines read in a few seconds after a restart
	var c Clock
	for i := 0; i < 300; i++ {
		at := now.Add(time.Duration(i) * 10 * time.Millisecond)
		c.observe(at.Add(-2*time.Hour), at)
	}
	if st := c.state(); st.Sure {
		t.Fatalf("a quick backlog must not be taken for a time zone: %+v", st)
	}
}
