package stations

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
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
			Launch:  s.Launch(exe, f.Root),
			Full:    s.Full(exe, f.Root),
			BaseURL: s.BaseURL(), WebURL: s.WebURL(),
			Why: why,
		})
	}
	return out
}

// Start runs a station's magpie: exe, with the station's folder as its
// XDG_CONFIG_HOME and its own page key, in a process of its own that
// outlives this one. It is refused while something already holds the
// station's port — including another station of this file, which Validate
// has already kept apart.
//
// The environment it is given is this process's own, with the station's
// XDG_CONFIG_HOME, MAGPIE_ADDR and MAGPIE_WEB_KEY over it. The port is
// MAGPIE_ADDR and not the Port in the station's settings.json, which is
// the station's own file to write: GatewayAddr reads the variable first
// (internal/settings), so a station listens where the page says it does
// without this magpie reaching into another's settings.
func Start(ctx context.Context, f File, s Station, exe string) (int, error) {
	if exe == "" {
		return 0, errors.New("there is no magpie to start: this one's path isn't known")
	}
	switch st, pid, running, why := Probe(ctx, s); st {
	case Up, Listening:
		return pid, fmt.Errorf("%s is already served by pid %d", s.Name, pid)
	case Busy:
		_ = running
		return 0, errors.New(why)
	}
	folder := s.Folder(f.Root)
	if err := MkdirFolder(folder); err != nil {
		return 0, err
	}
	args := []string{"serve"}
	env := append(os.Environ(), "XDG_CONFIG_HOME="+folder, "MAGPIE_ADDR="+s.Addr())
	if s.WebPort != 0 {
		if s.KeyFile == "" {
			if err := ensureKey(folder); err != nil {
				return 0, err
			}
		}
		key, err := KeyOf(s, f.Root)
		if err != nil {
			return 0, err
		}
		if key == "" {
			return 0, fmt.Errorf("%s has no page key", s.Name)
		}
		args = append(args, "--web=127.0.0.1:"+strconv.Itoa(s.WebPort))
		env = append(env, "MAGPIE_WEB_KEY="+key)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.Dir = folder
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
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

// Stop ends the magpie serving a station, the way a reader closing it
// would: SIGTERM, so it finishes the requests it has (its own gateway
// decides that, not this process). A protected station is refused, and so
// is a port held by something that isn't a magpie.
func Stop(ctx context.Context, s Station) error {
	if s.Protected {
		return fmt.Errorf("%s is protected: it is stopped where it was started, not from here", s.Name)
	}
	st, pid, _, why := Probe(ctx, s)
	switch st {
	case Down:
		return fmt.Errorf("%s isn't running", s.Name)
	case Busy:
		return errors.New(why)
	}
	return proc.Terminate(pid)
}
