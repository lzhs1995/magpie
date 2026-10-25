package stations

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/yetone/magpie/internal/appdir"
)

// A station is started three ways — the page's Start, `magpie serve
// --station=<name>` in a terminal, and the line written out for a reader
// who wants no stations.yml in it — and all three are one Plan, so what
// the page starts and what the terminal starts can't drift apart.

// StationsVar names a stations.yml other than the one in this magpie's own
// folder: a terminal's `magpie serve --station` then finds the stations a
// window run with another XDG_CONFIG_HOME keeps. It is no station's own:
// a station's magpie keeps its own stations, if any, and never writes this
// one's.
const StationsVar = "MAGPIE_STATIONS"

// reserved are the variables a Plan sets itself: what makes the station
// the one it is. A station's Env can't set them.
var reserved = map[string]bool{
	"XDG_CONFIG_HOME": true,
	"MAGPIE_ADDR":     true,
	"MAGPIE_WEB_KEY":  true,
	StationsVar:       true,
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// checkEnv says what is wrong with an Env, nil when nothing is.
func checkEnv(env map[string]string, whose string) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch {
		case !envName.MatchString(k):
			return fmt.Errorf("%s: %q isn't a variable's name", whose, k)
		case reserved[k]:
			return fmt.Errorf("%s: %s is the station's own, set from its folder, port and key", whose, k)
		}
	}
	return nil
}

// A Plan is how one station is started: the magpie that serves it, its
// arguments, the folder it runs in and logs to, and the variables put over
// the environment it is started from or taken out of it.
type Plan struct {
	Exe  string
	Args []string
	Dir  string
	// Log is the file a station started from the page writes to; one
	// started in a terminal writes to the terminal.
	Log string
	// Set and Unset are put over the environment the station is started
	// from; Set has no empty value, which Unset is instead.
	Set   map[string]string
	Unset []string
	// Key is whether the station's page key goes in MAGPIE_WEB_KEY, read
	// as it is started: the key itself is never in a Plan.
	Key bool
}

// PlanOf is the Plan for s in f. self is this magpie, which serves the
// station when f names no Runner.
func PlanOf(f File, s Station, self string) Plan {
	folder := s.Folder(f.Root)
	p := Plan{
		Exe:  f.Runner,
		Args: []string{"serve"},
		Dir:  folder,
		Log:  filepath.Join(folder, "serve.log"),
		Set:  map[string]string{},
	}
	if p.Exe == "" {
		p.Exe = self
	}
	// the file's variables, then the station's own over them; "" unsets
	env := map[string]string{
		// a cache of its own beside its folder, as the four started by
		// hand have: two magpies writing one cache would trip over each other
		"XDG_CACHE_HOME": filepath.Clean(folder) + "-cache",
	}
	for k, v := range f.Env {
		env[k] = v
	}
	for k, v := range s.Env {
		env[k] = v
	}
	env["XDG_CONFIG_HOME"] = folder
	env["MAGPIE_ADDR"] = s.Addr()
	env[StationsVar] = ""
	if s.WebPort != 0 {
		p.Args = append(p.Args, "--web=127.0.0.1:"+strconv.Itoa(s.WebPort))
		p.Key = true
	}
	for k, v := range env {
		if v == "" {
			p.Unset = append(p.Unset, k)
		} else {
			p.Set[k] = v
		}
	}
	sort.Strings(p.Unset)
	return p
}

// WithoutPage is p with no page of its own: a --web on the reader's own
// line wins over the station's page port, and the key that goes with it.
func (p Plan) WithoutPage() Plan {
	args := make([]string, 0, len(p.Args))
	for _, a := range p.Args {
		if !strings.HasPrefix(a, "--web=") {
			args = append(args, a)
		}
	}
	p.Args, p.Key = args, false
	return p
}

// setKeys is Set's names in the order a line lists them: the folder and
// the address that make the station the one it is first, the rest sorted.
func (p Plan) setKeys() []string {
	var first, rest []string
	for _, k := range []string{"XDG_CONFIG_HOME", "MAGPIE_ADDR"} {
		if _, ok := p.Set[k]; ok {
			first = append(first, k)
		}
	}
	for k := range p.Set {
		if k != "XDG_CONFIG_HOME" && k != "MAGPIE_ADDR" {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(first, rest...)
}

// Environ is base with the plan put over it, and key as MAGPIE_WEB_KEY
// when the plan has a page. A variable already there is replaced rather
// than repeated: which of two wins is not left to the program started.
func (p Plan) Environ(base []string, key string) []string {
	drop := map[string]bool{"MAGPIE_WEB_KEY": p.Key}
	for _, k := range p.Unset {
		drop[k] = true
	}
	for k := range p.Set {
		drop[k] = true
	}
	out := make([]string, 0, len(base)+len(p.Set)+1)
	for _, kv := range base {
		if k, _, found := strings.Cut(kv, "="); found && drop[k] {
			continue
		}
		out = append(out, kv)
	}
	for _, k := range p.setKeys() {
		out = append(out, k+"="+p.Set[k])
	}
	if p.Key {
		out = append(out, "MAGPIE_WEB_KEY="+key)
	}
	return out
}

// Line is the plan as one line of POSIX shell, keyCmd the shell that
// reads the page key so the key itself is never on it.
func (p Plan) Line(keyCmd string) string {
	parts := []string{"env"}
	for _, k := range p.Unset {
		parts = append(parts, "-u", k)
	}
	for _, k := range p.setKeys() {
		parts = append(parts, Quote(k+"="+p.Set[k]))
	}
	if p.Key {
		parts = append(parts, `MAGPIE_WEB_KEY="$(`+keyCmd+`)"`)
	}
	parts = append(parts, Quote(p.Exe))
	for _, a := range p.Args {
		parts = append(parts, Quote(a))
	}
	return strings.Join(parts, " ")
}

// keyRead is the shell that reads s's page key from wherever s keeps it:
// its own SecretsFile, or the KeyFile it names at KeyPath — with plutil
// on a Mac, where it is always there, and python3 elsewhere.
func keyRead(s Station, root string) string {
	if s.KeyFile == "" {
		p := filepath.Join(s.Folder(root), SecretsFile)
		return "grep -o '\"webKey\"[^\"]*\"[^\"]*\"' " + Quote(p) +
			" | sed 's/.*\"\\([^\"]*\\)\"$/\\1/'"
	}
	file := s.KeyFile
	if !filepath.IsAbs(file) {
		file = filepath.Join(s.Folder(root), file)
	}
	path := s.KeyPath
	if path == "" {
		path = "web_password"
	}
	if runtime.GOOS == "darwin" {
		return "plutil -extract " + Quote(path) + " raw -o - " + Quote(file)
	}
	return "python3 -c " + Quote(`import functools,json,sys;print(functools.reduce(lambda v,k:v[k],sys.argv[2].split("."),json.load(open(sys.argv[1]))))`) +
		" " + Quote(file) + " " + Quote(path)
}

// plainPath is the stations.yml a magpie run from an ordinary shell
// reads: the one in its default folder, or the portable one's.
func plainPath() string {
	if p := appdir.Portable(); p != "" {
		return filepath.Join(p, "stations.yml")
	}
	h, err := appdir.Home()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".config", "magpie", "stations.yml")
}

// LaunchPrefix is what a terminal's line needs before `magpie serve
// --station` to find these stations: nothing when they are where a plain
// shell's magpie looks, and StationsVar naming the file when they aren't.
func LaunchPrefix() string {
	if filepath.Clean(Path()) == filepath.Clean(plainPath()) {
		return ""
	}
	return StationsVar + "=" + Quote(Path()) + " "
}
