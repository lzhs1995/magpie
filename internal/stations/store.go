package stations

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/steady"
	"gopkg.in/yaml.v3"
)

// stations.yml holds the stations the reader made, beside plugins.yml in
// this magpie's own folder. Only this magpie writes it; a station's own
// folder is the station's, and nothing here writes inside one but the
// page key a new station needs to be opened with.

// File is stations.yml.
type File struct {
	// Root is where a station's folder goes when it names none, beside
	// this magpie's own folder by default.
	Root string `yaml:"root,omitempty"`
	// Stations are the reader's own, in the order the page shows them.
	Stations []Station `yaml:"stations"`
}

// SecretsFile is where a station's page key is kept, in the station's own
// folder: the line that starts it reads the key from there, so no key is
// in stations.yml, on the clipboard, or in the window's HTML.
const SecretsFile = "station-secrets.json"

// Secrets is what SecretsFile holds.
type Secrets struct {
	WebKey string `json:"webKey,omitempty"`
}

const header = "# magpie's stations: the gateways on this computer, a magpie of its own\n" +
	"# each (internal/stations). The window's Stations page writes this file.\n"

var fileMu sync.RWMutex

// Path is stations.yml.
func Path() string { return filepath.Join(appdir.Config(), "stations.yml") }

// DefaultRoot is where a station's folder goes when stations.yml names no
// root: magpie-stations beside this magpie's own folder, so a station's
// settings are never inside this one's.
func DefaultRoot() string {
	return filepath.Join(filepath.Dir(appdir.Config()), "magpie-stations")
}

// Load reads stations.yml. A file that isn't there is no stations and no
// error; one that can't be read or parsed is an error, never silently no
// stations: the page would otherwise offer to make a station over one
// already there.
func Load() (File, error) {
	fileMu.RLock()
	defer fileMu.RUnlock()
	return load()
}

func load() (File, error) {
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return File{Root: DefaultRoot()}, nil
	}
	if err != nil {
		return File{}, err
	}
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("%s: %w", Path(), err)
	}
	if f.Root == "" {
		f.Root = DefaultRoot()
	}
	return f, nil
}

// Save writes stations.yml whole or not at all, every station checked
// against the others first.
func Save(f File) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	return save(f)
}

func save(f File) error {
	if err := Validate(f); err != nil {
		return err
	}
	b, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		return err
	}
	return steady.WriteFile(Path(), append([]byte(header), b...), 0o600)
}

// Validate says what is wrong with the file as a whole: each station on
// its own, and no two sharing a name, a port or a folder.
func Validate(f File) error {
	names, ports, dirs := map[string]bool{}, map[int]string{}, map[string]bool{}
	for _, s := range f.Stations {
		if err := s.Check(); err != nil {
			return err
		}
		if names[s.Name] {
			return fmt.Errorf("two stations are called %q", s.Name)
		}
		names[s.Name] = true
		for _, p := range []int{s.Port, s.WebPort} {
			if p == 0 {
				continue
			}
			if other, taken := ports[p]; taken {
				return fmt.Errorf("%s and %s both want port %d", other, s.Name, p)
			}
			ports[p] = s.Name
		}
		d := filepath.Clean(s.Folder(f.Root))
		if dirs[d] {
			return fmt.Errorf("two stations keep their settings in %s", d)
		}
		dirs[d] = true
	}
	return nil
}

// Add puts a station in stations.yml, makes its folder and gives it a
// page key of its own. It is refused when its name, port or folder is
// another's.
func Add(s Station) (File, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	f, err := load()
	if err != nil {
		return File{}, err
	}
	f.Stations = append(f.Stations, s)
	if err := Validate(f); err != nil {
		return File{}, err
	}
	folder := s.Folder(f.Root)
	if err := MkdirFolder(folder); err != nil {
		return File{}, err
	}
	// a station that names its own KeyFile keeps the key it already has
	if s.WebPort != 0 && s.KeyFile == "" {
		if err := ensureKey(folder); err != nil {
			return File{}, err
		}
	}
	if err := save(f); err != nil {
		return File{}, err
	}
	return f, nil
}

