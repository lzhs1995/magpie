package stations

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/stations"
)

// post is one of the routes, with body.
func post(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	rw := httptest.NewRecorder()
	h(rw, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	return rw
}

// freePort is a port with the one after it free now.
func freePort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 50; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		if p >= 1024 && p < 65535 && stations.Free(p) && stations.Free(p+1) {
			return p
		}
	}
	t.Fatal("no two free ports in a row")
	return 0
}

// Edit keeps what its form doesn't carry. A station set up by hand that
// lost its KeyFile was given a new key of its own beside the one its
// running magpie serves, and one whose folder isn't named after it moved
// to an empty one; its variables went with them.
func TestEditKeepsWhatTheFormDoesntCarry(t *testing.T) {
	root := t.TempDir()
	t.Setenv(stations.StationsVar, filepath.Join(t.TempDir(), "stations.yml"))
	cred := filepath.Join(root, "CREDENTIALS.json")
	if err := os.WriteFile(cred, []byte(`{"linxi":{"web_password":"hand-made"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "not-named-after-it")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	was := stations.Station{Name: "linxi", Dir: dir, Port: port, WebPort: port + 1, Protected: true,
		KeyFile: cred, KeyPath: "linxi.web_password",
		Env: map[string]string{"NO_PROXY": "127.0.0.1", "HTTPS_PROXY": ""}}
	if err := stations.Save(stations.File{Root: root, Stations: []stations.Station{was}}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ name, body string }{
		{"linxi", `{"was":"linxi","name":"linxi","port":` + strconv.Itoa(port) + `,"webPort":` + strconv.Itoa(port+1) + `,"note":"a note"}`},
		{"linxi-2", `{"was":"linxi","name":"linxi-2","port":` + strconv.Itoa(port) + `,"webPort":` + strconv.Itoa(port+1) + `,"note":"renamed"}`},
	} {
		if rw := post(t, edit, c.body); rw.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.name, rw.Code, rw.Body)
		}
		f, err := stations.Load()
		if err != nil {
			t.Fatal(err)
		}
		s, there := f.Find(c.name)
		if !there {
			t.Fatalf("%s isn't there: %+v", c.name, f.Stations)
		}
		if s.KeyFile != was.KeyFile || s.KeyPath != was.KeyPath || !reflect.DeepEqual(s.Env, was.Env) ||
			s.Folder(root) != dir || !s.Protected {
			t.Errorf("%s: %+v, want its KeyFile, KeyPath, Env, folder and protection kept", c.name, s)
		}
		if k, err := stations.KeyOf(s, root); err != nil || k != "hand-made" {
			t.Errorf("%s: key %q, %v, want the one its magpie serves", c.name, k, err)
		}
		for _, d := range []string{dir, filepath.Join(root, c.name)} {
			if _, err := os.Stat(filepath.Join(d, stations.SecretsFile)); !os.IsNotExist(err) {
				t.Errorf("%s: a %s was written in %s (%v)", c.name, stations.SecretsFile, d, err)
			}
		}
	}
}

// A new station's magpie reads no agent's sign-in on this computer: it
// serves the keys put in it alone.
func TestNewStationReadsNoAgentsSignIn(t *testing.T) {
	root := t.TempDir()
	t.Setenv(stations.StationsVar, filepath.Join(t.TempDir(), "stations.yml"))
	if err := stations.Save(stations.File{Root: root}); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	if rw := post(t, create, `{"name":"fresh","port":`+strconv.Itoa(port)+`,"webPort":`+strconv.Itoa(port+1)+`}`); rw.Code != http.StatusOK {
		t.Fatalf("%d %s", rw.Code, rw.Body)
	}
	f, err := stations.Load()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := f.Find("fresh")
	if s.Env["MAGPIE_NO_ACCOUNTS"] != "1" {
		t.Errorf("env %v, want MAGPIE_NO_ACCOUNTS=1", s.Env)
	}
	if p := stations.PlanOf(f, s, "/m"); p.Set["MAGPIE_NO_ACCOUNTS"] != "1" {
		t.Errorf("plan sets %v, want MAGPIE_NO_ACCOUNTS=1", p.Set)
	}
}
