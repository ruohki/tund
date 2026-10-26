package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderPageBrand(t *testing.T) {
	s := &Server{cfg: &Config{}}
	render := func() string {
		r := httptest.NewRequest("GET", "http://gone.example/", nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		s.renderPage(w, r, 404, pageOffline, map[string]any{"Host": "gone.example"})
		return w.Body.String()
	}
	if body := render(); !strings.Contains(body, "</i>TUNd</div>") || !strings.Contains(body, "The TUNd client") {
		t.Errorf("default brand missing:\n%s", body)
	}
	s.runtime.Store(&Runtime{InstanceName: "Acme <Tunnels>"})
	body := render()
	if !strings.Contains(body, "</i>Acme &lt;Tunnels&gt;</div>") || !strings.Contains(body, "The Acme &lt;Tunnels&gt; client") {
		t.Errorf("instance name missing or unescaped:\n%s", body)
	}
}

func TestRenderPageLegalLinks(t *testing.T) {
	s := &Server{cfg: &Config{PublicScheme: "https", DashboardHost: "tund.example"}}
	render := func() string {
		r := httptest.NewRequest("GET", "http://gone.on.tund.example/", nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		s.renderPage(w, r, 404, pageNotFound, map[string]any{"Host": "gone.on.tund.example"})
		return w.Body.String()
	}
	s.runtime.Store(&Runtime{})
	body := render()
	if !strings.Contains(body, `href="https://tund.example/privacy"`) || strings.Contains(body, "/imprint") {
		t.Errorf("want a privacy link and no imprint link:\n%s", body)
	}
	s.runtime.Store(&Runtime{Imprint: true})
	if body := render(); !strings.Contains(body, `href="https://tund.example/imprint"`) {
		t.Errorf("imprint link missing:\n%s", body)
	}
}
