package store

import (
	"context"

	"xrayscope/internal/model"
)

// ApplyActive stores the minutes in which users moved data.
func (s *Store) ApplyActive(nodeID int64, recs []model.ActiveRec) error {
	if len(recs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
			s.resetCaches()
		}
	}()
	st, err := tx.Prepare(`INSERT INTO tact(hour,user_id,node_id,mask,hmask) VALUES(?,?,?,?,?)
		ON CONFLICT(hour,user_id,node_id) DO UPDATE SET mask=mask|excluded.mask, hmask=hmask|excluded.hmask`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, r := range recs {
		if r.U == "" || r.Mask == 0 || r.Hour <= 0 {
			continue
		}
		uid, err := s.userID(tx, r.U)
		if err != nil {
			return err
		}
		if _, err := st.Exec(r.Hour, uid, nodeID, r.Mask, r.Heavy&r.Mask); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// TAct returns, per user and hour, the minutes with real traffic (all nodes
// combined). userID 0 means every user.
func (s *Store) TAct(ctx context.Context, userID, fromHour, toHour int64) (map[int64]map[int64]int64, error) {
	return s.tact(ctx, "mask", userID, fromHour, toHour)
}

// THeavy is like TAct for the minutes of sustained transfer.
func (s *Store) THeavy(ctx context.Context, userID, fromHour, toHour int64) (map[int64]map[int64]int64, error) {
	return s.tact(ctx, "hmask", userID, fromHour, toHour)
}

func (s *Store) tact(ctx context.Context, col string, userID, fromHour, toHour int64) (map[int64]map[int64]int64, error) {
	q := `SELECT user_id, hour, ` + col + ` FROM tact WHERE hour>=? AND hour<? AND ` + col + `<>0`
	args := []any{fromHour, toHour}
	if userID > 0 {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[int64]int64{}
	for rows.Next() {
		var u, h, m int64
		if err := rows.Scan(&u, &h, &m); err != nil {
			return nil, err
		}
		if out[u] == nil {
			out[u] = map[int64]int64{}
		}
		out[u][h] |= m
	}
	return out, rows.Err()
}
