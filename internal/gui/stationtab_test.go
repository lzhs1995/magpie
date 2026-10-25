//go:build !nogui

package gui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stationTab posts body to the route with openStationTabIn set to open,
// and puts the hook back after.
func stationTab(t *testing.T, open func(name, addr string) error, body string) *httptest.ResponseRecorder {
	t.Helper()
	was := openStationTabIn
	openStationTabIn = open
	t.Cleanup(func() { openStationTabIn = was })
	mux := http.NewServeMux()
	stationTabRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/station-tab", strings.NewReader(body)))
	return w
}

// `magpie web` has no window host: the route says no tab, so the page
// opens the link itself.
func TestStationTabNoHost(t *testing.T) {
	w := stationTab(t, nil, `{"name":"linxi","url":"http://127.0.0.1:3446/"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"tabbed":false`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// The host gets the name and the address as the page sent them, and its
// nil answers that the tab is open.
func TestStationTabOpens(t *testing.T) {
	var gotName, gotAddr string
	w := stationTab(t, func(name, addr string) error {
		gotName, gotAddr = name, addr
		return nil
	}, `{"name":"agentrouter-a","url":"http://127.0.0.1:3466/?k=x"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"tabbed":true`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if gotName != "agentrouter-a" || gotAddr != "http://127.0.0.1:3466/?k=x" {
		t.Fatalf("host got %q %q", gotName, gotAddr)
	}
}

// ErrNoStationTabs, wrapped or not, falls back to the browser rather than
// failing the click.
func TestStationTabFallsBack(t *testing.T) {
	w := stationTab(t, func(string, string) error {
		return errors.Join(errors.New("no window"), ErrNoStationTabs)
	}, `{"name":"baibei","url":"http://127.0.0.1:3456/"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"tabbed":false`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// Any other error is the page's to show, and a body that isn't JSON never
// reaches the host.
func TestStationTabErrors(t *testing.T) {
	w := stationTab(t, func(string, string) error {
		return errors.New("a station's page is served over http")
	}, `{"name":"x","url":"file:///etc/passwd"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "served over http") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	called := false
	w = stationTab(t, func(string, string) error { called = true; return nil }, `not json`)
	if w.Code != 400 || called {
		t.Fatalf("%d %s called=%v", w.Code, w.Body, called)
	}
}
