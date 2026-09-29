package client

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

func displayHost(server string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(server, "https://"), "http://")
	return strings.TrimRight(s, "/")
}

// loginPrompt shows the user code and where to enter it.
func (d *Display) loginPrompt(server string, dc deviceCode, opened bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.pretty {
		d.println(fmt.Sprintf("To log in to %s, open %s and confirm the code %s", server, dc.VerificationURL, dc.UserCode))
		d.println("  or open " + dc.VerificationURLComplete)
		d.println("Waiting for approval…")
		return
	}

	type line struct{ plain, styled string }
	title := "Log in to " + displayHost(server)
	lines := []line{
		{title, d.c(bold, title)},
		{"", ""},
		{"Your code   " + dc.UserCode, d.c(dim, "Your code   ") + d.c(bold+cyan, dc.UserCode)},
		{"Approve at  " + dc.VerificationURL, d.c(dim, "Approve at  ") + dc.VerificationURL},
	}
	inner := 0
	for _, l := range lines {
		inner = max(inner, utf8.RuneCountInString(l.plain))
	}
	inner += 6
	d.println("")
	if inner+2 <= d.width() {
		border := d.c(dim, "╭"+strings.Repeat("─", inner)+"╮")
		d.println(border)
		empty := d.c(dim, "│") + strings.Repeat(" ", inner) + d.c(dim, "│")
		d.println(empty)
		for _, l := range lines {
			pad := inner - 3 - utf8.RuneCountInString(l.plain)
			d.println(d.c(dim, "│") + "   " + l.styled + strings.Repeat(" ", pad) + d.c(dim, "│"))
		}
		d.println(empty)
		d.println(d.c(dim, "╰"+strings.Repeat("─", inner)+"╯"))
	} else {
		for _, l := range lines {
			d.println(l.styled)
		}
	}
	d.println("")
	if opened {
		d.println("Opened your browser — check that the code matches, then approve.")
		d.println(d.c(dim, "If nothing opened, visit: ") + dc.VerificationURLComplete)
	} else {
		d.println("Open this link on any device, check that the code matches, then approve:")
		d.println("  " + d.c(bold, dc.VerificationURLComplete))
	}
	d.println("")
}

// LoggedIn confirms a successful login.
func (d *Display) LoggedIn(account, server, configPath string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	who := account
	if who == "" {
		who = "your account"
	}
	if !d.pretty {
		d.println(fmt.Sprintf("Logged in to %s as %s (authtoken saved to %s)", server, who, configPath))
		return
	}
	d.println(d.c(green, "✓") + " Logged in to " + displayHost(server) + " as " + d.c(bold, who))
	d.println(d.c(dim, "  Authtoken saved to "+configPath))
}

// spinner shows an in-place "waiting" line with elapsed time on terminals.
type spinner struct {
	d        *Display
	start    time.Time
	deadline time.Time
	label    string

	mu      sync.Mutex
	msg     string
	lastLen int
	quit    chan struct{}
	done    chan struct{}
	once    sync.Once
}

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (d *Display) startSpinner(label string, deadline time.Time) *spinner {
	s := &spinner{d: d, start: time.Now(), deadline: deadline, label: label, quit: make(chan struct{}), done: make(chan struct{})}
	if !d.pretty {
		close(s.done)
		return s
	}
	go s.loop()
	return s
}

func (s *spinner) loop() {
	defer close(s.done)
	t := time.NewTicker(120 * time.Millisecond)
	defer t.Stop()
	for i := 0; ; i++ {
		s.render(spinFrames[i%len(spinFrames)])
		select {
		case <-s.quit:
			s.clear()
			return
		case <-t.C:
		}
	}
}

func clock(d time.Duration) string {
	d = d.Round(time.Second)
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

func (s *spinner) render(frame string) {
	s.mu.Lock()
	msg := s.msg
	s.mu.Unlock()
	plain := fmt.Sprintf("%s %s… %s", frame, s.label, clock(time.Since(s.start)))
	styled := s.d.c(cyan, frame) + " " + s.label + "… " + s.d.c(dim, clock(time.Since(s.start)))
	left := time.Until(s.deadline)
	if left < 2*time.Minute {
		extra := "  (code expires in " + clock(left) + ")"
		plain += extra
		styled += s.d.c(yellow, extra)
	}
	if msg != "" {
		plain += "  " + msg
		styled += "  " + s.d.c(yellow, msg)
	}
	n := utf8.RuneCountInString(plain)
	s.d.mu.Lock()
	fmt.Fprint(s.d.out, "\r"+styled+strings.Repeat(" ", max(0, s.lastLen-n)))
	s.d.mu.Unlock()
	s.lastLen = n
}

func (s *spinner) clear() {
	s.d.mu.Lock()
	fmt.Fprint(s.d.out, "\r"+strings.Repeat(" ", s.lastLen)+"\r")
	s.d.mu.Unlock()
}

func (s *spinner) note(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

func (s *spinner) stop() {
	s.once.Do(func() { close(s.quit) })
	<-s.done
}
