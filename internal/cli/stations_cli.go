package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/yetone/magpie/internal/stations"
)

// `magpie station …` is the Stations page in a terminal: the same list,
// the same Start and Stop, the same + — each one the page's own code
// (internal/stations), so a station started here is the station the page
// starts, with its folder, port, variables and page key from stations.yml.
// A launcher of the reader's own (contrib/lanes/mg) reads `ls --json`
// rather than stations.yml, so what a station is has one reading.

const stationUsage = `magpie station — the gateways on this computer, a magpie of its own each

  magpie station ls [--json]      every station: what it is doing, its pid, gateway and page
  magpie station start <name>     start it beside this terminal, as the page's Start does; one serving already is left as it is
  magpie station stop <name> [--force]
                                  stop it (SIGTERM); a protected one only with --force
  magpie station new <name> [--port N] [--web-port N|--no-web] [--note …] [--dir …] [--protected] [--env K=V …]
                                  a new station, as the page's + makes one: the next free ports from 3445 in tens unless told;
                                  --env puts a variable over its environment (K= takes one out), --protected keeps it from being stopped
  magpie station url <name>       its page's link, signed in with its key

stations.yml is the one MAGPIE_STATIONS names, else the one in this magpie's folder.
`

// stationWait is how long start and stop look for the station to be up or
// down before saying it isn't yet; a test shortens it.
var stationWait = 20 * time.Second

func stationCmd(args []string) error { return stationCmdTo(os.Stdout, args) }

func stationCmdTo(out io.Writer, args []string) error {
	if len(args) < 2 {
		fmt.Fprint(out, stationUsage)
		return nil
	}
	rest := args[2:]
	switch args[1] {
	case "ls", "list":
		return stationList(out, rest)
	case "start", "up":
		return stationStart(out, rest)
	case "stop", "down":
		return stationStop(out, rest)
	case "new", "add":
		return stationNew(out, rest)
	case "url":
		return stationURL(out, rest)
	}
	return fmt.Errorf("magpie station: no %q here\n\n%s", args[1], stationUsage)
}

// stationArgs is a subcommand's one name and its flags: valued takes a
// value (--port 3500 or --port=3500), bools none. Anything else is
// refused, so a flag mistyped is never read as the station's name or
// quietly left out.
func stationArgs(cmd string, args []string, valued, bools []string) (name string, vals map[string]string, on map[string]bool, err error) {
	vals, on = map[string]string{}, map[string]bool{}
	in := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case in(bools, a):
			on[a] = true
		case in(valued, a):
			if i+1 >= len(args) {
				return "", nil, nil, fmt.Errorf("magpie station %s: %s needs a value", cmd, a)
			}
			vals[a] = args[i+1]
			i++
		case strings.HasPrefix(a, "--") && strings.Contains(a, "=") && in(valued, a[:strings.Index(a, "=")]):
			vals[a[:strings.Index(a, "=")]] = a[strings.Index(a, "=")+1:]
		case strings.HasPrefix(a, "-"):
			return "", nil, nil, fmt.Errorf("magpie station %s: no flag %s here: nothing was done", cmd, a)
		case name != "":
			return "", nil, nil, fmt.Errorf("magpie station %s: one station at a time, not %s and %s", cmd, name, a)
		default:
			name = a
		}
	}
	return name, vals, on, nil
}

// stationsJSON is `magpie station ls --json`: where the stations are read
// from, what serves them, and each one's state. No key is in it: Full
// reads the page key where the station keeps it as it is run.
type stationsJSON struct {
	Path     string            `json:"path"`
	Root     string            `json:"root"`
	Runner   string            `json:"runner"`
	Stations []stations.Status `json:"stations"`
}

