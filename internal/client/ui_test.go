package client

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"tund/internal/protocol"
)

// testDisplay writes to a buffer without color, pretty or as log lines.
func testDisplay(pretty bool) (*Display, *strings.Builder) {
	out := &strings.Builder{}
	return &Display{out: out, pretty: pretty, said: map[string]bool{}}, out
}

// untimed drops the leading clock (pretty) or RFC 3339 time (log mode) of
// every line.
func untimed(t *testing.T, out string) []string {
	t.Helper()
	stamp := regexp.MustCompile(`^(\d\d:\d\d:\d\d  |\d{4}-\d\d-\d\dT\S+ )`)
	var lines []string
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if !stamp.MatchString(l) {
			t.Fatalf("line without time: %q", l)
		}
		lines = append(lines, stamp.ReplaceAllString(l, ""))
	}
	return lines
}

func TestDisplayHandlerTunnel(t *testing.T) {
	tn := &tunnel{spec: handlerSpec(http.NotFoundHandler()), status: statusOnline, url: "https://files.tund.example",
		static: true, authMode: protocol.AuthPassword, warning: "pin limit reached"}
	tn.spec.Display, tn.spec.Badge = "~/Projects/share", "upload"
	tn.spec.Notes = []string{
		"visitors can browse, download and upload files · uploads never replace existing files",
		"hidden from visitors: dotfiles and secret files (.env, keys, credentials)",
	}
	v := (&Client{}).view(tn)

	d, out := testDisplay(true)
	d.TunnelOnline(v)
	want := "Forwarding    https://files.tund.example → ~/Projects/share  [static]  [upload]  [password]\n" +
		"              visitors can browse, download and upload files · uploads never replace existing files\n" +
		"              hidden from visitors: dotfiles and secret files (.env, keys, credentials)\n" +
		"              ⚠ pin limit reached\n"
	if out.String() != want {
		t.Errorf("pretty:\n%s\nwant:\n%s", out, want)
	}

	d, out = testDisplay(false)
	d.TunnelOnline(v)
	want = `tunnel online name=files url=https://files.tund.example local=~/Projects/share auth=password static=true badge=upload warning="pin limit reached"`
	if got := untimed(t, out.String()); len(got) != 1 || got[0] != want {
		t.Errorf("log:\n%q\nwant:\n%q", got, want)
	}

	tn.spec.Badge = ""
	d, out = testDisplay(false)
	d.TunnelOnline((&Client{}).view(tn))
	if strings.Contains(out.String(), "badge=") {
		t.Errorf("empty badge logged: %s", out)
	}
}

