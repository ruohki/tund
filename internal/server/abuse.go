package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"tund/internal/protocol"
)

// Abuse protection (docs/SPEC.md "Abuse protection"): trust gating, deceptive
// hostnames, blocked hosts, Safe Browsing and phishing heuristics.

// defaultBlockedWords may not appear in hostnames chosen by non-trusted
// accounts. Entries starting with "=" only match a whole dash-separated
// label part (short or ambiguous words); others match anywhere.
var defaultBlockedWords = []string{
	"paypal", "microsoft", "office365", "outlook", "hotmail", "icloud", "=apple", "appleid", "google", "gmail",
	"amazon", "netflix", "facebook", "instagram", "whatsapp", "telegram", "linkedin", "twitter", "coinbase",
	"binance", "kraken", "metamask", "trustwallet", "ledger", "trezor", "blockchain", "opensea", "seedphrase",
	"wallet", "sparkasse", "volksbank", "commerzbank", "deutschebank", "postbank", "=ing", "chase", "wellsfargo",
	"citibank", "=hsbc", "barclays", "revolut", "=n26", "klarna", "=dhl", "fedex", "=ups", "=usps", "=dpd",
	"ebay", "steam", "roblox", "discord", "login", "signin", "logon", "verify", "verification", "password",
	"banking", "=bank", "recovery", "unlock", "=secure",
}

// hostnameAllowed checks a label chosen by a non-trusted account.
func hostnameAllowed(label string, words []string) (string, bool) {
	flat := strings.ReplaceAll(strings.ToLower(label), "-", "")
	parts := strings.Split(strings.ToLower(label), "-")
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		if exact, ok := strings.CutPrefix(w, "="); ok {
			for _, p := range parts {
				if p == exact {
					return exact, false
				}
			}
			continue
		}
		if strings.Contains(flat, strings.ReplaceAll(w, "-", "")) {
			return w, false
		}
	}
	return "", true
}

// checkBindAbuse applies trust gating and the hostname word list to a bind.
func (s *Server) checkBindAbuse(acct Account, proto, host string) error {
	if acct.IsAdmin || acct.Trusted {
		return nil
	}
	rt := s.rt()
	switch {
	case proto == protocol.ProtoTCP && !rt.UntrustedTCP:
		return bindError("TCP tunnels need a trusted account on this server; ask the administrator")
	case proto == protocol.ProtoTLS && !rt.UntrustedTLS:
		return bindError("TLS tunnels need a trusted account on this server; ask the administrator")
	}
	if host == "" || proto == protocol.ProtoTCP {
		return nil
	}
	if !s.certs.underBase(host) {
		if rt.UntrustedCustomDomains == "deny" {
			return bindError("custom domains need a trusted account on this server; ask the administrator")
		}
		for _, label := range strings.Split(host, ".") {
			if strings.HasPrefix(label, "xn--") {
				return bindError(host + " uses internationalized (punycode) labels, which need a trusted account on this server")
			}
			if w, ok := hostnameAllowed(label, rt.BlockedWords); !ok {
				return bindError(fmt.Sprintf("the domain %q is not allowed on this server because it contains %q; ask the administrator", host, w))
			}
		}
		return nil
	}
	label := strings.TrimSuffix(host, "."+s.cfg.BaseDomain)
	if w, ok := hostnameAllowed(label, rt.BlockedWords); !ok {
		return bindError(fmt.Sprintf("the name %q is not allowed on this server because it contains %q (it looks like a brand or a login page); choose another name", label, w))
	}
	return nil
}

// --- blocked hosts ---

func (s *Server) blockedReason(host string) (string, bool) {
	m := s.blocked.Load()
	if m == nil {
		return "", false
	}
	r, ok := (*m)[host]
	return r, ok
}

func (s *Server) loadBlocked(ctx context.Context) error {
	m, err := s.store.BlockedHosts(ctx)
	if err != nil {
		return err
	}
	s.blocked.Store(&m)
	// Stop tunnels on hostnames that just got blocked.
	for _, t := range s.reg.Tunnels() {
		if _, ok := m[t.Hostname]; ok {
			t.session.unbind(t.BindID, "this hostname was blocked by the administrator of "+s.cfg.DashboardHost)
		}
	}
	return nil
}

