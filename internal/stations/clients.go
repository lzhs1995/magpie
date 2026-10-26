package stations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The lines that open a client through a station, for the page to copy.
// They are contrib/lanes/mg's: before it starts anything it checks that
// the client is pointed at this station and carries this station's own
// key, and it leaves the client's own settings as they are.

// ClientLine is one of those lines and what it opens: "claude" with the
// profile it goes by, or "codex".
type ClientLine struct {
	Client  string `json:"client"`
	Profile string `json:"profile,omitempty"`
	Line    string `json:"line"`
}

// ProfilesDir is where Claude Code's settings for the stations are kept,
// a file each, where mg reads them unless its mg.toml says otherwise:
// profiles/ beside the stations' folders.
func ProfilesDir(root string) string { return filepath.Join(root, "profiles") }

// profileName is a profile mg takes by name (`mg cc <station> -k <name>`).
var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ClientLines is s's lines: Claude Code by each profile in dir pointed at
// s (`mg cc <station> -k <profile>`), or `mg cc <station>` while there is
// none, which says what to make; then Codex, `mg cx <station>`. A profile
// is read for its ANTHROPIC_BASE_URL alone: its key is neither kept nor
// shown.
func ClientLines(s Station, dir string) []ClientLine {
	var out []ClientLine
	for _, p := range profilesFor(s, dir) {
		out = append(out, ClientLine{Client: "claude", Profile: p, Line: "mg cc " + Quote(s.Name) + " -k " + Quote(p)})
	}
	if len(out) == 0 {
		out = append(out, ClientLine{Client: "claude", Line: "mg cc " + Quote(s.Name)})
	}
	return append(out, ClientLine{Client: "codex", Line: "mg cx " + Quote(s.Name)})
}

// profilesFor is the profiles in dir whose ANTHROPIC_BASE_URL is s's
// gateway, in the order of their file names. One mg wouldn't take by
// name is left out, as is one that can't be read or isn't a Claude Code
// settings file.
func profilesFor(s Station, dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		stem, ok := strings.CutSuffix(e.Name(), ".json")
		// profileName starts with a letter or digit: hidden ones and
		// templates (_name.json) aren't profiles, as mg skips them too
		if !ok || !profileName.MatchString(stem) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var doc struct {
			Env struct {
				Base string `json:"ANTHROPIC_BASE_URL"`
			} `json:"env"`
		}
		if json.Unmarshal(b, &doc) != nil {
			continue
		}
		if strings.TrimRight(doc.Env.Base, "/") == s.BaseURL() {
			names = append(names, stem)
		}
	}
	return names
}
