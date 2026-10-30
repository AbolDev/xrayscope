package collect

import (
	"testing"
	"time"
	"unsafe"

	"xrayscope/internal/parser"
)

// The tailer hands the aggregator strings that point into a reused buffer.
// Repeated keys must not make the stored keys follow that buffer.
func TestAggregatorSurvivesBufferReuse(t *testing.T) {
	line := []byte("2024/05/01 10:20:30.123456 from 1.2.3.4:5555 accepted tcp:example.com:443 [in >> out] email: alice\n")
	buf := make([]byte, len(line))
	a := NewAggregator()
	for i := 0; i < 5; i++ {
		copy(buf, line)
		s := unsafe.String(&buf[0], len(buf)-1)
		e, ok := parser.ParseLine(s, time.UTC)
		if !ok {
			t.Fatal("not parsed")
		}
		a.Add(e)
		for j := range buf { // the next read overwrites the buffer
			buf[j] = 'X'
		}
	}
	recs := a.Drain()
	if len(recs) != 1 || recs[0].U != "alice" || recs[0].IP != "1.2.3.4" || recs[0].D != "example.com" || recs[0].N != 5 {
		t.Fatalf("recs = %+v", recs)
	}
	if a.Len() != 0 {
		t.Fatal("drain must reset the aggregator")
	}
}

func mustEntry() parser.Entry {
	e, ok := parser.ParseLine("2024/05/01 10:20:30.123456 from 1.2.3.4:5555 accepted tcp:example.com:443 [in >> out] email: user1", time.UTC)
	if !ok {
		panic("bad fixture")
	}
	return e
}