func stationList(out io.Writer, args []string) error {
	name, _, on, err := stationArgs("ls", args, nil, []string{"--json"})
	if err != nil {
		return err
	}
	if name != "" {
		return fmt.Errorf("magpie station ls takes no name: it lists them all")
	}
	f, err := stations.Load()
	if err != nil {
		return err
	}
	self, _ := os.Executable()
	runner := f.Runner
	if runner == "" {
		runner = self
	}
	list := stations.Report(context.Background(), f, self)
	if list == nil {
		list = []stations.Status{}
	}
	if on["--json"] {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(stationsJSON{Path: stations.Path(), Root: f.Root, Runner: runner, Stations: list})
	}
	if len(list) == 0 {
		fmt.Fprintf(out, "no stations in %s · magpie station new <name> makes one\n", stations.Path())
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tPID\tGATEWAY\tPAGE\tPROTECTED\tNOTE")
	for _, s := range list {
		pid, page, prot := "-", "-", ""
		if s.PID != 0 {
			pid = strconv.Itoa(s.PID)
		}
		if s.WebURL != "" {
			page = s.WebURL
		}
		if s.Protected {
			prot = "yes"
		}
		state := string(s.State)
		if s.Why != "" {
			state += " (" + s.Why + ")"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, state, pid, s.BaseURL, page, prot, s.Note)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%s\n", stations.Path())
	return nil
}

// waitState looks at s until done takes its state, for stationWait at
// most, and says whether it did.
func waitState(ctx context.Context, s stations.Station, done func(stations.State) bool) bool {
	for deadline := time.Now().Add(stationWait); ; time.Sleep(200 * time.Millisecond) {
		if st, _, _, _ := stations.Probe(ctx, s); done(st) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

func stationStart(out io.Writer, args []string) error {
	name, _, _, err := stationArgs("start", args, nil, nil)
	if err != nil {
		return err
	}
	f, s, err := stationNamed(name)
	if err != nil {
		return fmt.Errorf("magpie station start: %w", err)
	}
	ctx := context.Background()
	// one serving already is the station up, not a reason to fail: a
	// line that starts every station can be run again
	switch st, pid, _, why := stations.Probe(ctx, s); st {
	case stations.Up, stations.Listening:
		fmt.Fprintf(out, "%s is already served by pid %d at %s: left as it is\n", s.Name, pid, s.BaseURL())
		return nil
	case stations.Busy:
		return fmt.Errorf("magpie station start: %s: %s", s.Name, why)
	}
	self, _ := os.Executable()
	pid, err := stations.Start(ctx, f, s, self)
	if err != nil {
		return fmt.Errorf("magpie station start: %w", err)
	}
	log := stations.PlanOf(f, s, self).Log
	if !waitState(ctx, s, func(st stations.State) bool { return st == stations.Up }) {
		return fmt.Errorf("magpie station start: %s was started (pid %d) and doesn't answer at %s after %s · its log: %s", s.Name, pid, s.BaseURL(), stationWait, log)
	}
	fmt.Fprintf(out, "%s started, pid %d · gateway %s", s.Name, pid, s.BaseURL())
	if s.WebURL() != "" {
		fmt.Fprintf(out, " · page %s", s.WebURL())
	}
	fmt.Fprintf(out, "\n  log: %s\n", log)
	return nil
}

func stationStop(out io.Writer, args []string) error {
	name, _, on, err := stationArgs("stop", args, nil, []string{"--force"})
	if err != nil {
		return err
	}
	_, s, err := stationNamed(name)
	if err != nil {
		return fmt.Errorf("magpie station stop: %w", err)
	}
	ctx := context.Background()
	if st, _, _, _ := stations.Probe(ctx, s); st == stations.Down {
		fmt.Fprintf(out, "%s isn't running\n", s.Name)
		return nil
	}
	if s.Protected && !on["--force"] {
		return fmt.Errorf("magpie station stop: %s is protected: nothing was stopped · --force stops it all the same", s.Name)
	}
	if err := stations.StopForce(ctx, s); err != nil {
		return fmt.Errorf("magpie station stop: %w", err)
	}
	if !waitState(ctx, s, func(st stations.State) bool { return st == stations.Down }) {
		return fmt.Errorf("magpie station stop: %s was asked to stop and still holds %s after %s: it finishes the requests it has first", s.Name, s.Addr(), stationWait)
	}
	fmt.Fprintf(out, "%s stopped\n", s.Name)
	return nil
}

// envFlags takes every --env K=V (or --env=K=V) out of args: the station's
// own variables over NewEnv's, a K= taking the variable out, as a line of
// stations.yml's env does. A variable said twice is refused, so which one
// was meant is never guessed.
func envFlags(args []string) (rest []string, env map[string]string, err error) {
	env = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		var kv string
		switch {
		case a == "--env":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("magpie station new: --env needs a value")
			}
			kv = args[i+1]
			i++
		case strings.HasPrefix(a, "--env="):
			kv = a[len("--env="):]
		default:
			rest = append(rest, a)
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, nil, fmt.Errorf("magpie station new: --env is NAME=value (or NAME= to take it out), not %q", kv)
		}
		if _, twice := env[k]; twice {
			return nil, nil, fmt.Errorf("magpie station new: --env says %s twice", k)
		}
		env[k] = v
	}
	return rest, env, nil
}

func stationNew(out io.Writer, args []string) error {
	args, env, err := envFlags(args)
	if err != nil {
		return err
	}
	name, vals, on, err := stationArgs("new", args, []string{"--port", "--web-port", "--note", "--dir"}, []string{"--no-web", "--protected"})
	if err != nil {
		return err
	}
	if err := stations.CheckName(name); err != nil {
		return fmt.Errorf("magpie station new: %w", err)
	}
	port := func(flag string) (int, bool, error) {
		v, there := vals[flag]
		if !there {
			return 0, false, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, true, fmt.Errorf("magpie station new: %s is a port, not %q", flag, v)
		}
		return n, true, nil
	}
	p, gotPort, err := port("--port")
	if err != nil {
		return err
	}
	w, gotWeb, err := port("--web-port")
	if err != nil {
		return err
	}
	if gotWeb && on["--no-web"] {
		return fmt.Errorf("magpie station new: --web-port and --no-web say two things")
	}
	if !gotPort {
		f, err := stations.Load()
		if err != nil {
			return err
		}
		// as the page's + offers: the next pair free from FirstPort, in tens
		if p = stations.NextPort(f.Stations, stations.FirstPort, 10); p == 0 {
			return fmt.Errorf("magpie station new: no two free ports in a row from %d", stations.FirstPort)
		}
	}
	switch {
	case on["--no-web"]:
		w = 0
	case !gotWeb:
		w = p + 1
	}
	s := stations.Station{Name: name, Port: p, WebPort: w, Note: vals["--note"], Dir: vals["--dir"],
		Protected: on["--protected"], Env: stations.NewEnv()}
	for k, v := range env {
		s.Env[k] = v
	}
	f, err := stations.Add(s)
	if err != nil {
		return fmt.Errorf("magpie station new: %w", err)
	}
	fmt.Fprintf(out, "%s: gateway %s", s.Name, s.BaseURL())
	if s.WebURL() != "" {
		fmt.Fprintf(out, " · page %s", s.WebURL())
	}
	fmt.Fprintf(out, "\n  folder: %s\n  in %s\n  start it: magpie station start %s\n", s.Folder(f.Root), stations.Path(), s.Name)
	return nil
}

func stationURL(out io.Writer, args []string) error {
	name, _, _, err := stationArgs("url", args, nil, nil)
	if err != nil {
		return err
	}
	f, s, err := stationNamed(name)
	if err != nil {
		return fmt.Errorf("magpie station url: %w", err)
	}
	u, err := stations.PageURL(f, s)
	if err != nil {
		return fmt.Errorf("magpie station url: %w", err)
	}
	fmt.Fprintln(out, u)
	return nil
}
