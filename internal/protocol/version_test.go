package protocol

import "testing"

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.4.0", "0.3.0", true},
		{"v0.4.0", "0.3.9", true},
		{"0.10.0", "0.9.1", true}, // numeric, not lexical
		{"1.0.0", "0.99.99", true},
		{"0.3.0", "0.3.0", false},
		{"0.3.0", "0.4.0", false},
		{"0.4.0", "0.4.0-rc1", false}, // suffixes are ignored
		{"0.4.0", "dev", false},       // development builds
		{"0.4.0", "main-1a2b3c4", false},
		{"", "0.3.0", false},
		{"garbage", "0.3.0", false},
		{"0.4", "0.3.0", false},
	}
	for _, c := range cases {
		if got := NewerVersion(c.latest, c.current); got != c.want {
			t.Errorf("NewerVersion(%q, %q) = %v", c.latest, c.current, got)
		}
	}
}
