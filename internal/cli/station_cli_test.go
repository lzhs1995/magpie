package cli

import (
	"strings"
	"testing"
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

// envWith replaces a variable rather than repeating it: which of two
// wins is not left to the program being exec'd.
func TestEnvWithReplacesRatherThanRepeats(t *testing.T) {
	got := envWith(
		[]string{"PATH=/bin", "XDG_CONFIG_HOME=/old", "HOME=/h"},
		map[string]string{"XDG_CONFIG_HOME": "/new", "MAGPIE_ADDR": "127.0.0.1:3500"},
	)
	var xdg []string
	for _, kv := range got {
		if strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			xdg = append(xdg, kv)
		}
	}
	if len(xdg) != 1 || xdg[0] != "XDG_CONFIG_HOME=/new" {
		t.Errorf("XDG_CONFIG_HOME: %v, want exactly one, the new one", xdg)
	}
	// positive control: what wasn't set over is still there, once
	for _, want := range []string{"PATH=/bin", "HOME=/h", "MAGPIE_ADDR=127.0.0.1:3500"} {
		n := 0
		for _, kv := range got {
			if kv == want {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s appears %d times, want 1 · %v", want, n, got)
		}
	}
}
