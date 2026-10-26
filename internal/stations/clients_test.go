package stations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The page's Claude Code lines are the profiles pointed at the station,
// one each, by the name mg takes them by: a trailing / is the same
// gateway, another station's, a template, a hidden one, one mg wouldn't
// take by name and one that isn't JSON are none of them. With none, the
// line is `mg cc <station>`, which says what to make. Codex is one line.
func TestClientLinesAreTheStationsProfiles(t *testing.T) {
	dir := t.TempDir()
	profile := func(name, base string) {
		t.Helper()
		b := `{"env":{"ANTHROPIC_BASE_URL":"` + base + `","ANTHROPIC_AUTH_TOKEN":"sk-not-on-the-page-0123456789"}}`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	profile("linxi-s2.json", "http://127.0.0.1:3445/")
	profile("linxi-s1.json", "http://127.0.0.1:3445")
	profile("baibei-s3.json", "http://127.0.0.1:3455")
	// its name says linxi; where it is pointed says baibei, and mg goes by that
	profile("linxi-moved.json", "http://127.0.0.1:3455")
	profile("_template.json", "http://127.0.0.1:3445")
	profile(".linxi-old.json", "http://127.0.0.1:3445")
	profile("linxi copy.json", "http://127.0.0.1:3445")
	profile("linxi-s1.json.bak", "http://127.0.0.1:3445")
	for name, b := range map[string]string{
		"broken.json": `{"env":`,
		// a second env, the one mg reads, that isn't one: the gateway in
		// the first is read before the error is
		"odd.json": `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:3445"},"env":7}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "linxi-dir.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	linxi := Station{Name: "linxi", Port: 3445, WebPort: 3446}
	want := []ClientLine{
		{Client: "claude", Profile: "linxi-s1", Line: "mg cc linxi -k linxi-s1"},
		{Client: "claude", Profile: "linxi-s2", Line: "mg cc linxi -k linxi-s2"},
		{Client: "codex", Line: "mg cx linxi"},
	}
	if got := ClientLines(linxi, dir); !reflect.DeepEqual(got, want) {
		t.Errorf("linxi:\n got %+v\nwant %+v", got, want)
	}
	fresh := Station{Name: "fresh", Port: 3500}
	want = []ClientLine{{Client: "claude", Line: "mg cc fresh"}, {Client: "codex", Line: "mg cx fresh"}}
	if got := ClientLines(fresh, dir); !reflect.DeepEqual(got, want) {
		t.Errorf("no profile: %+v", got)
	}
	if got := ClientLines(fresh, filepath.Join(dir, "nowhere")); !reflect.DeepEqual(got, want) {
		t.Errorf("no profiles folder: %+v", got)
	}
}

// The list the page draws carries the lines, from profiles/ beside the
// stations' folders, and no profile's key.
func TestReportCarriesClientLinesAndNoKey(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(ProfilesDir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	b := `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:3725","ANTHROPIC_AUTH_TOKEN":"sk-not-on-the-page-0123456789"}}`
	if err := os.WriteFile(filepath.Join(ProfilesDir(root), "a-s1.json"), []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	f := File{Root: root, Stations: []Station{{Name: "a", Port: 3725}}}
	list := Report(context.Background(), f, "/usr/local/bin/magpie")
	if len(list) != 1 || len(list[0].Clients) != 2 || list[0].Clients[0].Line != "mg cc a -k a-s1" {
		t.Fatalf("%+v", list)
	}
	out, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "sk-not-on-the-page") {
		t.Errorf("a profile's key is in the list: %s", out)
	}
}
