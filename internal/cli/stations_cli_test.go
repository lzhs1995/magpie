package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/stations"
)

// station runs `magpie station <args…>` and gives what it printed.
func station(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := stationCmdTo(&out, append([]string{"station"}, args...))
	return out.String(), err
}

// writeRunner is a stand-in magpie in dir that writes down its arguments
// and environment and exits: a station it serves is never up.
func writeRunner(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in magpie is a shell script")
	}
	p := filepath.Join(dir, "magpie")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + stations.Quote(filepath.Join(dir, "args")) +
		"\nenv > " + stations.Quote(filepath.Join(dir, "env")) + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// `ls --json` is what a launcher reads instead of stations.yml: the file,
// the runner and each station's state and lines. No key is in it, though
// each station here has one.
func TestStationListJSONHasNoKey(t *testing.T) {
	root := t.TempDir()
	cred := filepath.Join(root, "CREDENTIALS.json")
	if err := os.WriteFile(cred, []byte(`{"a":{"web_password":"page-key-in-a-file"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, w := freeStationPorts(t)
	runner := filepath.Join(root, "official", "magpie")
	stationsAt(t, stations.File{Root: root, Runner: runner, Stations: []stations.Station{
		{Name: "a", Port: p, WebPort: w, Protected: true, KeyFile: cred, KeyPath: "a.web_password",
			Env: map[string]string{"NO_PROXY": "127.0.0.1"}},
		{Name: "b", Port: p + 10, WebPort: p + 11},
	}})
	// b's own key, given it as the page gives a station one
	if err := stations.MkdirFolder(filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	if _, err := stations.PageKey(stations.File{Root: root}, stations.Station{Name: "b", Port: p + 10, WebPort: p + 11}); err != nil {
		t.Fatal(err)
	}
	ownKey, _ := stations.Key(filepath.Join(root, "b"))
	out, err := station(t, "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"page-key-in-a-file", ownKey} {
		if k == "" || strings.Contains(out, k) {
			t.Fatalf("a key (%q) is in ls --json:\n%s", k, out)
		}
	}
	var got stationsJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != stations.Path() || got.Root != root || got.Runner != runner || len(got.Stations) != 2 {
		t.Fatalf("path %s root %s runner %s, %d stations", got.Path, got.Root, got.Runner, len(got.Stations))
	}
	a := got.Stations[0]
	if a.Name != "a" || a.State != stations.Down || !a.Protected || a.BaseURL != "http://127.0.0.1:"+strconv.Itoa(p) ||
		a.Env["NO_PROXY"] != "127.0.0.1" || a.Folder != filepath.Join(root, "a") || !strings.Contains(a.Full, runner) {
		t.Errorf("a: %+v", a)
	}
	// and the table says the same, with the file it read
	table, err := station(t, "ls")
	if err != nil || !strings.Contains(table, "a ") || !strings.Contains(table, "down") || !strings.Contains(table, stations.Path()) {
		t.Errorf("ls: %v\n%s", err, table)
	}
}

// start runs the station's Plan, as the page's Start does — the runner,
// its arguments, its variables — and says so when the station doesn't
// come up rather than reporting it started; a port something else holds
// is refused before anything runs.
func TestStationStartRunsThePlan(t *testing.T) {
	was := stationWait
	stationWait = 300 * time.Millisecond
	t.Cleanup(func() { stationWait = was })
	root, out := t.TempDir(), t.TempDir()
	runner := writeRunner(t, out)
	p, w := freeStationPorts(t)
	stationsAt(t, stations.File{Root: root, Runner: runner,
		Env:      map[string]string{"HTTPS_PROXY": ""},
		Stations: []stations.Station{{Name: "s", Port: p, WebPort: w, Env: map[string]string{"NO_PROXY": "127.0.0.1"}}}})
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:1")
	_, err := station(t, "start", "s")
	// the stand-in exits at once: the station never answers
	if err == nil || !strings.Contains(err.Error(), "doesn't answer") || !strings.Contains(err.Error(), "serve.log") {
		t.Fatalf("%v, want it said the station doesn't answer, and where its log is", err)
	}
	var args, env []byte
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if env, _ = os.ReadFile(filepath.Join(out, "env")); len(env) > 0 {
			break
		}
	}
	args, _ = os.ReadFile(filepath.Join(out, "args"))
	if want := "serve\n--web=127.0.0.1:" + strconv.Itoa(w) + "\n"; string(args) != want {
		t.Errorf("the runner was started with %q, want %q", args, want)
	}
	got := map[string]string{}
	for _, l := range strings.Split(string(env), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			got[k] = v
		}
	}
	for k, v := range map[string]string{"XDG_CONFIG_HOME": filepath.Join(root, "s"), "MAGPIE_ADDR": "127.0.0.1:" + strconv.Itoa(p), "NO_PROXY": "127.0.0.1"} {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{"HTTPS_PROXY", stations.StationsVar} {
		if v, there := got[k]; there {
			t.Errorf("%s = %q reached the station", k, v)
		}
	}

	// a port held by something that isn't a magpie: nothing is run
	if err := os.Remove(filepath.Join(out, "args")); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := station(t, "start", "s"); err == nil {
		t.Fatal("started on a port another program holds")
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(out, "args")); !os.IsNotExist(err) {
		t.Errorf("the runner was started on a held port (%v)", err)
	}
}

// stop leaves a station that isn't running as it is and says so; a
// protected one is refused without --force, and a port another program
// holds is never ended, --force or not.
func TestStationStopRefusesProtectedAndForeign(t *testing.T) {
	p, w := freeStationPorts(t)
	stationsAt(t, stations.File{Root: t.TempDir(), Stations: []stations.Station{
		{Name: "kept", Port: p, WebPort: w, Protected: true},
	}})
	if out, err := station(t, "stop", "kept"); err != nil || !strings.Contains(out, "isn't running") {
		t.Errorf("down: %q, %v", out, err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := station(t, "stop", "kept"); err == nil || !strings.Contains(err.Error(), "protected") || !strings.Contains(err.Error(), "--force") {
		t.Errorf("protected: %v, want refused with --force named", err)
	}
	// the test holds the port: it isn't a magpie, so --force doesn't end it
	if _, err := station(t, "stop", "kept", "--force"); err == nil {
		t.Error("--force stopped a port held by something that isn't a magpie")
	}
	if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(p)); err != nil {
		t.Errorf("the holder's port went away: %v", err)
	} else {
		c.Close()
	}
}

// new makes the station the page's + makes: the next free pair of ports
// from FirstPort in tens, its page on the one after, its own folder and
// page key, and no agent's sign-in read. What it is told wins, and two
// flags that say opposite things are refused with nothing written.
func TestStationNewMakesWhatThePageMakes(t *testing.T) {
	root := t.TempDir()
	stationsAt(t, stations.File{Root: root})
	out, err := station(t, "new", "fresh", "--note", "a note")
	if err != nil {
		t.Fatal(err)
	}
	f, err := stations.Load()
	if err != nil || len(f.Stations) != 1 {
		t.Fatalf("%+v, %v", f, err)
	}
	s := f.Stations[0]
	want := stations.NextPort(nil, stations.FirstPort, 10)
	if s.Name != "fresh" || s.Port != want || s.WebPort != want+1 || s.Note != "a note" || s.Env["MAGPIE_NO_ACCOUNTS"] != "1" {
		t.Errorf("made %+v, want ports %d/%d and MAGPIE_NO_ACCOUNTS=1", s, want, want+1)
	}
	if key, err := stations.Key(filepath.Join(root, "fresh")); err != nil || key == "" {
		t.Errorf("no page key of its own: %q, %v", key, err)
	}
	if !strings.Contains(out, "magpie station start fresh") || !strings.Contains(out, s.BaseURL()) {
		t.Errorf("said %q", out)
	}

	if _, err := station(t, "new", "told", "--port=3800", "--no-web"); err != nil {
		t.Fatal(err)
	}
	f, _ = stations.Load()
	if s, _ := f.Find("told"); s.Port != 3800 || s.WebPort != 0 {
		t.Errorf("told: %+v", s)
	}
	before, _ := os.ReadFile(stations.Path())
	for _, args := range [][]string{
		{"new", "x", "--web-port", "3901", "--no-web"},
		{"new", "x", "--port", "port"},
		{"new", "x", "--prot", "3900"},
		{"new", "fresh"},
		{"new", "x", "y"},
		{"new"},
	} {
		if _, err := station(t, args...); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
	if after, _ := os.ReadFile(stations.Path()); !bytes.Equal(before, after) {
		t.Error("a refused new wrote stations.yml")
	}
}

// new takes the station's own variables and whether it is protected, as a
// launcher's template gives them (contrib/lanes/mg new): --env over
// NewEnv's, K= kept in stations.yml as the variable taken out. A variable
// said twice, one without =, or one the station sets itself is refused
// with nothing written.
func TestStationNewTakesEnvAndProtected(t *testing.T) {
	stationsAt(t, stations.File{Root: t.TempDir()})
	if _, err := station(t, "new", "tpl", "--port", "3800", "--protected",
		"--env", "HTTPS_PROXY=", "--env=NO_PROXY=127.0.0.1,localhost,::1", "--env", "MAGPIE_NO_ACCOUNTS=1"); err != nil {
		t.Fatal(err)
	}
	f, _ := stations.Load()
	s, _ := f.Find("tpl")
	want := map[string]string{"HTTPS_PROXY": "", "NO_PROXY": "127.0.0.1,localhost,::1", "MAGPIE_NO_ACCOUNTS": "1"}
	if !s.Protected || !reflect.DeepEqual(s.Env, want) {
		t.Errorf("made %+v, want protected and env %v", s, want)
	}
	before, _ := os.ReadFile(stations.Path())
	for _, args := range [][]string{
		{"new", "x", "--env", "A=1", "--env", "A=2"},
		{"new", "x", "--env", "NOEQUALS"},
		{"new", "x", "--env", "=v"},
		{"new", "x", "--env"},
		{"new", "x", "--env", "MAGPIE_ADDR=127.0.0.1:1"},
		{"new", "x", "--env", "1BAD=v"},
	} {
		if _, err := station(t, args...); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
	if after, _ := os.ReadFile(stations.Path()); !bytes.Equal(before, after) {
		t.Error("a refused new wrote stations.yml")
	}
}

// url is the page signed in: the key read where the station keeps it.
func TestStationURL(t *testing.T) {
	root := t.TempDir()
	cred := filepath.Join(root, "CREDENTIALS.json")
	if err := os.WriteFile(cred, []byte(`{"web_password":"k1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stationsAt(t, stations.File{Root: root, Stations: []stations.Station{
		{Name: "paged", Port: 3600, WebPort: 3601, KeyFile: cred},
		{Name: "bare", Port: 3610},
	}})
	if out, err := station(t, "url", "paged"); err != nil || out != "http://127.0.0.1:3601/?k=k1\n" {
		t.Errorf("paged: %q, %v", out, err)
	}
	if _, err := station(t, "url", "bare"); err == nil || !strings.Contains(err.Error(), "no page") {
		t.Errorf("bare: %v", err)
	}
	if _, err := station(t, "url", "nobody"); err == nil || !strings.Contains(err.Error(), "there is paged, bare") {
		t.Errorf("nobody: %v, want the stations there are", err)
	}
}