func TestDisplayUpload(t *testing.T) {
	for _, c := range []struct {
		ev          UploadEvent
		pretty, log string
	}{
		{
			UploadEvent{Path: "/inbox/notes.txt", Requested: "notes.txt", Size: 12, Remote: "203.0.113.9"},
			"↑ saved   /inbox/notes.txt · 12 B · 203.0.113.9",
			"upload saved path=/inbox/notes.txt size=12 remote=203.0.113.9",
		},
		{
			UploadEvent{Path: "/inbox/report (1).pdf", Requested: "report.pdf", Size: 2457600, Renamed: true, Taken: "report.pdf",
				User: "alice@example.com", Remote: "203.0.113.7"},
			"↑ saved   /inbox/report (1).pdf · 2.3 MB · alice@example.com · report.pdf was taken",
			`upload saved path="/inbox/report (1).pdf" requested=report.pdf size=2457600 user=alice@example.com remote=203.0.113.7`,
		},
		{
			// Cleaning up changed the name: the line names the file that was
			// there, never one that doesn't exist.
			UploadEvent{Path: "/inbox/a_b (1).txt", Requested: "a:b.txt", Size: 8, Renamed: true, Taken: "a_b.txt", Remote: "203.0.113.7"},
			"↑ saved   /inbox/a_b (1).txt · 8 B · 203.0.113.7 · a_b.txt was taken",
			`upload saved path="/inbox/a_b (1).txt" requested=a:b.txt size=8 remote=203.0.113.7`,
		},
		{
			// Without Taken, the name the visitor sent stands in.
			UploadEvent{Path: "/inbox/notes (2).txt", Requested: "notes.txt", Size: 3, Renamed: true, Taken: "", Remote: "203.0.113.7"},
			"↑ saved   /inbox/notes (2).txt · 3 B · 203.0.113.7 · notes.txt was taken",
			`upload saved path="/inbox/notes (2).txt" requested=notes.txt size=3 remote=203.0.113.7`,
		},
		{
			UploadEvent{Path: "/inbox/x (1).txt", Requested: "x.txt", Size: 1, Renamed: true, Taken: "x\u202e.txt", Remote: "203.0.113.7"},
			`↑ saved   /inbox/x (1).txt · 1 B · 203.0.113.7 · x\u202e.txt was taken`,
			`upload saved path="/inbox/x (1).txt" requested=x.txt size=1 remote=203.0.113.7`,
		},
		{
			UploadEvent{Path: "/inbox/big.iso", Requested: "big.iso", Size: 1288490188, Remote: "203.0.113.7", Error: "the disk is full"},
			"↑ failed  /inbox/big.iso · 1.2 GB received · 203.0.113.7 · the disk is full",
			`upload failed path=/inbox/big.iso received=1288490188 remote=203.0.113.7 error="the disk is full"`,
		},
		{
			UploadEvent{Path: "/inbox/a.txt", Requested: "a.txt", Remote: "203.0.113.7", Error: "the upload broke off"},
			"↑ failed  /inbox/a.txt · 203.0.113.7 · the upload broke off",
			`upload failed path=/inbox/a.txt received=0 remote=203.0.113.7 error="the upload broke off"`,
		},
		{
			// Visitors pick names: escape what could drive the terminal or
			// reorder the line.
			UploadEvent{Path: "/inbox/x.txt", Requested: "x\x1b[2J\u202etxt.exe", Size: 3, User: "mallory\a", Remote: "203.0.113.7",
				Error: "refused\nnext"},
			`↑ failed  /inbox/x.txt · 3 B received · mallory\a · refused\nnext`,
			`upload failed path=/inbox/x.txt requested=x\x1b[2J\u202etxt.exe received=3 user=mallory\a remote=203.0.113.7 error=refused\nnext`,
		},
	} {
		d, out := testDisplay(true)
		d.Upload(c.ev)
		if got := untimed(t, out.String()); len(got) != 1 || got[0] != c.pretty {
			t.Errorf("pretty:\n%q\nwant:\n%q", got, c.pretty)
		}
		d, out = testDisplay(false)
		d.Upload(c.ev)
		if got := untimed(t, out.String()); len(got) != 1 || got[0] != c.log {
			t.Errorf("log:\n%q\nwant:\n%q", got, c.log)
		}
	}
}

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"report (1).pdf":          "report (1).pdf",
		"Café 日本\u00a0x.txt":      "Café 日本\u00a0x.txt",
		"👩\u200d💻.txt":            `👩\u200d💻.txt`,
		"a\tb\r\n":                `a\tb\r\n`,
		"\u009b31m":               `\u009b31m`, // C1 CSI
		"bad\xffutf8":             "bad\ufffdutf8",
		"\u2028\u202eexe.txt\x7f": `\u2028\u202eexe.txt\x7f`,
	} {
		if got := printable(in); got != want {
			t.Errorf("printable(%q) = %q, want %q", in, got, want)
		}
		if got := Printable(in); got != want {
			t.Errorf("Printable(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 2457600: "2.3 MB", 1288490188: "1.2 GB"} {
		if got := HumanBytes(n); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDisplayPretty(t *testing.T) {
	for _, pretty := range []bool{true, false} {
		if d, _ := testDisplay(pretty); d.Pretty() != pretty {
			t.Errorf("Pretty() = %v, want %v", d.Pretty(), pretty)
		}
	}
	// Plain log lines when asked for, even on a terminal.
	if NewDisplay(true).Pretty() {
		t.Error("NewDisplay(true) draws the interactive view")
	}
}
