package live

import (
	"testing"
	"time"

	"xrayscope/internal/store"
)

func TestOnlineWindowAndPrune(t *testing.T) {
	tr := New()
	now := time.Now().Unix() / 60
	tr.Observe([]store.Obs{
		{UserID: 1, Email: "a", IP: "1.1.1.1", Minute: now},
		{UserID: 1, Email: "a", IP: "2.2.2.2", Minute: now - 1},
		{UserID: 1, Email: "a", IP: "3.3.3.3", Minute: now - 30},
		{UserID: 2, Email: "b", IP: "4.4.4.4", Minute: now - 10},
	})
	on := tr.Online(3 * time.Minute)
	if len(on) != 1 || on[0].Email != "a" || len(on[0].Conns) != 2 {
		t.Fatalf("online = %+v", on)
	}
	if on[0].Conns[0].IP != "1.1.1.1" {
		t.Fatalf("newest connection must come first: %+v", on[0].Conns)
	}
	if _, ok := tr.OnlineUser(2, 3*time.Minute); ok {
		t.Fatal("user b is not online")
	}
	if got := tr.Online(15 * time.Minute); len(got) != 2 {
		t.Fatalf("wider window: %+v", got)
	}
	tr.Prune(5 * time.Minute)
	if got := tr.Online(time.Hour); len(got) != 1 || len(got[0].Conns) != 2 {
		t.Fatalf("after prune: %+v", got)
	}
}

func TestAddressChangeIsOneDevice(t *testing.T) {
	tr := New()
	tr.SameNet = func(a, b string) bool { return a[:3] == b[:3] }
	now := time.Now().Unix() / 60
	tr.Observe([]store.Obs{
		// the phone used 5.1.x until a minute ago, then got 5.1.y
		{UserID: 1, Email: "a", IP: "5.1.0.1", Minute: now - 4},
		{UserID: 1, Email: "a", IP: "5.1.0.1", Minute: now - 1},
		{UserID: 1, Email: "a", IP: "5.1.9.9", Minute: now - 1},
		{UserID: 1, Email: "a", IP: "5.1.9.9", Minute: now},
		// a laptop on another network, active the whole time
		{UserID: 1, Email: "a", IP: "9.9.9.9", Minute: now - 4},
		{UserID: 1, Email: "a", IP: "9.9.9.9", Minute: now},
	})
	on := tr.Online(5 * time.Minute)
	if len(on) != 1 || len(on[0].Conns) != 3 || on[0].Devices != 2 {
		t.Fatalf("online = %+v", on)
	}
	for _, c := range on[0].Conns {
		if c.Replaced != (c.IP == "5.1.0.1") {
			t.Fatalf("replaced flags wrong: %+v", on[0].Conns)
		}
	}
}

func TestObserveKeepsNewest(t *testing.T) {
	tr := New()
	now := time.Now().Unix() / 60
	tr.Observe([]store.Obs{{UserID: 1, Email: "a", IP: "1.1.1.1", Minute: now}})
	tr.Observe([]store.Obs{{UserID: 1, Email: "a", IP: "1.1.1.1", Minute: now - 5}})
	on := tr.Online(time.Minute)
	if len(on) != 1 || on[0].LastSeen != now*60 {
		t.Fatalf("older observation overwrote a newer one: %+v", on)
	}
}
