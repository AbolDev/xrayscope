package update

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.1.0-beta.10", "v0.1.0-beta.9", true},
		{"v0.1.0-beta.9", "v0.1.0-beta.10", false},
		{"v0.1.0", "v0.1.0-beta.10", true},
		{"v0.1.0-beta.10", "v0.1.0", false},
		{"v0.2.0-beta.1", "v0.1.9", true},
		{"v0.1.0", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s,%s)=%v", c.a, c.b, got)
		}
	}
}

func TestPick(t *testing.T) {
	list := []Release{
		{Tag: "v0.2.0-beta.1", Prerelease: true},
		{Tag: "v0.1.0"},
		{Tag: "v0.1.0-beta.9", Prerelease: true},
	}
	if r, _ := Pick(list, "stable"); r.Tag != "v0.1.0" {
		t.Errorf("stable: %s", r.Tag)
	}
	if r, _ := Pick(list, "beta"); r.Tag != "v0.2.0-beta.1" {
		t.Errorf("beta: %s", r.Tag)
	}
}
