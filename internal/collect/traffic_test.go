package collect

import (
	"testing"

	"xrayscope/internal/model"
)

func TestDeltas(t *testing.T) {
	last := map[string][2]int64{"a": {100, 1000}, "b": {50, 50}, "c": {10, 10}}
	users := []model.PanelUser{
		{Email: "a", Up: 150, Down: 1500}, // normal growth
		{Email: "b", Up: 5, Down: 60},     // upload counter was reset
		{Email: "c", Up: 10, Down: 10},    // unchanged
		{Email: "d", Up: 999, Down: 999},  // new account: baseline only
	}
	deltas, totals := Deltas(last, users, 1000)
	got := map[string]model.TrafficRec{}
	for _, d := range deltas {
		got[d.U] = d
	}
	if len(got) != 2 {
		t.Fatalf("deltas = %+v", deltas)
	}
	if a := got["a"]; a.Up != 50 || a.Down != 500 {
		t.Fatalf("a = %+v", a)
	}
	if b := got["b"]; b.Up != 5 || b.Down != 10 {
		t.Fatalf("b = %+v", b)
	}
	if _, ok := totals["c"]; ok {
		t.Fatal("unchanged account must not be rewritten")
	}
	if totals["d"] != [2]int64{999, 999} {
		t.Fatalf("baseline missing: %+v", totals)
	}
}
