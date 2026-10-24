// Package stations is the Stations page: the gateways on this computer,
// a magpie of its own each. + makes one, names it, gives it a port and a
// folder, hands back the one line that starts it, and opens its own page,
// where everything a single magpie offers is offered — because it is a
// single magpie, serving its own folder.
//
// The page is a plugin's own (docs/subsystems/plugin-runtime.md): it
// registers its page in the window's nav slot and its routes under its
// own prefix, so the core's handler has no room taken in it.
package stations

import (
	"github.com/yetone/magpie/internal/pluginrt"
	"github.com/yetone/magpie/internal/stations"
)

// Settings are the page's own.
type Settings struct {
	FirstPort int `json:"firstPort" title:"First port a new station is offered" description:"The + button offers the next free port from here up, in tens: the gateway's, and its page on the one after" min:"1024" max:"65000"`
}

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "stations", API: "^1", Requires: []string{"window"},
		Name: "Stations", Sub: "The gateways on this computer, a magpie of its own each: make one, name it, copy the line that starts it",
		One: "1 station", Many: "{n} stations",
		InUse: func() (int, error) {
			f, err := stations.Load()
			return len(f.Stations), err
		},
		Parts:    []pluginrt.Part{{Slot: "nav.page", Key: "stations"}},
		Settings: Settings{FirstPort: 3500},
		Apply:    apply,
		Client:   []string{"i18n.js", "stations.css", "client.js"},
	})
}
