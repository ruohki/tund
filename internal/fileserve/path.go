package fileserve

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// splitPath splits an escaped request path into its decoded segments; dir
// reports a trailing slash. ok is false for paths that can't name a shared
// entry: empty interior segments ("//evil.com", "/a//b"), "." and "..",
// decoded "/" or "\", NUL and bad escapes. Paths are never cleaned.
func splitPath(escaped string) (segs []string, dir, ok bool) {
	rest, found := strings.CutPrefix(escaped, "/")
	if !found {
		return nil, false, false
	}
	if rest == "" {
		return nil, true, true
	}
	parts := strings.Split(rest, "/")
	if parts[len(parts)-1] == "" {
		dir, parts = true, parts[:len(parts)-1]
	}
	segs = make([]string, len(parts))
	for i, p := range parts {
		seg, err := url.PathUnescape(p)
		if err != nil || seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "/\\\x00") {
			return nil, false, false
		}
		segs[i] = seg
	}
	return segs, dir, true
}

// href returns the absolute, escaped URL path of segs, with a trailing slash
// for folders (and always for the share root). It never starts with "//".
func href(segs []string, dir bool) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(s))
	}
	if dir || len(segs) == 0 {
		b.WriteByte('/')
	}
	return b.String()
}

// join appends a name to a share-relative path ("." is the share root).
func join(rel, name string) string {
	if rel == "." || rel == "" {
		return name
	}
	return rel + "/" + name
}

// shown returns a name as pages and the owner's terminal display it: invalid
// UTF-8 becomes U+FFFD, and control characters and bidi marks become visible
// \uXXXX escapes, so a name can't hide its extension or rewrite the text
// around it.
func shown(name string) string {
	if utf8.ValidString(name) && strings.IndexFunc(name, invisible) < 0 {
		return name
	}
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(name, "\uFFFD") {
		if invisible(r) {
			fmt.Fprintf(&b, `\u%04X`, r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// invisible reports control characters and the marks that reorder text.
func invisible(r rune) bool {
	return r < 0x20 || r >= 0x7f && r <= 0x9f || bidi(r)
}

func bidi(r rune) bool {
	return r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069
}

// pathText is the display form of a URL path for messages: "/a/b/".
func pathText(segs []string, dir bool) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteByte('/')
		b.WriteString(shown(s))
	}
	if dir || len(segs) == 0 {
		b.WriteByte('/')
	}
	return b.String()
}
