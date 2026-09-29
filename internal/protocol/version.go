package protocol

import (
	"strconv"
	"strings"
)

// ParseVersion reads a release version ("0.4.0", "v0.4.0", "0.4.0-rc1"; the
// suffix is ignored) into major, minor and patch. Anything else (e.g. "dev")
// is not a release.
func ParseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// NewerVersion reports whether latest is a newer release than current. It is
// false when either isn't a release version (development builds never nag).
func NewerVersion(latest, current string) bool {
	l, ok1 := ParseVersion(latest)
	c, ok2 := ParseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}