// blockAndReport blocks host, stops its tunnel, flags the owner and files a
// report. Used by the automatic detectors.
func (s *Server) blockAndReport(ctx context.Context, t *Tunnel, source, category, reason string, details map[string]any, block bool) {
	if block {
		if err := s.store.BlockHost(ctx, t.Hostname, reason); err != nil {
			logf("block %s: %v", t.Hostname, err)
		} else if err := s.loadBlocked(ctx); err != nil {
			logf("reload blocked hosts: %v", err)
		}
		s.store.Notify(ctx, "tund_config", map[string]string{"kind": "blocked_hosts"})
	}
	if err := s.store.FlagUser(ctx, t.UserID, reason); err != nil {
		logf("flag user %s: %v", t.UserID, err)
	}
	id, err := s.store.CreateAbuseReport(ctx, AbuseReport{
		Hostname: t.Hostname, URL: t.PublicURL, Category: category, Source: source,
		Description: reason, Details: details, UserID: t.UserID, TunnelID: t.ID,
	})
	if err != nil {
		logf("abuse report for %s: %v", t.Hostname, err)
		return
	}
	s.store.Notify(ctx, "tund_abuse", map[string]string{"id": id, "hostname": t.Hostname, "source": source})
	logf("abuse: %s %s (%s): %s", source, t.Hostname, map[bool]string{true: "blocked", false: "reported"}[block], reason)
}

// --- Google Safe Browsing ---

func (s *Server) safeBrowsingLoop(ctx context.Context) {
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if key := s.rt().SafeBrowsingKey; key != "" {
				if err := s.checkSafeBrowsing(ctx, key); err != nil {
					logf("safe browsing: %v", err)
				}
			}
		}
	}
}

func publicWebURL(t *Tunnel) string {
	if t.Proto == protocol.ProtoTLS {
		return "https://" + t.Hostname + "/"
	}
	return t.PublicURL + "/"
}

func (s *Server) checkSafeBrowsing(ctx context.Context, key string) error {
	byURL := map[string]*Tunnel{}
	for _, t := range s.reg.Tunnels() {
		if t.Proto == protocol.ProtoTCP || t.ownerTrusted.Load() {
			continue
		}
		byURL[publicWebURL(t)] = t
	}
	urls := make([]string, 0, len(byURL))
	for u := range byURL {
		urls = append(urls, u)
	}
	for len(urls) > 0 {
		n := min(len(urls), 500)
		batch := urls[:n]
		urls = urls[n:]
		matches, err := safeBrowsingLookup(ctx, key, batch)
		if err != nil {
			return err
		}
		for u, threat := range matches {
			if t := byURL[u]; t != nil {
				s.blockAndReport(ctx, t, "safe_browsing", categoryForThreat(threat),
					"flagged by Google Safe Browsing as "+threat, map[string]any{"threat_type": threat, "url": u}, true)
			}
		}
	}
	return nil
}

func categoryForThreat(t string) string {
	if t == "SOCIAL_ENGINEERING" {
		return "phishing"
	}
	return "malware"
}

