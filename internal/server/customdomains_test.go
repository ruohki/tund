package server

import "testing"

func TestCustomDomainsEnabled(t *testing.T) {
	on, off := true, false
	cases := []struct {
		global, admin bool
		override      *bool
		want          bool
	}{
		{false, false, nil, false}, // default: off
		{true, false, nil, true},   // turned on for the instance
		{false, true, nil, true},   // admins don't need the setting
		{false, false, &on, true},  // enabled for this account
		{true, false, &off, false}, // disabled for this account
		{false, true, &off, false}, // the override wins for admins too
	}
	for _, c := range cases {
		if got := customDomainsEnabled(c.global, c.admin, c.override); got != c.want {
			t.Errorf("customDomainsEnabled(%v, %v, %v) = %v", c.global, c.admin, c.override, got)
		}
	}
}
