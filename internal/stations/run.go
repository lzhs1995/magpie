package stations

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// Starting and stopping a station, and saying what state it is in.
//
// A station this magpie started isn't its child for long: it is its own
// magpie, serving its own gateway, and it goes on when this one is
// quit — the four a reader runs by hand do. So a station is found again
// by what is listening on its port, not by a process this one remembers.

// Status is a station and what it is doing now.
type Status struct {
	Station `json:",inline" yaml:",inline"`
	// State is Up, Down, Busy or Listening.
	State State `json:"state"`
	// PID is the magpie serving it, 0 when none is.
	PID int `json:"pid,omitempty"`
	// Exe is what that process is running, for a station served by a
	// magpie other than this one's build.
	Exe string `json:"exe,omitempty"`
	// Folder is where its settings are, so the page can show it.
	Folder string `json:"folder"`
	// Launch is the one line that starts it; Full is the same start
	// written out, for a reader who wants no stations.yml in it.
	Launch string `json:"launch"`
	Full   string `json:"full"`
	// BaseURL is what a client is pointed at; WebURL is its page.
	BaseURL string `json:"baseURL"`
	WebURL  string `json:"webURL,omitempty"`
	// Clients is the lines that open Claude Code and Codex through it
	// (ClientLines).
	Clients []ClientLine `json:"clients"`
	// Why says what is wrong when the state isn't Up or Down.
	Why string `json:"why,omitempty"`
}

// Probe says what state a station is in. Nothing on its port is Down; a
// magpie listening and answering is Up; a magpie listening that doesn't
// answer is Listening (starting, or wedged); anything else holding the
// port is Busy, and nothing here stops it.
func Probe(ctx context.Context, s Station) (State, int, string, string) {
	pids, err := proc.ListeningOn(ctx, s.Port)
	if err != nil {
		return Down, 0, "", "the port can't be looked at: " + err.Error()
	}
	if len(pids) == 0 {
		return Down, 0, "", ""
	}
	pid := pids[0]
	exe, _ := proc.Executable(pid)
	if exe != "" && !proc.IsMagpie(exe) {
		return Busy, pid, exe, "port " + strconv.Itoa(s.Port) + " is held by " + filepath.Base(exe)
	}
	c, err := net.DialTimeout("tcp", s.Addr(), 2*time.Second)
	if err != nil {
		return Listening, pid, exe, "it holds the port but doesn't answer yet"
	}
	c.Close()
	return Up, pid, exe, ""
}

// Report is every station and its state, probed as they are asked for.
func Report(ctx context.Context, f File, exe string) []Status {
	out := make([]Status, 0, len(f.Stations))
	for _, s := range f.Stations {
		st, pid, running, why := Probe(ctx, s)
		out = append(out, Status{
			Station: s,
			State:   st, PID: pid, Exe: running,
			Folder:  s.Folder(f.Root),
			Launch:  s.Launch(exe),
			Full:    PlanOf(f, s, exe).Line(keyRead(s, f.Root)),
			BaseURL: s.BaseURL(), WebURL: s.WebURL(),
			Clients: ClientLines(s, ProfilesDir(f.Root)),
			Why:     why,
		})
	}
	return out
}

// Start runs a station's magpie as its Plan says, in a process of its
// own that outlives this one, writing to serve.log in its folder. It is
// refused while something already holds the station's port — including
// another station of this file, which Validate has already kept apart.
//
// The port is MAGPIE_ADDR and not the Port in the station's
// settings.json, which is the station's own file to write: GatewayAddr
// reads the variable first (internal/settings), so a station listens where
// the page says it does without this magpie reaching into another's
// settings.
func Start(ctx context.Context, f File, s Station, self string) (int, error) {
	p := PlanOf(f, s, self)
	if p.Exe == "" {
		return 0, errors.New("there is no magpie to start: this one's path isn't known")
	}
	switch st, pid, _, why := Probe(ctx, s); st {
	case Up, Listening:
		return pid, fmt.Errorf("%s is already served by pid %d", s.Name, pid)
	case Busy:
		return 0, errors.New(why)
	}
	if err := MkdirFolder(p.Dir); err != nil {
		return 0, err
	}
	key, err := PageKey(f, s)
	if err != nil {
		return 0, err
	}
	// appended to, so what a station said before it last stopped is
	// still there to read
	out, err := os.OpenFile(p.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	cmd := proc.Command(p.Exe, p.Args...)
	cmd.Env = p.Environ(os.Environ(), key)
	cmd.Dir = p.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, out, out
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Let it go: it serves its own gateway and outlives this magpie, so
	// nothing here waits on it. Release leaves no zombie behind.
	go func() { _ = cmd.Process.Release() }()
	return pid, nil
}

// PageKey is the key s's page is started with, "" when it has no page. A
// station with no KeyFile is given one in its own SecretsFile if it has
// none; one that names a KeyFile must have its key there already, since
// nothing here writes into a file the reader set up.
func PageKey(f File, s Station) (string, error) {
	if s.WebPort == 0 {
		return "", nil
	}
	if s.KeyFile == "" {
		if err := ensureKey(s.Folder(f.Root)); err != nil {
			return "", err
		}
	}
	key, err := KeyOf(s, f.Root)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", fmt.Errorf("%s has no page key", s.Name)
	}
	return key, nil
}

// PageURL is s's page with its key in the link, the one a browser opens
// signed in: "" and an error when s has no page.
func PageURL(f File, s Station) (string, error) {
	if s.WebURL() == "" {
		return "", fmt.Errorf("%s has no page: give it one in Edit", s.Name)
	}
	key, err := KeyOf(s, f.Root)
	if err != nil {
		return "", err
	}
	if key == "" {
		return s.WebURL(), nil
	}
	return s.WebURL() + "/?k=" + key, nil
}

// Stop ends the magpie serving a station, the way a reader closing it
// would: SIGTERM, so it finishes the requests it has (its own gateway
// decides that, not this process). A protected station is refused, and so
// is a port held by something that isn't a magpie.
func Stop(ctx context.Context, s Station) error {
	if s.Protected {
		return fmt.Errorf("%s is protected: it is stopped where it was started, not from here", s.Name)
	}
	return StopForce(ctx, s)
}

// StopForce is Stop for a station the reader said by name to stop even
// though it is protected (`magpie station stop <name> --force`). A port
// held by something that isn't a magpie is still refused.
func StopForce(ctx context.Context, s Station) error {
	st, pid, _, why := Probe(ctx, s)
	switch st {
	case Down:
		return fmt.Errorf("%s isn't running", s.Name)
	case Busy:
		return errors.New(why)
	}
	return proc.Terminate(pid)
}
