package cli

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/stations"
)

// --station is taken out of the line and the rest is left for serve, so a
// flag of the reader's own still reaches it.
func TestStationFlagSplitsTheLine(t *testing.T) {
	for _, c := range []struct {
		args    []string
		rest    []string
		station string
	}{
		{[]string{"--station=work"}, nil, "work"},
		{[]string{"--station=work", "--web=127.0.0.1:3500"}, []string{"--web=127.0.0.1:3500"}, "work"},
		{[]string{"--web", "--station=a.b-c_1"}, []string{"--web"}, "a.b-c_1"},
		// no --station at all: the line is untouched and serve behaves as before
		{[]string{"--web=127.0.0.1:3500"}, []string{"--web=127.0.0.1:3500"}, ""},
		{nil, nil, ""},
	} {
		rest, station, err := stationFlag(c.args)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if station != c.station {
			t.Errorf("%v: station %q, want %q", c.args, station, c.station)
		}
		if strings.Join(rest, " ") != strings.Join(c.rest, " ") {
			t.Errorf("%v: rest %v, want %v", c.args, rest, c.rest)
		}
	}
}

// A line that can't mean one station is refused, rather than serving the
// wrong one or silently dropping a name.
func TestStationFlagRefusesWhatCantMeanOneStation(t *testing.T) {
	for _, args := range [][]string{
		{"--station"},                  // no name
		{"--station="},                 // empty name
		{"--station=a", "--station=b"}, // two stations
	} {
		if _, _, err := stationFlag(args); err == nil {
			t.Errorf("%v: taken, want refused", args)
		}
	}
}

// hasWebFlag is what lets a --web of the reader's own win over the
// station's page port. Both spellings count; a station name that merely
// contains "web" does not.
func TestHasWebFlag(t *testing.T) {
	if !hasWebFlag([]string{"--web"}) || !hasWebFlag([]string{"--web=127.0.0.1:3500"}) {
		t.Error("a --web on the line wasn't seen")
	}
	if hasWebFlag([]string{"--station=webby"}) || hasWebFlag(nil) {
		t.Error("something that isn't --web was taken for it")
	}
}

// stationsAt writes f as the stations.yml MAGPIE_STATIONS names, for one
// test.
func stationsAt(t *testing.T, f stations.File) {
	t.Helper()
	t.Setenv(stations.StationsVar, filepath.Join(t.TempDir(), "stations.yml"))
	if err := stations.Save(f); err != nil {
		t.Fatal(err)
	}
}

// freeStationPorts is a gateway port and the page port after it, both
// free now.
func freeStationPorts(t *testing.T) (int, int) {
	t.Helper()
	for i := 0; i < 50; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		if p >= 1024 && p < 65535 && stations.Free(p) && stations.Free(p+1) {
			return p, p + 1
		}
	}
	t.Fatal("no two free ports in a row")
	return 0, 0
}

// execd is what runStation handed to exec, the test standing in for it.
type execd struct {
	called    bool
	argv0     string
	args, env []string
}

func stubExec(t *testing.T) *execd {
	t.Helper()
	got := &execd{}
	was := stationExec
	stationExec = func(argv0 string, argv, envv []string) error {
		*got = execd{true, argv0, argv, envv}
		return nil
	}
	t.Cleanup(func() { stationExec = was })
	return got
}

// `magpie serve --station` runs the station's Plan — the magpie the file
// names as runner, the station's page with the key from the KeyFile it
// names, its variables — as the page's Start does. A station set up by
// hand keeps its key in a CREDENTIALS.json of its own, and the line read
// only station-secrets.json, so it refused to start one.
func TestServeStationRunsItsPlan(t *testing.T) {
	root := t.TempDir()
	cred := filepath.Join(root, "CREDENTIALS.json")
	if err := os.WriteFile(cred, []byte(`{"linxi":{"web_password":"hand-made"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(root, "official", "magpie")
	port, web := freeStationPorts(t)
	stationsAt(t, stations.File{Root: root, Runner: runner,
		Env: map[string]string{"HTTPS_PROXY": ""},
		Stations: []stations.Station{{Name: "linxi", Port: port, WebPort: web,
			KeyFile: cred, KeyPath: "linxi.web_password", Protected: true}}})
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:1")
	got := stubExec(t)

	if err := runStation("linxi", nil); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, "linxi")
	if got.argv0 != runner || strings.Join(got.args, " ") != runner+" serve --web=127.0.0.1:"+strconv.Itoa(web) {
		t.Errorf("exec %s %q, want the runner serving the station's page", got.argv0, got.args)
	}
	env := map[string][]string{}
	for _, kv := range got.env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = append(env[k], v)
	}
	for k, want := range map[string]string{
		"MAGPIE_WEB_KEY": "hand-made", "XDG_CONFIG_HOME": folder,
		"MAGPIE_ADDR": "127.0.0.1:" + strconv.Itoa(port), "XDG_CACHE_HOME": folder + "-cache",
	} {
		if len(env[k]) != 1 || env[k][0] != want {
			t.Errorf("%s = %q, want exactly %q", k, env[k], want)
		}
	}
	for _, k := range []string{"HTTPS_PROXY", stations.StationsVar} {
		if v, there := env[k]; there {
			t.Errorf("%s = %q reached the station", k, v)
		}
	}
	if _, err := os.Stat(filepath.Join(folder, stations.SecretsFile)); !os.IsNotExist(err) {
		t.Errorf("a station with its own KeyFile was given a %s (%v)", stations.SecretsFile, err)
	}

	// a --web of the reader's own wins, and the station's key isn't put in
	if err := runStation("linxi", []string{"--web=127.0.0.1:9999"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.args, " ") != runner+" serve --web=127.0.0.1:9999" {
		t.Errorf("exec %q, want the reader's --web alone", got.args)
	}
	for _, kv := range got.env {
		if strings.HasPrefix(kv, "MAGPIE_WEB_KEY=hand-made") {
			t.Error("the station's key went with the reader's own --web")
		}
	}
}

// A station whose port is already held isn't started a second time: the
// line says what holds it rather than exec'ing a magpie that would only
// fail on the port, or two serving one folder.
func TestServeStationRefusesAHeldPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	stationsAt(t, stations.File{Root: t.TempDir(),
		Stations: []stations.Station{{Name: "held", Port: port}}})
	got := stubExec(t)
	err = runStation("held", nil)
	if err == nil || got.called {
		t.Fatalf("err %v, exec'd %v: want refused before exec", err, got.called)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(port)) {
		t.Errorf("%v doesn't say which port", err)
	}
}

// A name that isn't there says which stations.yml was read: the one a
// window keeps may be another (MAGPIE_STATIONS).
func TestServeStationSaysWhichFileItRead(t *testing.T) {
	stationsAt(t, stations.File{Root: t.TempDir(),
		Stations: []stations.Station{{Name: "a", Port: 3500}}})
	stubExec(t)
	err := runStation("linxi", nil)
	if err == nil || !strings.Contains(err.Error(), stations.Path()) || !strings.Contains(err.Error(), "there is a") {
		t.Errorf("%v, want the file read and the stations in it", err)
	}
}
