package cli

import (
	"context"
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

// runStation runs the named station in this process's place, as its
// Plan says: the page's Start runs the same one. rest is what was on the
// line besides --station: a --web of the reader's own wins over the
// station's page port, so the long line's behaviour is still reachable.
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
			return fmt.Errorf("magpie serve: no station is called %q, and %s sets up none · the window's Stations page makes one, or %s names the stations.yml it keeps", name, stations.Path(), stations.StationsVar)
		}
		return fmt.Errorf("magpie serve: no station is called %q in %s · there is %s", name, stations.Path(), strings.Join(known, ", "))
	}
	// one already serving it is said so, rather than a second magpie
	// started only to fail on the port, or to fight over its folder
	switch st, pid, _, why := stations.Probe(context.Background(), s); st {
	case stations.Up, stations.Listening:
		return fmt.Errorf("magpie serve: %s is already served by pid %d at %s", name, pid, s.BaseURL())
	case stations.Busy:
		return fmt.Errorf("magpie serve: %s: %s", name, why)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	p := stations.PlanOf(f, s, self)
	if hasWebFlag(rest) {
		p = p.WithoutPage()
	}
	if err := stations.MkdirFolder(p.Dir); err != nil {
		return err
	}
	key := ""
	if p.Key {
		if key, err = stations.PageKey(f, s); err != nil {
			return fmt.Errorf("magpie serve: %w", err)
		}
	}
	args := append(append([]string{p.Exe}, p.Args...), rest...)
	return stationExec(p.Exe, args, p.Environ(os.Environ(), key))
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
