package report

import (
	"xrayscope/internal/analytics"
	"xrayscope/internal/store"
)

// refineAct returns a copy of presence rows whose interactive minutes are the
// minutes of real use, built the same way as the time per site: automatic
// polling removed and short or busy pauses bridged. Presence gains the
// bridged minutes too, so "online" never falls below "active".
//
// The copy is for totals, charts and sleep; anything that compares
// addresses (concurrency, IP sessions) keeps using the raw rows, because the
// refined minutes are put on one row per user and hour.
func (e *Env) refineAct(ctx ctxT, userID int64, rg Range, rows []store.ActRow) ([]store.ActRow, error) {
	svc, err := e.Store.Svc(ctx, userID, rg.contextFromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	services, err := e.Store.Services(ctx)
	if err != nil {
		return nil, err
	}
	tact, err := e.Store.TAct(ctx, userID, rg.contextFromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	use := analytics.UseMinutes(svc, services, tact, rg.window())

	type uh struct{ user, hour int64 }
	out := make([]store.ActRow, len(rows))
	copy(out, rows)
	done := map[uh]bool{}
	for i := range out {
		r := &out[i]
		k := uh{r.UserID, r.Hour}
		if done[k] {
			r.IMask = 0
			continue
		}
		done[k] = true
		m := use[r.UserID][r.Hour]
		r.IMask = m
		r.Mask |= m
	}
	return out, nil
}
