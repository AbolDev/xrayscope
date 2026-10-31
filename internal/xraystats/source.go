package xraystats

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNoAPI is returned while Xray's statistics API cannot be found.
var ErrNoAPI = errors.New("Xray statistics API not found")

// Source finds Xray's statistics API and keeps it queried. The address is
// looked up again whenever a query fails, because the panel may restart Xray
// with a different configuration.
type Source struct {
	Override string                // address from the configuration, if any
	Find     func() (string, bool) // asks the panel where the API listens

	mu      sync.Mutex
	client  *Client
	lastTry time.Time
}

// Query implements Querier.
func (s *Source) Query(ctx context.Context) (map[string][2]int64, error) {
	s.mu.Lock()
	cl := s.client
	if cl == nil {
		if time.Since(s.lastTry) < 10*time.Second {
			s.mu.Unlock()
			return nil, ErrNoAPI
		}
		s.lastTry = time.Now()
		addr := s.Override
		if addr == "" && s.Find != nil {
			addr, _ = s.Find()
		}
		if addr == "" {
			s.mu.Unlock()
			return nil, ErrNoAPI
		}
		s.client = NewClient(addr)
		cl = s.client
	}
	s.mu.Unlock()
	res, err := cl.Query(ctx)
	if err != nil {
		s.mu.Lock()
		s.client = nil // look the address up again next time
		s.mu.Unlock()
	}
	return res, err
}
