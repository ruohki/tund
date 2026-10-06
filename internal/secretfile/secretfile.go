// Package secretfile has the name rules and content markers that keep secrets
// out of tund serve: keys, credentials, app data and the like are never
// listed, served or used as a share root, and visitors can't upload files that
// other programs pick up on their own. It is a safety net, not data loss
// prevention.
package secretfile

import (
	"iter"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Fold returns the comparison key of a file name: NFC-normalized, Unicode
// case-folded and without default-ignorable code points, so "ID_RSA",
// "id_rſa" and a "Makefile" with a zero-width character inside compare like
// on the real file systems (APFS and NTFS fold more than strings.ToLower,
// HFS+ skips zero-width and bidi controls). Like APFS, it keeps the dotless
// "ıd_rsa" apart from "id_rsa".
func Fold(name string) string {
	for i := range len(name) {
		if name[i] >= utf8.RuneSelf {
			// A Caser must not be shared between goroutines; making one is free.
			return cases.Fold().String(norm.NFC.String(dropIgnorable(name)))
		}
	}
	// ASCII: NFC changes nothing and folding only lowers A-Z.
	return strings.ToLower(name)
}

// ignorable are Unicode's default-ignorable code points: soft hyphen,
// zero-width spaces and joiners, bidi controls, variation selectors, tags and
// the like. Nothing shows them, and HFS+ skips U+200C-200F, U+202A-202E,
// U+206A-206F and U+FEFF when it compares names.
var ignorable = &unicode.RangeTable{
	R16: []unicode.Range16{
		{0x00ad, 0x00ad, 1}, {0x034f, 0x034f, 1}, {0x061c, 0x061c, 1}, {0x115f, 0x1160, 1}, {0x17b4, 0x17b5, 1},
		{0x180b, 0x180f, 1}, {0x200b, 0x200f, 1}, {0x202a, 0x202e, 1}, {0x2060, 0x206f, 1}, {0x3164, 0x3164, 1},
		{0xfe00, 0xfe0f, 1}, {0xfeff, 0xfeff, 1}, {0xffa0, 0xffa0, 1}, {0xfff0, 0xfff8, 1},
	},
	R32:         []unicode.Range32{{0x1bca0, 0x1bca3, 1}, {0x1d173, 0x1d17a, 1}, {0xe0000, 0xe0fff, 1}},
	LatinOffset: 1,
}

func isIgnorable(r rune) bool { return unicode.Is(ignorable, r) }

// dropIgnorable removes the ignorable code points from s and keeps invalid
// UTF-8 as it is. Removing one can join the invalid bytes around it into
// another, hence the loop.
func dropIgnorable(s string) string {
	for strings.ContainsFunc(s, isIgnorable) {
		var b strings.Builder
		for s != "" {
			r, n := utf8.DecodeRuneInString(s)
			if !isIgnorable(r) {
				b.WriteString(s[:n])
			}
			s = s[n:]
		}
		s = b.String()
	}
	return s
}

// Denied reports whether an entry with this name must never be listed, served
// or used as a share root component. dir says whether the entry is a
// directory. rule names the matching rule for logs ("ssh key", "env file", ...).
func Denied(name string, dir bool) (rule string, denied bool) {
	if dir {
		return dirRules.find(Fold(name))
	}
	return fileRules.find(Fold(name))
}

// UploadRefused reports whether visitors may not upload a file with this
// (already sanitized) name: Denied names plus names that other programs pick
// up or parse on their own.
func UploadRefused(name string) (rule string, refused bool) {
	key := Fold(name)
	if rule, ok := fileRules.find(key); ok {
		return rule, true
	}
	return plantRules.find(key)
}

// maxBackups is how many backup markers a name may carry and still match the
// rules of the original ("id_rsa copy.old~").
const maxBackups = 3

// backupExts are what editors and people append to a copy.
var backupExts = []string{"~", ".bak", ".backup", ".old", ".orig", ".save", ".sav", ".swp", ".tmp", ".copy"}

// variants yields a Fold key and then the key with one, two and three backup
// markers removed (see trimBackup): "credentials (1).json.bak",
// "credentials (1).json", "credentials.json". Trailing dots and spaces never
// count (Windows drops them).
func variants(key string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for i := 0; ; i++ {
			key = strings.TrimRight(key, ". ")
			if key == "" || !yield(key) || i == maxBackups {
				return
			}
			var ok bool
			if key, ok = trimBackup(key); !ok {
				return
			}
		}
	}
}