// safeBrowsingLookup returns url → threat type for the URLs Google flags.
func safeBrowsingLookup(ctx context.Context, key string, urls []string) (map[string]string, error) {
	type entry struct {
		URL string `json:"url"`
	}
	entries := make([]entry, len(urls))
	for i, u := range urls {
		entries[i] = entry{u}
	}
	body, _ := json.Marshal(map[string]any{
		"client": map[string]string{"clientId": "tund", "clientVersion": Version},
		"threatInfo": map[string]any{
			"threatTypes":      []string{"MALWARE", "SOCIAL_ENGINEERING", "UNWANTED_SOFTWARE", "POTENTIALLY_HARMFUL_APPLICATION"},
			"platformTypes":    []string{"ANY_PLATFORM"},
			"threatEntryTypes": []string{"URL"},
			"threatEntries":    entries,
		},
	})
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://safebrowsing.googleapis.com/v4/threatMatches:find?key="+url.QueryEscape(key), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	var out struct {
		Matches []struct {
			ThreatType string `json:"threatType"`
			Threat     struct {
				URL string `json:"url"`
			} `json:"threat"`
		} `json:"matches"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	res := map[string]string{}
	for _, m := range out.Matches {
		res[m.Threat.URL] = m.ThreatType
	}
	return res, nil
}

// --- phishing heuristics ---

var phishBrands = []string{
	"paypal", "microsoft", "office 365", "outlook", "apple id", "icloud", "google", "gmail", "amazon", "netflix",
	"facebook", "instagram", "whatsapp", "coinbase", "binance", "metamask", "trust wallet", "ledger", "sparkasse",
	"volksbank", "commerzbank", "deutsche bank", "postbank", "chase", "wells fargo", "citibank", "hsbc", "barclays",
	"revolut", "n26", "klarna", "dhl", "fedex", "ups", "usps", "dpd", "ebay", "steam", "roblox", "discord",
}

var phishPhrases = []string{
	"verify your account", "verify your identity", "confirm your identity", "account has been suspended",
	"account has been locked", "unusual activity", "unusual sign-in", "seed phrase", "recovery phrase",
	"secret recovery", "private key", "12-word", "24-word", "credit card number", "card number", "cvv", "cvc",
	"expiration date", "social security", "update your payment", "payment information", "sign in to continue",
	"your parcel", "delivery fee", "customs fee",
}

var (
	rePassword = regexp.MustCompile(`(?i)<input[^>]+type\s*=\s*["']?password`)
	reTitle    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reHeading  = regexp.MustCompile(`(?is)<h[12][^>]*>(.*?)</h[12]>`)
	reFormURL  = regexp.MustCompile(`(?i)<form[^>]+action\s*=\s*["']?(https?://[^"'\s>]+)`)
	reTags     = regexp.MustCompile(`(?s)<[^>]*>`)
)

const phishThreshold = 6

// phishScore rates an HTML page; signals explain the score.
func phishScore(page, host string) (int, []string) {
	lower := strings.ToLower(page)
	score, signals := 0, []string{}
	add := func(n int, why string) { score += n; signals = append(signals, why) }
	if rePassword.MatchString(page) {
		add(3, "password field")
	}
	head := ""
	if m := reTitle.FindStringSubmatch(page); m != nil {
		head += " " + m[1]
	}
	for _, m := range reHeading.FindAllStringSubmatch(page, 5) {
		head += " " + reTags.ReplaceAllString(m[1], " ")
	}
	head = strings.ToLower(head)
	brandHits := 0
	for _, b := range phishBrands {
		if containsWord(head, b) {
			add(3, "brand in title/heading: "+b)
			break
		}
	}
	for _, b := range phishBrands {
		if brandHits < 2 && containsWord(lower, b) {
			brandHits++
		}
	}
	if brandHits > 0 {
		add(brandHits, "brand names in page")
	}
	phrases := 0
	for _, p := range phishPhrases {
		if phrases < 3 && strings.Contains(lower, p) {
			phrases++
			add(2, "phrase: "+p)
		}
	}
	for _, m := range reFormURL.FindAllStringSubmatch(page, 5) {
		if u, err := url.Parse(m[1]); err == nil && u.Hostname() != "" && u.Hostname() != host {
			add(2, "form posts to "+u.Hostname())
			break
		}
	}
	return score, signals
}

// containsWord matches w at word boundaries (letters/digits around it).
func containsWord(s, w string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], w)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(w)
		before := start == 0 || !isWordChar(s[start-1])
		after := end == len(s) || !isWordChar(s[end])
		if before && after {
			return true
		}
		i = start + 1
	}
}

func isWordChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c >= 0x80
}

// phishScanner analyses captured HTML off the request path.
type phishScanner struct {
	ch     chan phishJob
	mu     sync.Mutex
	recent map[string]time.Time // hostname -> last report
}

type phishJob struct {
	t         *Tunnel
	requestID string
	body      []byte
	encoding  string
	truncated bool
}

func newPhishScanner() *phishScanner {
	return &phishScanner{ch: make(chan phishJob, 256), recent: map[string]time.Time{}}
}

// maybeScan queues a finished exchange for analysis when it looks like a page.
func (s *Server) maybeScan(t *Tunnel, rec *RequestRecord) {
	rt := s.rt()
	if !rt.PhishingHeuristics || t.ownerTrusted.Load() || rec.Status != http.StatusOK || len(rec.RespBody) == 0 {
		return
	}
	if !strings.Contains(strings.ToLower(rec.RespHeaders.Get("Content-Type")), "text/html") {
		return
	}
	s.phish.mu.Lock()
	last, seen := s.phish.recent[t.Hostname]
	s.phish.mu.Unlock()
	if seen && time.Since(last) < 24*time.Hour {
		return
	}
	select {
	case s.phish.ch <- phishJob{t: t, requestID: rec.ID, body: rec.RespBody, encoding: rec.RespHeaders.Get("Content-Encoding"), truncated: rec.RespBodyTruncated}:
	default:
	}
}

func (s *Server) phishLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.phish.ch:
			page := j.body
			if enc := strings.ToLower(strings.TrimSpace(j.encoding)); enc != "" && enc != "identity" {
				dec, ok := decodeBody(enc, j.body, j.truncated)
				if !ok {
					continue
				}
				page = dec
			}
			score, signals := phishScore(string(page), j.t.Hostname)
			if score < phishThreshold {
				continue
			}
			s.phish.mu.Lock()
			if last, seen := s.phish.recent[j.t.Hostname]; seen && time.Since(last) < 24*time.Hour {
				s.phish.mu.Unlock()
				continue
			}
			s.phish.recent[j.t.Hostname] = time.Now()
			s.phish.mu.Unlock()
			s.blockAndReport(ctx, j.t, "heuristic", "phishing",
				fmt.Sprintf("page looks like phishing (score %d)", score),
				map[string]any{"score": score, "signals": signals, "request_id": j.requestID},
				s.rt().PhishingAutoBlock)
		}
	}
}
