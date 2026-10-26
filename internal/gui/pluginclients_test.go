package gui

import (
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/pluginrt"
)

// The page loads each built-in plugin's files once: the ones index.html
// names where it names them, and the others from the plugin's manifest
// (the stations plugin, which index.html doesn't name, so the fork leaves
// that file as upstream has it). A file loaded twice would register its
// plugin twice, which the kernel refuses.
func TestPluginClientsLoadedOnce(t *testing.T) {
	page, err := os.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "plugins/stations/") {
		t.Fatal("index.html names the stations plugin's files; its manifest is to")
	}
	served := string(versionedPage(page))
	again := string(versionedPage(withPluginClients(page)))
	if served != again {
		t.Fatal("adding the plugins' files to a page that has them added them again")
	}
	for _, m := range pluginrt.Registered() {
		for _, c := range m.Client {
			name := "plugins/" + m.ID + "/" + c
			if n := strings.Count(served, `="`+name+`?v=`); n != 1 {
				t.Errorf("%s is loaded %d times", name, n)
			}
		}
	}
	if !strings.Contains(served, `href="plugins/stations/stations.css?v=`) || !strings.Contains(served, `src="plugins/stations/client.js?v=`) {
		t.Fatal("the stations plugin's files aren't in the page")
	}
}