// `magpie station …` reads stations.yml and starts or stops what it names,
// and does nothing else under the home: none of the moves and
// migrations every other command makes first, no folder of magpie's own
// made, no agent's file touched. A launcher runs `ls --json` often.
func TestStationLeavesTheHomeAsItWas(t *testing.T) {
	root := helpHome(t)
	elsewhere := t.TempDir()
	t.Setenv(stations.StationsVar, filepath.Join(elsewhere, "stations.yml"))
	if err := stations.Save(stations.File{Root: elsewhere, Stations: []stations.Station{{Name: "a", Port: 3720}}}); err != nil {
		t.Fatal(err)
	}
	before := tree(t, root)
	// it is left out of run's switch, so the help test there doesn't see it
	for _, args := range [][]string{{"station", "--help"}, {"stations", "start", "a", "-h"}, {"station", "help"}} {
		if out, err := runOut(t, args...); err != nil || !strings.Contains(out, "magpie station ls") {
			t.Errorf("magpie %s: %q, %v", strings.Join(args, " "), out, err)
		}
	}
	for _, args := range [][]string{{"station", "ls", "--json"}, {"stations", "ls"}, {"station", "url", "a"}, {"station"}} {
		if _, err := runOut(t, args...); err != nil && args[1] != "url" {
			t.Errorf("magpie %s: %v", strings.Join(args, " "), err)
		}
		if got := tree(t, root); !reflect.DeepEqual(got, before) {
			t.Fatalf("magpie %s wrote under the home:\nbefore %v\nafter  %v", strings.Join(args, " "), before, got)
		}
	}
}

// What isn't a subcommand or a flag of it is refused, never taken as a
// name: a flag mistyped is named as the flag it is.
func TestStationRefusesWhatItDoesntKnow(t *testing.T) {
	stationsAt(t, stations.File{Root: t.TempDir(), Stations: []stations.Station{{Name: "a", Port: 3700}}})
	for _, args := range [][]string{
		{"restart", "a"}, {"stop", "a", "b"}, {"ls", "a"},
	} {
		if _, err := station(t, args...); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
	for _, c := range []struct {
		flag string
		args []string
	}{
		{"--forcee", []string{"start", "a", "--forcee"}},
		{"--js", []string{"ls", "--js"}},
		{"-f", []string{"stop", "-f", "a"}},
		{"--prot", []string{"new", "--prot", "3900", "b"}},
	} {
		if _, err := station(t, c.args...); err == nil || !strings.Contains(err.Error(), "no flag "+c.flag) {
			t.Errorf("%v: %v, want %s named as no flag", c.args, err, c.flag)
		}
	}
	if out, err := station(t); err != nil || !strings.Contains(out, "magpie station ls") {
		t.Errorf("no subcommand: %q, %v", out, err)
	}
}
