// Package stations is magpie's gateways on this computer, one magpie of
// its own each: the window's + makes one, names it, gives it a port and a
// folder for its settings, and hands back the one line that starts it.
//
// Why a magpie of its own and not several gateways in one: a magpie's
// gateway is one address (settings.GatewayAddr), and the key a session
// holds is let go when that key is resting or spent
// (internal/gateway/affinity.go), so a session can move to another key of
// the same magpie. Keeping one gateway's keys from another's is therefore
// a matter of separate processes, each with its own settings folder — what
// XDG_CONFIG_HOME names (internal/appdir). A station is that pair: a
// folder and the port its magpie listens on.
//
// Everything a station's own magpie shows is the magpie this one does:
// the same build, serving its own folder. This package starts and stops
// them and says what state each is in; it never writes inside another
// station's folder.
package stations

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// A Station is one gateway: a name the reader gave it, the folder its
// magpie keeps its settings in, and the ports it listens on.
type Station struct {
	// Name is the reader's own, and what its folder is called.
	Name string `yaml:"name" json:"name"`
	// Dir is the folder for XDG_CONFIG_HOME; empty is Root/Name.
	Dir string `yaml:"dir,omitempty" json:"dir"`
	// Port is where its gateway listens; WebPort is where its page does,
	// 0 for no page.
	Port    int `yaml:"port" json:"port"`
	WebPort int `yaml:"webPort,omitempty" json:"webPort"`
	// Protected stations are never stopped from the page: the ones
	// serving work the reader can't have cut off.
	Protected bool `yaml:"protected,omitempty" json:"protected"`
	// Note is the reader's own line about what this station is for.
	Note string `yaml:"note,omitempty" json:"note"`
	// KeyFile and KeyPath name where a station already keeps its page
	// key, for a station that was running before this page existed: the
	// file, and the dotted path to the key inside it. Empty means the
	// station's own SecretsFile, which Add writes. Nothing here ever
	// writes a key into KeyFile — a station set up by hand owns its own
	// credentials, and the page only reads them to open it.
	KeyFile string `yaml:"keyFile,omitempty" json:"keyFile,omitempty"`
	KeyPath string `yaml:"keyPath,omitempty" json:"keyPath,omitempty"`
	// Env is put over the environment the station's magpie is started
	// from, over the file's own Env: a value of "" takes the variable out,
	// as a proxy the station must not go through. It is shown on the page
	// and in the line written out, so it is no place for a key.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

// nameOK is a station's name: a folder name and a URL path both take it,
// and it is told apart from another at a glance.
var nameOK = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,39}$`)

// CheckName says what is wrong with a station's name, nil when nothing
// is.
func CheckName(name string) error {
	switch {
	case name == "":
		return errors.New("a station needs a name")
	case !nameOK.MatchString(name):
		return fmt.Errorf("a station's name is letters, digits, - . _ , 40 at most, starting with a letter or digit, not %q", name)
	case name == "." || name == "..":
		return fmt.Errorf("%q isn't a name a folder can have", name)
	}
	return nil
}

// CheckPort says what is wrong with a port for a station: one a user may
// listen on, or 0 where a station needs none (WebPort).
func CheckPort(p int, what string) error {
	if p != 0 && (p < 1024 || p > 65535) {
		return fmt.Errorf("a station's %s is a number from 1024 to 65535, not %d", what, p)
	}
	return nil
}

// Check says what is wrong with a station on its own, nil when nothing
// is. Whether its ports and folder clash with another's is Store's to
// say, which knows the others.
func (s Station) Check() error {
	if err := CheckName(s.Name); err != nil {
		return err
	}
	if err := CheckPort(s.Port, "port"); err != nil {
		return err
	}
	if s.Port == 0 {
		return errors.New("a station needs a port of its own")
	}
	if err := CheckPort(s.WebPort, "page's port"); err != nil {
		return err
	}
	if s.WebPort == s.Port {
		return fmt.Errorf("the gateway and the page can't share port %d", s.Port)
	}
	if s.Dir != "" && !filepath.IsAbs(s.Dir) {
		return fmt.Errorf("a station's folder is an absolute path, not %q", s.Dir)
	}
	return checkEnv(s.Env, s.Name)
}

// Folder is where the station's magpie keeps its settings: its own Dir,
// or one named after it under root.
func (s Station) Folder(root string) string {
	if s.Dir != "" {
		return s.Dir
	}
	return filepath.Join(root, s.Name)
}

// Addr is the station's gateway address, what a client is pointed at.
func (s Station) Addr() string { return "127.0.0.1:" + strconv.Itoa(s.Port) }

// BaseURL is the station's gateway as a client's base URL. The clients
// magpie writes take host:port with no /v1 of their own.
func (s Station) BaseURL() string { return "http://" + s.Addr() }

// WebURL is the station's page, "" when it has no page. The key the page
// asks for isn't in it: Secrets has that, read from the station's own
// folder when the reader opens it.
func (s Station) WebURL() string {
	if s.WebPort == 0 {
		return ""
	}
	return "http://127.0.0.1:" + strconv.Itoa(s.WebPort)
}

// State is what a station is doing now.
type State string

const (
	// Up: its port answers, and a magpie is listening on it.
	Up State = "up"
	// Down: nothing is listening on its port.
	Down State = "down"
	// Busy: something is listening on its port that isn't a magpie, so
	// starting this station would fail and nothing here may stop it.
	Busy State = "busy"
	// Listening: a magpie holds the port but a connection doesn't
	// complete — it is starting up, or wedged.
	Listening State = "listening"
)

// Launch is the one line that starts a station, for the reader to copy:
// `magpie serve --station=<name>`. Everything else the station needs —
// its folder, its port, its variables and its page key — magpie finds in
// stations.yml and the station's own folder, so no key is on the
// clipboard, and a station renamed or moved doesn't leave a stale line in
// someone's notes. When this window's stations.yml isn't the one a plain
// shell's magpie reads, the line names it (LaunchPrefix).
func (s Station) Launch(exe string) string {
	return LaunchPrefix() + Quote(Command(exe)) + " serve --station=" + Quote(s.Name)
}

// Command is how to name this magpie on a line the reader keeps: its own
// name when that name on the PATH is this same file, and its full path
// when it isn't. A reader who has magpie installed copies `magpie serve
// …`, and one running it from a folder of their own still gets a line
// that works.
func Command(exe string) string {
	if exe == "" {
		return "magpie"
	}
	base := filepath.Base(exe)
	found, err := exec.LookPath(base)
	if err != nil {
		return exe
	}
	if sameFile(found, exe) {
		return base
	}
	return exe
}

// sameFile says whether two paths are the same file, following symlinks:
// /usr/local/bin/magpie is often a link to the one in the app.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// Quote is v as one shell word.
func Quote(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\n\"'$`\\*?[]()<>|&;#~") {
		return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
	}
	return v
}

// Free says whether a port can be listened on now. A station's port that
// isn't free and has no magpie on it is Busy.
func Free(port int) bool {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// NextPort is a port no station in list has, from start upwards, and free
// now. step apart keeps a station's gateway and page from the next
// station's.
func NextPort(list []Station, start, step int) int {
	taken := map[int]bool{}
	for _, s := range list {
		taken[s.Port] = true
		if s.WebPort != 0 {
			taken[s.WebPort] = true
		}
	}
	for p := start; p <= 65535-step; p += step {
		if !taken[p] && !taken[p+1] && Free(p) && Free(p+1) {
			return p
		}
	}
	return 0
}

// MkdirFolder makes a station's folder, readable by its owner alone: the
// keys and the settings of a gateway are in it.
func MkdirFolder(path string) error { return os.MkdirAll(path, 0o700) }
