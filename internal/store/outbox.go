package store

import (
	"context"
	"database/sql"
	"errors"
)

// The outbox holds the batches a pull node produces until the main server
// has collected them. It only runs once the main server asked for it, so a
// node nobody pulls from does not store anything extra.
const (
	outboxKey     = "outbox:on"
	outboxMaxRows = 100000
)

// OutboxOn reports whether batches are being queued for a main server.
func (s *Store) OutboxOn() bool {
	if s.outboxOn.Load() {
		return true
	}
	v, ok, _ := s.KV(outboxKey)
	if ok && v == "1" {
		s.outboxOn.Store(true)
		return true
	}
	return false
}

// EnableOutbox starts queueing batches.
func (s *Store) EnableOutbox() error {
	if s.OutboxOn() {
		return nil
	}
	if err := s.SetKV(outboxKey, "1"); err != nil {
		return err
	}
	s.outboxOn.Store(true)
	return nil
}

// OutboxItem is one queued batch.
type OutboxItem struct {
	Seq  int64
	Body []byte
}

// PushOutbox queues one serialised batch.
func (s *Store) PushOutbox(body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Exec(`INSERT INTO outbox(body) VALUES(?)`, body); err != nil {
		return err
	}
	if s.outboxPushes.Add(1)%500 == 0 {
		_, err := s.w.Exec(`DELETE FROM outbox WHERE seq <= (SELECT MAX(seq) FROM outbox) - ?`, outboxMaxRows)
		return err
	}
	return nil
}

// PullOutbox acknowledges everything up to and including after, then
// returns the next queued batches (at most limit, at most maxBytes of
// payload but always at least one). last is the highest sequence number ever
// assigned, which tells the puller if the node's database was replaced.
func (s *Store) PullOutbox(ctx context.Context, after int64, limit, maxBytes int) (items []OutboxItem, last int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.ExecContext(ctx, `DELETE FROM outbox WHERE seq<=?`, after); err != nil {
		return nil, 0, err
	}
	var seq sql.NullInt64
	err = s.w.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name='outbox'`).Scan(&seq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, err
	}
	last = seq.Int64
	rows, err := s.w.QueryContext(ctx, `SELECT seq, body FROM outbox WHERE seq>? ORDER BY seq LIMIT ?`, after, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var it OutboxItem
		if err := rows.Scan(&it.Seq, &it.Body); err != nil {
			return nil, 0, err
		}
		if len(items) > 0 && total+len(it.Body) > maxBytes {
			break
		}
		total += len(it.Body)
		items = append(items, it)
	}
	return items, last, rows.Err()
}
