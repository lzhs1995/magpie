package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/yetone/magpie/internal/stations"
)

// `magpie serve --station=<name>` is the one line that starts a station,
// short enough to keep: everything else the station needs — the folder
// its settings are in, the port its gateway listens on and the key its
// page asks for — is in stations.yml and the station's own folder, which
// the Stations page wrote (internal/stations).
//
// It starts the station in this process rather than beside it: the
// variables are put in and magpie is run again in its place, so what runs
// is an ordinary `magpie serve`, the same thing the long line starts, and
// Ctrl-C in this terminal ends the station as a reader expects. Nothing
// here is a second way to serve a gateway — it is the same way, with the
// station's own settings found rather than typed.

// stationExec is syscall.Exec; a test stands in for it.
var stationExec = syscall.Exec

// stationFlag pulls --station=<name> out of args, and says whether it was
// there. Two of them is an error: a station is one gateway.
func stationFlag(args []string) (rest []string, name string, err error) {
	for _, a := range args {
		switch {
		case a == "--station":
			return nil, "", errors.New("magpie serve: --station needs a name, as --station=work")
		case strings.HasPrefix(a, "--station="):
			if name != "" {
				return nil, "", errors.New("magpie serve: --station twice; a station is one gateway")
			}
			name = strings.TrimPrefix(a, "--station=")
			if name == "" {
				return nil, "", errors.New("magpie serve: --station= has no name")
			}
		default:
			rest = append(rest, a)
		}
	}
	return rest, name, nil
}

// runStation runs the named station in this process's place. rest is what
// was on the line besides --station: a --web of the reader's own wins over
// the station's page port, so the long line's behaviour is still reachable.
func runStation(name string, rest []string) error {
	if err := stations.CheckName(name); err != nil {
		return fmt.Errorf("magpie serve: %w", err)
	}
	f, err := stations.Load()
	if err != nil {
		return err
	}
	s, there := f.Find(name)
	if !there {
		known := make([]string, 0, len(f.Stations))
		for _, x := range f.Stations {
			known = append(known, x.Name)
		}
		if len(known) == 0 {
			return fmt.Errorf("magpie serve: no station is called %q, and no station is set up yet · the window's Stations page makes one", name)
		}
		return fmt.Errorf("magpie serve: no station is called %q · there is %s", name, strings.Join(known, ", "))
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	folder := s.Folder(f.Root)
	if err := stations.MkdirFolder(folder); err != nil {
		return err
	}

	// The station's own environment, over this process's: the folder its
	// settings are in, and the port its gateway listens on. MAGPIE_ADDR
	// is what GatewayAddr reads first (internal/settings), so the station
	// listens where the page says it does without this run writing inside
	// the station's settings.json.
	env := envWith(os.Environ(), map[string]string{
		"XDG_CONFIG_HOME": folder,
		"MAGPIE_ADDR":     s.Addr(),
	})

	args := []string{exe, "serve"}
	args = append(args, rest...)
	// The station's page, unless the line asked for its own --web.
	if s.WebPort != 0 && !hasWebFlag(rest) {
		key, err := stations.Key(folder)
		if err != nil {
			return err
		}
		if key == "" {
			return fmt.Errorf("magpie serve: %s has no page key in %s", name, stations.SecretsFile)
		}
		env = envWith(env, map[string]string{"MAGPIE_WEB_KEY": key})
		args = append(args, "--web="+strings.TrimPrefix(s.WebURL(), "http://"))
	}
	return stationExec(exe, args, env)
}

// hasWebFlag says whether the line already asks for a page of its own.
func hasWebFlag(args []string) bool {
	for _, a := range args {
		if a == "--web" || strings.HasPrefix(a, "--web=") {
			return true
		}
	}
	return false
}

// envWith is env with set over it: a variable already there is replaced
// rather than repeated, since which of two wins is the exec'd program's
// to decide and not something to leave to it.
func envWith(env []string, set map[string]string) []string {
	out := make([]string, 0, len(env)+len(set))
	for _, kv := range env {
		k, _, found := strings.Cut(kv, "=")
		if found {
			if _, over := set[k]; over {
				continue
			}
		}
		out = append(out, kv)
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	return out
}
