package gui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// ErrNoStationTabs says there are no tabs to open the station in, so the
// caller opens its page in the browser as it did before. Only macOS groups
// windows into tabs; elsewhere every station opens in the browser.
var ErrNoStationTabs = errors.New("no tabs to open a station in")

// openStationTabIn is the window host's openStationTab, set while the
// desktop app runs so a route can reach it without the Windows interface
// gaining a method every other host would have to answer. nil in `magpie
// web`, where there is no window of ours: the page opens the link itself.
var openStationTabIn func(name, addr string) error

// stationTabRoutes: POST /api/station-tab {"name","url"} loads a station's
// own page as a tab of the stations' window. It answers
//
//	{"tabbed": true}  the tab is open (or was, and is now selected)
//	{"tabbed": false} no tabs here: the page opens the link itself
//
// and a 4xx with {"error"} when the station's address is not one a tab may
// load. Nothing here starts, stops or reaches a station: the address comes
// from the stations plugin, which owns them.
func stationTabRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/station-tab", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		open := openStationTabIn
		if open == nil {
			writeJSON(rw, map[string]bool{"tabbed": false})
			return
		}
		switch err := open(in.Name, in.URL); {
		case err == nil:
			writeJSON(rw, map[string]bool{"tabbed": true})
		case errors.Is(err, ErrNoStationTabs):
			writeJSON(rw, map[string]bool{"tabbed": false})
		default:
			fail(rw, err)
		}
	})
}
