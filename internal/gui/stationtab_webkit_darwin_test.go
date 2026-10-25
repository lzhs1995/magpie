//go:build darwin && cgo && !nogui

package gui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// A station's tab does what a browser tab does with what a station's page
// sends out of itself. A window.open with no click before it (a sign-in
// link opened after the request it waited on) and a link clicked to
// another site go to the browser, and the tab stays on the station. A
// reply sent as an attachment is saved to Downloads, as "name (1)" beside
// a file of that name already there. Without the tab's delegate the first
// two did nothing, or took the tab off the station, and the third was lost.
func TestStationTabSendsLinksOutAndSavesDownloads(t *testing.T) {
	out, err := runAppKit(t, 30*time.Second, []string{"MAGPIE_TEST_STATION_TAB=1"})
	if err != nil || !strings.Contains(string(out), "station tab: ok") {
		t.Fatalf("station tab: %v\n%s", err, out)
	}
}

func init() {
	if os.Getenv("MAGPIE_TEST_STATION_TAB") != "1" {
		return
	}
	var servers []*httptest.Server
	fail := func(format string, a ...any) {
		fmt.Printf("station tab: "+format+"\n", a...)
		for _, s := range servers {
			s.CloseClientConnections()
			s.Close()
		}
		os.Exit(1)
	}
	home := os.Getenv("CFFIXED_USER_HOME")
	if home == "" {
		fail("no temporary home to save downloads in")
	}
	dir := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "usage.csv"), []byte("old"), 0o600); err != nil {
		fail("%v", err)
	}
	// another site: a tab that went there would count a hit here
	var otherHits, exports atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		fmt.Fprint(w, "<p>another site</p>")
	}))
	servers = append(servers, other)
	const signIn = "https://sign-in.example/authorize?code=1"
	// each step waits until the one before reached the browser's opener,
	// so the page's three actions never overlap however slow the machine
	signedIn, wentAway := make(chan struct{}), make(chan struct{})
	var signOnce, awayOnce sync.Once
	station := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<a id="away" href="%s/page">away</a> <a id="csv" href="/export">csv</a>
<script>
(async () => {
  await fetch("/after?step=load");
  window.open(%q, "_blank", "noopener");
  await fetch("/after?step=signin");
  document.getElementById("away").click();
  await fetch("/after?step=away");
  document.getElementById("csv").click();
})();
</script>`, other.URL, signIn)
		case "/after":
			wait := map[string]chan struct{}{"signin": signedIn, "away": wentAway}[r.URL.Query().Get("step")]
			if wait != nil {
				select {
				case <-wait:
				case <-time.After(10 * time.Second):
				}
			}
			w.WriteHeader(http.StatusNoContent)
		case "/export":
			exports.Add(1)
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", `attachment; filename="usage.csv"`)
			fmt.Fprint(w, "day,cost\n2026-10-25,1.5\n")
		default:
			http.NotFound(w, r)
		}
	}))
	servers = append(servers, station)

	opened := make(chan string, 8)
	saved := make(chan [2]string, 4)
	stationOpenURL = func(u string) {
		opened <- u
		switch {
		case u == signIn:
			signOnce.Do(func() { close(signedIn) })
		case strings.HasPrefix(u, other.URL):
			awayOnce.Do(func() { close(wentAway) })
		}
	}
	stationDownloaded = func(path, err string) { saved <- [2]string{path, err} }

	app := application.New(application.Options{
		Name: "magpie station tab regression",
		Mac:  application.MacOptions{ActivationPolicy: application.ActivationPolicyAccessory},
		Assets: application.AssetOptions{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "<p>magpie</p>")
		})},
	})
	h := &host{app: app}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			setStationDownloadsDir(dir)
			if err := h.openStationTab("one", station.URL+"/"); err != nil {
				fail("open: %v", err)
			}
			var links []string
			var got [2]string
			for deadline := time.After(15 * time.Second); len(links) < 2 || got[0] == ""; {
				select {
				case u := <-opened:
					links = append(links, u)
				case got = <-saved:
					if got[1] != "" {
						fail("the download failed: %s", got[1])
					}
				case <-deadline:
					fail("timed out: links to the browser %q, export asked for %d times, download %q, the other site asked %d times",
						links, exports.Load(), got[0], otherHits.Load())
				}
			}
			sort.Strings(links)
			want := []string{other.URL + "/page", signIn}
			sort.Strings(want)
			if fmt.Sprint(links) != fmt.Sprint(want) {
				fail("links to the browser %q, want %q", links, want)
			}
			if n := otherHits.Load(); n != 0 {
				fail("the tab itself went to the other site (%d requests)", n)
			}
			if got[0] != filepath.Join(dir, "usage (1).csv") {
				fail("saved as %s, want usage (1).csv beside the one there", got[0])
			}
			b, err := os.ReadFile(got[0])
			if err != nil || string(b) != "day,cost\n2026-10-25,1.5\n" {
				fail("saved %q (%v)", b, err)
			}
			if b, _ := os.ReadFile(filepath.Join(dir, "usage.csv")); string(b) != "old" {
				fail("the file already there became %q", b)
			}
			fmt.Println("station tab: ok")
			for _, s := range servers {
				s.Close()
			}
			app.Quit()
		}()
	})
	if err := app.Run(); err != nil {
		panic(err)
	}
	os.Exit(0)
}