// trimBackup removes one backup marker: a backup extension; a download number
// behind an extension (wget's "credentials.json.1", not the man page
// "shadow.5"); a copy marker at the end or before the extension, where file
// managers and browsers put it ("secrets copy.yml", "credentials (1).json");
// or a leading "copy of ".
func trimBackup(key string) (string, bool) {
	for _, s := range backupExts {
		if k, ok := strings.CutSuffix(key, s); ok {
			return k, true
		}
	}
	if i := strings.LastIndexByte(key, '.'); i > 0 && number(key[i+1:]) && strings.Contains(key[:i], ".") {
		return key[:i], true
	}
	if k, ok := trimCopy(key); ok {
		return k, true
	}
	if i := strings.LastIndexByte(key, '.'); i > 0 {
		if k, ok := trimCopy(key[:i]); ok {
			return k + key[i:], true
		}
	}
	return trimCopyOf(key)
}

// trimCopy removes a trailing copy marker: " (N)" or Firefox's "(N)",
// " copy", " copy N", the Windows " - copy", or a counter " N" or " - N"
// (Finder's "secrets 2.yml"). Markers without a word or parentheses only
// count up to 99, so a year stays part of the name ("Secrets 2023").
func trimCopy(s string) (string, bool) {
	if t, ok := strings.CutSuffix(s, ")"); ok {
		if i := strings.LastIndexByte(t, '('); i >= 0 && number(t[i+1:]) {
			if spaced := strings.HasSuffix(t[:i], " "); spaced || counter(t[i+1:]) {
				return strings.TrimSuffix(t[:i], " "), true
			}
		}
	}
	t := s
	if u := strings.TrimRight(s, "0123456789"); len(u) < len(s) {
		switch n := s[len(u):]; {
		case strings.HasSuffix(u, " copy "):
			t = u[:len(u)-1]
		case strings.HasSuffix(u, " - ") && counter(n):
			return u[:len(u)-3], true
		case strings.HasSuffix(u, " ") && counter(n):
			return u[:len(u)-1], true
		}
	}
	for _, m := range []string{" - copy", " copy"} {
		if u, ok := strings.CutSuffix(t, m); ok {
			return u, true
		}
	}
	return s, false
}

// trimCopyOf removes a leading "copy of " or "copy (N) of " (Google Drive,
// older Windows).
func trimCopyOf(s string) (string, bool) {
	rest, ok := strings.CutPrefix(s, "copy ")
	if !ok {
		return s, false
	}
	if n, t, ok := strings.Cut(rest, ") of "); ok && strings.HasPrefix(n, "(") && number(n[1:]) {
		return t, true
	}
	if t, ok := strings.CutPrefix(rest, "of "); ok {
		return t, true
	}
	return s, false
}

func number(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// counter reports a copy counter: a number of at most two digits.
func counter(s string) bool {
	return number(s) && len(s) <= 2
}

// A group is one rule of a table: the name the owner's log shows, the
// patterns it matches and the patterns that exempt a name from it. Patterns
// are Fold keys; '*' matches any run of characters, everything else itself.
type group struct {
	rule     string
	patterns []string
	except   []string
}

// rules is a compiled table: literal patterns and "*.ext" patterns in maps,
// the rest in order.
type rules struct {
	names map[string]string
	exts  map[string]string // by extension, with the dot
	globs []group
}

// newRules compiles a table. A literal in several groups keeps the first
// group's rule.
func newRules(groups []group) rules {
	rs := rules{names: map[string]string{}, exts: map[string]string{}}
	for _, g := range groups {
		var globs []string
		for _, p := range g.patterns {
			ext, isExt := strings.CutPrefix(p, "*")
			switch {
			case g.except != nil:
				globs = append(globs, p)
			case isExt && ext != "" && strings.LastIndexAny(ext, "*.") == 0:
				if rs.exts[ext] == "" {
					rs.exts[ext] = g.rule
				}
			case strings.Contains(p, "*"):
				globs = append(globs, p)
			case rs.names[p] == "":
				rs.names[p] = g.rule
			}
		}
		if globs != nil {
			g.patterns = globs
			rs.globs = append(rs.globs, g)
		}
	}
	return rs
}

// find matches a Fold key and its variants against the table.
func (rs rules) find(key string) (string, bool) {
	for k := range variants(key) {
		if rule, ok := rs.names[k]; ok {
			return rule, true
		}
		if i := strings.LastIndexByte(k, '.'); i >= 0 {
			if rule, ok := rs.exts[k[i:]]; ok {
				return rule, true
			}
		}
		for _, g := range rs.globs {
			if matchAny(g.patterns, k) && !matchAny(g.except, k) {
				return g.rule, true
			}
		}
	}
	return "", false
}

func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if match(p, name) {
			return true
		}
	}
	return false
}

// match reports whether name matches pattern, where '*' matches any run of
// characters and everything else only itself.
func match(pattern, name string) bool {
	prefix, rest, wild := strings.Cut(pattern, "*")
	if !wild {
		return name == pattern
	}
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	name = name[len(prefix):]
	for {
		part, more, wild := strings.Cut(rest, "*")
		if !wild {
			return strings.HasSuffix(name, part)
		}
		i := strings.Index(name, part)
		if i < 0 {
			return false
		}
		name, rest = name[i+len(part):], more
	}
}
