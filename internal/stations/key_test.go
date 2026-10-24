package stations

import (
	"os"
	"path/filepath"
	"testing"
)

// A station set up before this page existed keeps its page key where it
// already is, and the two shapes a reader's own credentials file comes in
// are both read: a nested one shared by several stations, and a flat one
// in the station's own folder. Neither is written to.
func TestKeyOfReadsBothCredentialShapes(t *testing.T) {
	root := t.TempDir()
	// shared, nested: {"linxi": {"web_password": "..."}}
	shared := filepath.Join(root, "CREDENTIALS.json")
	if err := os.WriteFile(shared, []byte(`{"linxi":{"web_password":"nested-key-1"},"baibei":{"web_password":"nested-key-2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// the station's own, flat: {"web_password": "..."}
	own := filepath.Join(root, "agentrouter-a")
	if err := os.MkdirAll(own, 0o700); err != nil {
		t.Fatal(err)
	}
	flat := filepath.Join(own, "CREDENTIALS.json")
	if err := os.WriteFile(flat, []byte(`{"web_password":"flat-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		s    Station
		want string
	}{
		{"nested", Station{Name: "linxi", Port: 3445, WebPort: 3446,
			KeyFile: shared, KeyPath: "linxi.web_password"}, "nested-key-1"},
		{"nested, the other station", Station{Name: "baibei", Port: 3455, WebPort: 3456,
			KeyFile: shared, KeyPath: "baibei.web_password"}, "nested-key-2"},
		{"flat, in the station's folder", Station{Name: "agentrouter-a", Port: 3465, WebPort: 3466,
			KeyFile: flat, KeyPath: "web_password"}, "flat-key"},
		{"flat, named relative to the folder", Station{Name: "agentrouter-a", Port: 3465, WebPort: 3466,
			KeyFile: "CREDENTIALS.json", KeyPath: "web_password"}, "flat-key"},
	} {
		got, err := KeyOf(c.s, root)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: key %q, want %q", c.name, got, c.want)
		}
	}

	// nothing was written into the reader's own credentials
	for _, p := range []string{shared, flat} {
		before, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := KeyOf(Station{Name: "linxi", Port: 3445, WebPort: 3446,
			KeyFile: shared, KeyPath: "linxi.web_password"}, root); err != nil {
			t.Fatal(err)
		}
		after, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
			t.Errorf("%s was written to", p)
		}
	}
}

// A key that can't be read is an error naming what was looked for, never
// an empty key passed on as if the station had none: an empty key would
// open the page with no key and read as "this station needs no key".
func TestKeyOfRefusesWhatItCantRead(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "CREDENTIALS.json")
	if err := os.WriteFile(bad, []byte(`{"linxi":{"web_password":123}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, path string }{
		{"the key isn't text", "linxi.web_password"},
		{"no such station in the file", "nope.web_password"},
		{"a path through a non-object", "linxi.web_password.deeper"},
	} {
		got, err := KeyOf(Station{Name: "linxi", Port: 3445, WebPort: 3446,
			KeyFile: bad, KeyPath: c.path}, root)
		if err == nil {
			t.Errorf("%s: no error, key %q", c.name, got)
		}
		if got != "" {
			t.Errorf("%s: gave back a key as well as failing: %q", c.name, got)
		}
	}
	// a file that isn't there is an error too, not a silent empty key
	if _, err := KeyOf(Station{Name: "x", Port: 3445, WebPort: 3446,
		KeyFile: filepath.Join(root, "gone.json"), KeyPath: "web_password"}, root); err == nil {
		t.Error("a missing key file was taken as no key")
	}
}

// With no KeyFile a station uses its own SecretsFile, the one Add writes:
// the behaviour stations made in the window have.
func TestKeyOfFallsBackToTheStationsOwnSecrets(t *testing.T) {
	root := t.TempDir()
	s := Station{Name: "made-here", Port: 3505, WebPort: 3506}
	folder := s.Folder(root)
	if err := MkdirFolder(folder); err != nil {
		t.Fatal(err)
	}
	// no secrets yet: no key, and no error — the station simply has none
	got, err := KeyOf(s, root)
	if err != nil || got != "" {
		t.Fatalf("before any key: %q %v", got, err)
	}
	if err := ensureKey(folder); err != nil {
		t.Fatal(err)
	}
	got, err = KeyOf(s, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 32 {
		t.Errorf("key %q, want 32 hex characters", got)
	}
	// ensureKey doesn't replace a key that is already there
	again, err := KeyOf(s, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureKey(folder); err != nil {
		t.Fatal(err)
	}
	third, err := KeyOf(s, root)
	if err != nil {
		t.Fatal(err)
	}
	if again != third {
		t.Errorf("the key changed under a second ensureKey: %q then %q", again, third)
	}
}