// Edit replaces the station called name with s, which may rename it. Its
// folder isn't moved or removed: the settings and keys in it are the
// reader's, and a rename that left them behind would quietly make a new
// empty station of an old one. A renamed station keeps the folder it had.
func Edit(name string, s Station) (File, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	f, err := load()
	if err != nil {
		return File{}, err
	}
	at := -1
	for i, x := range f.Stations {
		if x.Name == name {
			at = i
			break
		}
	}
	if at < 0 {
		return File{}, fmt.Errorf("no station is called %q", name)
	}
	if f.Stations[at].Protected && !s.Protected {
		return File{}, fmt.Errorf("%s is protected: take that off in stations.yml yourself", name)
	}
	if s.Dir == "" && s.Name != name {
		s.Dir = f.Stations[at].Folder(f.Root)
	}
	f.Stations[at] = s
	if err := Validate(f); err != nil {
		return File{}, err
	}
	folder := s.Folder(f.Root)
	if err := MkdirFolder(folder); err != nil {
		return File{}, err
	}
	// a station that names its own KeyFile keeps the key it already has
	if s.WebPort != 0 && s.KeyFile == "" {
		if err := ensureKey(folder); err != nil {
			return File{}, err
		}
	}
	if err := save(f); err != nil {
		return File{}, err
	}
	return f, nil
}

// Remove takes a station out of stations.yml, leaving its folder where it
// is: what is in it is the reader's, and a page that deleted a gateway's
// keys on a mis-click would be a page nobody could trust. A protected
// station is refused.
func Remove(name string) (File, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	f, err := load()
	if err != nil {
		return File{}, err
	}
	kept := make([]Station, 0, len(f.Stations))
	found := false
	for _, s := range f.Stations {
		switch {
		case s.Name != name:
			kept = append(kept, s)
		case s.Protected:
			return File{}, fmt.Errorf("%s is protected and stays", name)
		default:
			found = true
		}
	}
	if !found {
		return File{}, fmt.Errorf("no station is called %q", name)
	}
	f.Stations = kept
	if err := save(f); err != nil {
		return File{}, err
	}
	return f, nil
}

// Find is the station called name, and whether it is there.
func (f File) Find(name string) (Station, bool) {
	for _, s := range f.Stations {
		if s.Name == name {
			return s, true
		}
	}
	return Station{}, false
}

// ensureKey gives a station a page key of its own if it has none. An
// unreadable secrets file is an error, never written over: the key in it
// is what the reader's own running station is using.
func ensureKey(folder string) error {
	p := filepath.Join(folder, SecretsFile)
	b, err := os.ReadFile(p)
	switch {
	case err == nil:
		var s Secrets
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if s.WebKey != "" {
			return nil
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	k := make([]byte, 16)
	if _, err := rand.Read(k); err != nil {
		return err
	}
	out, err := json.MarshalIndent(Secrets{WebKey: hex.EncodeToString(k)}, "", "  ")
	if err != nil {
		return err
	}
	return steady.WriteFile(p, append(out, '\n'), 0o600)
}

// Key is a station's page key, "" when it has none. It is read when the
// reader opens the station and never kept in the window.
func Key(folder string) (string, error) {
	b, err := os.ReadFile(filepath.Join(folder, SecretsFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var s Secrets
	if err := json.Unmarshal(b, &s); err != nil {
		return "", err
	}
	return s.WebKey, nil
}

// KeyOf is a station's page key, from wherever that station keeps it: its
// own SecretsFile, or the KeyFile it names, at the dotted KeyPath inside
// it. A station that was set up by hand before this page existed keeps
// its key where it already is; nothing here writes into that file.
func KeyOf(s Station, root string) (string, error) {
	if s.KeyFile == "" {
		return Key(s.Folder(root))
	}
	p := s.KeyFile
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.Folder(root), p)
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%s: the key file %s isn't there", s.Name, p)
	}
	if err != nil {
		return "", err
	}
	var any1 any
	if err := json.Unmarshal(b, &any1); err != nil {
		return "", fmt.Errorf("%s: %w", p, err)
	}
	path := s.KeyPath
	if path == "" {
		path = "web_password"
	}
	v, err := dig(any1, path)
	if err != nil {
		return "", fmt.Errorf("%s in %s: %w", path, p, err)
	}
	key, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s in %s isn't text", path, p)
	}
	return key, nil
}

// dig walks a dotted path through decoded JSON.
func dig(v any, path string) (any, error) {
	for _, part := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%q isn't inside an object", part)
		}
		v, ok = m[part]
		if !ok {
			return nil, fmt.Errorf("there is no %q", part)
		}
	}
	return v, nil
}
