package stations

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/yetone/magpie/internal/pluginrt"
	"github.com/yetone/magpie/internal/stations"
)

// The page's routes, under /api/plugins/x/stations/. Each one answers
// with the whole list and its states, as the core's pages do: the page
// draws what it is given rather than keeping its own idea of the state.

func apply(c *pluginrt.Ctx) error {
	setConf(c.Config)
	c.OnDispose(func() { setConf(nil) })
	c.HandleFunc("GET list", list)
	c.HandleFunc("POST new", create)
	c.HandleFunc("POST edit", edit)
	c.HandleFunc("POST remove", remove)
	c.HandleFunc("POST start", start)
	c.HandleFunc("POST stop", stop)
	c.HandleFunc("GET open", open)
	return nil
}

// exe is this magpie, the one a station is started with: a station runs
// the same build this window does.
func exe() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

type listJSON struct {
	Root      string            `json:"root"`
	Exe       string            `json:"exe"`
	FirstPort int               `json:"firstPort"`
	Stations  []stations.Status `json:"stations"`
	Next      map[string]int    `json:"next"`
}

func firstPort(c map[string]any) int {
	if v, ok := c["firstPort"].(float64); ok && v >= 1024 {
		return int(v)
	}
	return stations.FirstPort
}

// conf is the page's composed config, kept at Apply so the routes read
// the same values Plugins › Built-in set. A route runs on the window's
// own goroutines while Reconcile runs on another, so it is behind a lock.
var (
	confMu sync.RWMutex
	conf   map[string]any
)

func setConf(c map[string]any) {
	confMu.Lock()
	conf = c
	confMu.Unlock()
}

func config() map[string]any {
	confMu.RLock()
	defer confMu.RUnlock()
	return conf
}

func list(rw http.ResponseWriter, r *http.Request) {
	f, err := stations.Load()
	if err != nil {
		fail(rw, err)
		return
	}
	first := firstPort(config())
	p := stations.NextPort(f.Stations, first, 10)
	writeJSON(rw, listJSON{
		Root: f.Root, Exe: exe(), FirstPort: first,
		Stations: stations.Report(r.Context(), f, exe()),
		Next:     map[string]int{"port": p, "webPort": p + 1},
	})
}

// body reads a request's station, the page's own shape: the fields a
// reader fills in, never a folder of this magpie's own choosing.
type body struct {
	Name    string `json:"name"`
	Was     string `json:"was"` // the name before an edit
	Port    int    `json:"port"`
	WebPort int    `json:"webPort"`
	Note    string `json:"note"`
	Dir     string `json:"dir"`
	NoWeb   bool   `json:"noWeb"`
}

func (b body) station() stations.Station {
	s := stations.Station{Name: b.Name, Port: b.Port, WebPort: b.WebPort, Note: b.Note, Dir: b.Dir}
	if b.NoWeb {
		s.WebPort = 0
	}
	return s
}

func read(rw http.ResponseWriter, r *http.Request) (body, bool) {
	var b body
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		fail(rw, err)
		return body{}, false
	}
	return b, true
}

func create(rw http.ResponseWriter, r *http.Request) {
	b, ok := read(rw, r)
	if !ok {
		return
	}
	s := b.station()
	s.Env = stations.NewEnv()
	if _, err := stations.Add(s); err != nil {
		fail(rw, err)
		return
	}
	list(rw, r)
}

func edit(rw http.ResponseWriter, r *http.Request) {
	b, ok := read(rw, r)
	if !ok {
		return
	}
	was := b.Was
	if was == "" {
		was = b.Name
	}
	f, err := stations.Load()
	if err != nil {
		fail(rw, err)
		return
	}
	old, there := f.Find(was)
	if !there {
		fail(rw, fmt.Errorf("no station is called %q", was))
		return
	}
	s := b.station()
	s.Protected = old.Protected // the page never takes protection off
	// what the form doesn't carry stays as it was: a station set up by
	// hand that lost its KeyFile would be given a new key of its own, and
	// one that lost its Dir would move to an empty folder
	s.KeyFile, s.KeyPath, s.Env = old.KeyFile, old.KeyPath, old.Env
	if s.Dir == "" {
		s.Dir = old.Dir
	}
	if _, err := stations.Edit(was, s); err != nil {
		fail(rw, err)
		return
	}
	list(rw, r)
}

func remove(rw http.ResponseWriter, r *http.Request) {
	b, ok := read(rw, r)
	if !ok {
		return
	}
	if _, err := stations.Remove(b.Name); err != nil {
		fail(rw, err)
		return
	}
	list(rw, r)
}

func start(rw http.ResponseWriter, r *http.Request) {
	b, ok := read(rw, r)
	if !ok {
		return
	}
	f, err := stations.Load()
	if err != nil {
		fail(rw, err)
		return
	}
	s, there := f.Find(b.Name)
	if !there {
		fail(rw, fmt.Errorf("no station is called %q", b.Name))
		return
	}
	if _, err := stations.Start(r.Context(), f, s, exe()); err != nil {
		fail(rw, err)
		return
	}
	list(rw, r)
}

func stop(rw http.ResponseWriter, r *http.Request) {
	b, ok := read(rw, r)
	if !ok {
		return
	}
	f, err := stations.Load()
	if err != nil {
		fail(rw, err)
		return
	}
	s, there := f.Find(b.Name)
	if !there {
		fail(rw, fmt.Errorf("no station is called %q", b.Name))
		return
	}
	if err := stations.Stop(r.Context(), s); err != nil {
		fail(rw, err)
		return
	}
	list(rw, r)
}

// open hands back the link that opens a station's page, with the key it
// asks for, read from the station's own folder as it is asked for: the
// key is in no list the page holds and in no line the reader copies.
func open(rw http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	f, err := stations.Load()
	if err != nil {
		fail(rw, err)
		return
	}
	s, there := f.Find(name)
	if !there {
		fail(rw, fmt.Errorf("no station is called %q", name))
		return
	}
	url, err := stations.PageURL(f, s)
	if err != nil {
		fail(rw, err)
		return
	}
	writeJSON(rw, map[string]string{"url": url})
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(v)
}

func fail(rw http.ResponseWriter, err error) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(rw).Encode(map[string]string{"error": err.Error()})
}
