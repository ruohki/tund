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
