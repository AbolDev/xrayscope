package collect

import (
	"context"
	"time"

	"xrayscope/internal/model"
	"xrayscope/internal/panel"
)

// TrafficPoller turns the panel's cumulative per-user counters into deltas.
type TrafficPoller struct {
	Panel    panel.Panel
	Interval time.Duration
	// Last holds the previously seen cumulative counters per email.
	Last map[string][2]int64
	// Deliver receives deltas, the new counters and the account list.
	Deliver func(deltas []model.TrafficRec, totals map[string][2]int64, users []model.PanelUser) error
	Warn    func(err error)
}

// Run polls until ctx ends.
func (p *TrafficPoller) Run(ctx context.Context) {
	if p.Interval <= 0 {
		p.Interval = 30 * time.Second
	}
	if p.Last == nil {
		p.Last = map[string][2]int64{}
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := p.poll(ctx); err != nil && p.Warn != nil && ctx.Err() == nil {
			p.Warn(err)
		}
		t.Reset(p.Interval)
	}
}

func (p *TrafficPoller) poll(ctx context.Context) error {
	users, err := p.Panel.Users(ctx)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	deltas, totals := Deltas(p.Last, users, now)
	if err := p.Deliver(deltas, totals, users); err != nil {
		return err
	}
	for e, t := range totals {
		p.Last[e] = t
	}
	return nil
}

// Deltas compares cumulative counters with the previous ones. Accounts seen
// for the first time only establish a baseline. A counter that went down was
// reset by the panel, so its current value is the delta. totals contains
// only the accounts whose counters changed.
func Deltas(last map[string][2]int64, users []model.PanelUser, now int64) (deltas []model.TrafficRec, totals map[string][2]int64) {
	totals = map[string][2]int64{}
	for _, u := range users {
		cur := [2]int64{u.Up, u.Down}
		prev, seen := last[u.Email]
		if seen && prev == cur {
			continue
		}
		totals[u.Email] = cur
		if !seen {
			continue
		}
		up, down := cur[0]-prev[0], cur[1]-prev[1]
		if up < 0 {
			up = cur[0]
		}
		if down < 0 {
			down = cur[1]
		}
		if up > 0 || down > 0 {
			deltas = append(deltas, model.TrafficRec{T: now, U: u.Email, Up: up, Down: down})
		}
	}
	return deltas, totals
}
