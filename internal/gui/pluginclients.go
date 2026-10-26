package gui

import (
	"bytes"
	"strings"

	"github.com/yetone/magpie/internal/pluginrt"
)

// Where a built-in plugin's files go in the page that index.html doesn't
// name itself: its stylesheets after the page's own, its scripts after the
// kernel they register with and before app.js, which looks for their pages
// and tabs, and a page.js, which runs on app.js's helpers, before start.js
// shows the first page. TestBuiltinClientsLoaded holds these places.
var (
	pluginCSSAfter    = []byte(`<link rel="stylesheet" href="app.css">`)
	pluginScriptAfter = []byte(`<script src="kernel.js"></script>`)
	pluginPageBefore  = []byte(`<script src="start.js"></script>`)
)

// withPluginClients is the page with every built-in plugin's Client files
// it doesn't load already, so a plugin added in this tree (plugins/all) is
// loaded from its own manifest rather than by lines written into
// index.html, which upstream edits all the time. A file the page names
// itself is left where the page has it.
func withPluginClients(page []byte) []byte {
	var css, scripts, late []string
	for _, m := range pluginrt.Registered() {
		for _, c := range m.Client {
			name := "plugins/" + m.ID + "/" + c
			if bytes.Contains(page, []byte(`"`+name+`"`)) {
				continue
			}
			switch {
			case strings.HasSuffix(c, ".css"):
				css = append(css, `<link rel="stylesheet" href="`+name+`">`)
			case c == "page.js":
				late = append(late, `<script src="`+name+`"></script>`)
			default:
				scripts = append(scripts, `<script src="`+name+`"></script>`)
			}
		}
	}
	page = insertAfter(page, pluginCSSAfter, css)
	page = insertAfter(page, pluginScriptAfter, scripts)
	if len(late) > 0 {
		page = bytes.Replace(page, pluginPageBefore, []byte(strings.Join(late, "\n")+"\n"+string(pluginPageBefore)), 1)
	}
	return page
}

func insertAfter(page, anchor []byte, tags []string) []byte {
	if len(tags) == 0 {
		return page
	}
	return bytes.Replace(page, anchor, []byte(string(anchor)+"\n"+strings.Join(tags, "\n")), 1)
}
