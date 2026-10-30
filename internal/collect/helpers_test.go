package collect

import (
	"time"

	"xrayscope/internal/parser"
)

func mustEntry() parser.Entry {
	e, ok := parser.ParseLine("2024/05/01 10:20:30.123456 from 1.2.3.4:5555 accepted tcp:example.com:443 [in >> out] email: user1", time.UTC)
	if !ok {
		panic("bad fixture")
	}
	return e
}
