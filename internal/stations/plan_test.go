package stations

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// freePorts is a gateway port and the page port after it, both free now.
func freePorts(t *testing.T) (int, int) {
	t.Helper()
	for i := 0; i < 50; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		if p >= 1024 && p < 65535 && Free(p) && Free(p+1) {
			return p, p + 1
		}
	}
	t.Fatal("no two free ports in a row")
	return 0, 0
}

// envMap is a KEY=VALUE list as a map.
func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// A station's own variables are put over the file's, "" takes one out,
// and what makes the station the one it is — its folder, its address and
// its key — is the Plan's whatever either says.
func TestPlanOfPutsTheStationsOwnOverTheFiles(t *testing.T) {
	root := t.TempDir()
	runner := filepath.Join(root, "official", "magpie")
	f := File{
		Root:   root,
		Runner: runner,
		Env: map[string]string{
			"NO_PROXY":           "127.0.0.1,localhost,::1",
			"HTTPS_PROXY":        "",
			"MAGPIE_NO_ACCOUNTS": "1",
		},
	}
	s := Station{Name: "linxi", Port: 3445, WebPort: 3446,
		Env: map[string]string{"NO_PROXY": "k40.example,127.0.0.1", "MAGPIE_NO_ACCOUNTS": ""}}
	p := PlanOf(f, s, "/this/magpie")

	folder := filepath.Join(root, "linxi")
	if p.Exe != runner {
		t.Errorf("exe %s, want the file's runner %s", p.Exe, runner)
	}
	if strings.Join(p.Args, " ") != "serve --web=127.0.0.1:3446" || !p.Key {
		t.Errorf("args %q key %v, want serve with the station's page and its key", p.Args, p.Key)
	}
	if p.Dir != folder || p.Log != filepath.Join(folder, "serve.log") {
		t.Errorf("dir %s log %s, want the station's folder and serve.log in it", p.Dir, p.Log)
	}
	for k, want := range map[string]string{
		"XDG_CONFIG_HOME": folder,
		"MAGPIE_ADDR":     "127.0.0.1:3445",
		"XDG_CACHE_HOME":  folder + "-cache",
		"NO_PROXY":        "k40.example,127.0.0.1", // the station's own wins
	} {
		if p.Set[k] != want {
			t.Errorf("%s = %q, want %q", k, p.Set[k], want)
		}
	}
	if strings.Join(p.Unset, " ") != "HTTPS_PROXY MAGPIE_NO_ACCOUNTS "+StationsVar {
		t.Errorf("unset %q, want the file's \"\", the station's \"\" and %s", p.Unset, StationsVar)
	}
	for _, k := range p.Unset {
		if _, both := p.Set[k]; both {
			t.Errorf("%s is both set and unset", k)
		}
	}

	// no runner: this magpie serves it; no page: no key and no --web
	p = PlanOf(File{Root: root}, Station{Name: "quiet", Port: 3500}, "/this/magpie")
	if p.Exe != "/this/magpie" || strings.Join(p.Args, " ") != "serve" || p.Key {
		t.Errorf("exe %s args %q key %v, want this magpie, serve alone, no key", p.Exe, p.Args, p.Key)
	}
}

// Environ replaces what the plan sets rather than repeating it, takes out
// what it unsets, and leaves the rest of the environment as it was.
func TestEnvironReplacesRatherThanRepeats(t *testing.T) {
	root := t.TempDir()
	p := PlanOf(File{Root: root, Env: map[string]string{"HTTPS_PROXY": ""}},
		Station{Name: "a", Port: 3500, WebPort: 3501}, "/m")
	got := p.Environ([]string{
		"PATH=/bin", "HOME=/h", "XDG_CONFIG_HOME=/old", "HTTPS_PROXY=http://proxy:1",
		"MAGPIE_WEB_KEY=someone-elses", StationsVar + "=/the/window/stations.yml",
	}, "the-key")
	count := map[string]int{}
	for _, kv := range got {
		k, _, _ := strings.Cut(kv, "=")
		count[k]++
	}
	for k, n := range count {
		if n != 1 {
			t.Errorf("%s appears %d times · %v", k, n, got)
		}
	}
	m := envMap(got)
	if m["XDG_CONFIG_HOME"] != filepath.Join(root, "a") || m["MAGPIE_WEB_KEY"] != "the-key" {
		t.Errorf("XDG_CONFIG_HOME %q MAGPIE_WEB_KEY %q, want the station's", m["XDG_CONFIG_HOME"], m["MAGPIE_WEB_KEY"])
	}
	for _, k := range []string{"HTTPS_PROXY", StationsVar} {
		if _, there := m[k]; there {
			t.Errorf("%s is still there", k)
		}
	}
	// positive control: what the plan doesn't name is still there
	if m["PATH"] != "/bin" || m["HOME"] != "/h" {
		t.Errorf("PATH %q HOME %q, want them untouched", m["PATH"], m["HOME"])
	}
	// a station with no page leaves a MAGPIE_WEB_KEY of the shell's alone
	q := PlanOf(File{Root: root}, Station{Name: "b", Port: 3510}, "/m")
	if envMap(q.Environ([]string{"MAGPIE_WEB_KEY=mine"}, ""))["MAGPIE_WEB_KEY"] != "mine" {
		t.Error("a station with no page took the shell's MAGPIE_WEB_KEY")
	}
}

// fakeRunner is a magpie that writes down what it was started with, in
// dir: its arguments one a line, then its environment, then says one
// thing on each of its outputs.
func fakeRunner(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in magpie is a shell script")
	}
	p := filepath.Join(dir, "magpie")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + Quote(filepath.Join(dir, "args")) +
		"\nenv > " + Quote(filepath.Join(dir, "env")) +
		"\necho started\necho oops >&2\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// The line written out for a reader who wants no stations.yml in it
// starts the magpie the page's Start does, with the same arguments and
// variables: one Plan makes both. The key is read by the line from where
// the station keeps it, never written in it — a KeyFile's nested key and
// a station's own SecretsFile both.
func TestLineStartsWhatStartDoes(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	runner := fakeRunner(t, out)
	cred := filepath.Join(root, "CREDENTIALS.json")
	if err := os.WriteFile(cred, []byte(`{"linxi":{"web_password":"nested key $HOME \"q\" 'one'"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f := File{Root: root, Runner: runner,
		Env: map[string]string{"HTTPS_PROXY": "", "NO_PROXY": "127.0.0.1,localhost,::1"}}
	for _, s := range []Station{
		{Name: "linxi", Port: 3945, WebPort: 3946, KeyFile: cred, KeyPath: "linxi.web_password",
			Env: map[string]string{"NOTE": "two words & a $sign"}},
		{Name: "own", Port: 3955, WebPort: 3956},
	} {
		if err := MkdirFolder(s.Folder(root)); err != nil {
			t.Fatal(err)
		}
		key, err := PageKey(f, s)
		if err != nil || key == "" {
			t.Fatalf("%s: key %q, %v", s.Name, key, err)
		}
		p := PlanOf(f, s, "/unused")
		line := p.Line(keyRead(s, root))
		if strings.Contains(line, key) {
			t.Fatalf("%s: the key is on the line: %s", s.Name, line)
		}
		base := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
			"HTTPS_PROXY=http://proxy.invalid:1", StationsVar + "=/the/window/stations.yml"}
		cmd := exec.Command("/bin/sh", "-c", line)
		cmd.Env = base
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s\n%s", s.Name, err, line, b)
		}
		args, _ := os.ReadFile(filepath.Join(out, "args"))
		if string(args) != strings.Join(p.Args, "\n")+"\n" {
			t.Errorf("%s: started with %q, want %q", s.Name, args, p.Args)
		}
		envB, _ := os.ReadFile(filepath.Join(out, "env"))
		got := envMap(strings.Split(string(envB), "\n"))
		for k, v := range envMap(p.Environ(base, key)) {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q (what Start gives)", s.Name, k, got[k], v)
			}
		}
		for _, k := range p.Unset {
			if v, there := got[k]; there {
				t.Errorf("%s: %s = %q is still there", s.Name, k, v)
			}
		}
	}
}

// The page's Start runs the station's Plan, and what the station says on
// either output is added to serve.log in its folder, after what an
// earlier run left there.
func TestStartRunsThePlanAndLogs(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	runner := fakeRunner(t, out)
	port, web := freePorts(t)
	f := File{Root: root, Runner: runner, Env: map[string]string{"HTTPS_PROXY": ""}}
	s := Station{Name: "logged", Port: port, WebPort: web}
	folder := s.Folder(root)
	if err := MkdirFolder(folder); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "serve.log"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:1")
	t.Setenv(StationsVar, filepath.Join(t.TempDir(), "stations.yml"))
	if _, err := Start(context.Background(), f, s, "/unused"); err != nil {
		t.Fatal(err)
	}
	// "oops" is the stand-in's last word: once it is in the log, so are
	// its arguments and environment
	var log []byte
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		log, _ = os.ReadFile(filepath.Join(folder, "serve.log"))
		if strings.Contains(string(log), "oops") || time.Now().After(deadline) {
			break
		}
	}
	if string(log) != "before\nstarted\noops\n" && string(log) != "before\noops\nstarted\n" {
		t.Fatalf("serve.log is %q, want what was there and then both outputs", log)
	}
	args, _ := os.ReadFile(filepath.Join(out, "args"))
	if want := "serve\n--web=127.0.0.1:" + strconv.Itoa(web) + "\n"; string(args) != want {
		t.Errorf("started with %q, want %q", args, want)
	}
	envB, _ := os.ReadFile(filepath.Join(out, "env"))
	got := envMap(strings.Split(string(envB), "\n"))
	key, err := KeyOf(s, root)
	if err != nil || key == "" {
		t.Fatalf("key %q, %v", key, err)
	}
	for k, want := range map[string]string{
		"XDG_CONFIG_HOME": folder, "XDG_CACHE_HOME": folder + "-cache",
		"MAGPIE_ADDR": s.Addr(), "MAGPIE_WEB_KEY": key,
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	for _, k := range []string{"HTTPS_PROXY", StationsVar} {
		if v, there := got[k]; there {
			t.Errorf("%s = %q reached the station", k, v)
		}
	}
}

// A station that names its own KeyFile is started with the key there and
// is never given one of its own beside it; one that names none is given
// one once, and keeps it.
func TestPageKey(t *testing.T) {
	root := t.TempDir()
	cred := filepath.Join(root, "CREDENTIALS.json")
	if err := os.WriteFile(cred, []byte(`{"a":{"web_password":"hand-made"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f := File{Root: root}
	hand := Station{Name: "hand", Port: 3500, WebPort: 3501, KeyFile: cred, KeyPath: "a.web_password"}
	if err := MkdirFolder(hand.Folder(root)); err != nil {
		t.Fatal(err)
	}
	if k, err := PageKey(f, hand); err != nil || k != "hand-made" {
		t.Errorf("key %q, %v, want the one in its KeyFile", k, err)
	}
	wrong := hand
	wrong.KeyPath = "b.web_password"
	if _, err := PageKey(f, wrong); err == nil {
		t.Error("a KeyPath with nothing at it was taken")
	}
	if _, err := os.Stat(filepath.Join(hand.Folder(root), SecretsFile)); !os.IsNotExist(err) {
		t.Errorf("a station with a KeyFile was given a %s of its own (%v)", SecretsFile, err)
	}

	own := Station{Name: "own", Port: 3510, WebPort: 3511}
	if err := MkdirFolder(own.Folder(root)); err != nil {
		t.Fatal(err)
	}
	k1, err := PageKey(f, own)
	if err != nil || len(k1) != 32 {
		t.Fatalf("key %q, %v, want a new one", k1, err)
	}
	if k2, _ := PageKey(f, own); k2 != k1 {
		t.Errorf("a second start gave key %q, want the first, %q", k2, k1)
	}
	if k, err := PageKey(f, Station{Name: "nopage", Port: 3520}); k != "" || err != nil {
		t.Errorf("a station with no page: key %q, %v, want none", k, err)
	}
}

// MAGPIE_STATIONS names the stations.yml to read, so a terminal finds the
// stations a window with another XDG_CONFIG_HOME keeps; the line the page
// hands out names it only when a plain shell's magpie would read another.
func TestStationsVarNamesTheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(StationsVar, "")
	if want := filepath.Join(home, ".config", "magpie", "stations.yml"); Path() != want || LaunchPrefix() != "" {
		t.Errorf("plain: Path %s prefix %q, want %s and none", Path(), LaunchPrefix(), want)
	}

	// a window run with its own XDG_CONFIG_HOME: the line says where
	view := filepath.Join(home, "view")
	t.Setenv("XDG_CONFIG_HOME", view)
	want := filepath.Join(view, "magpie", "stations.yml")
	if Path() != want || LaunchPrefix() != StationsVar+"="+Quote(want)+" " {
		t.Errorf("another folder: Path %s prefix %q, want %s named", Path(), LaunchPrefix(), want)
	}
	// and the line, run with that file named, puts a station that names
	// no folder in the one the window does
	windows := DefaultRoot()
	t.Setenv(StationsVar, want)
	if DefaultRoot() != windows {
		t.Errorf("the line's root %s, the window's %s", DefaultRoot(), windows)
	}

	// named: that file, its stations' folders beside the folder it is in
	// when it names no root, as the window's are beside its own
	named := filepath.Join(home, "elsewhere", "magpie", "stations.yml")
	t.Setenv(StationsVar, named)
	if Path() != named || DefaultRoot() != filepath.Join(home, "elsewhere", "magpie-stations") {
		t.Errorf("named: Path %s root %s", Path(), DefaultRoot())
	}
	if err := Save(File{Stations: []Station{{Name: "a", Port: 3500}}}); err != nil {
		t.Fatal(err)
	}
	if f, err := Load(); err != nil || len(f.Stations) != 1 || f.Root != DefaultRoot() {
		t.Errorf("load: %+v, %v", f, err)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Errorf("the window's own stations.yml was written (%v)", err)
	}

	// a relative name is no name
	t.Setenv(StationsVar, "stations.yml")
	if Path() != want {
		t.Errorf("relative: Path %s, want the folder's own %s", Path(), want)
	}
}

// What would change which station a magpie is can't be put in its
// variables, and a runner is a path that means one file wherever it is
// read from.
func TestValidateRefusesWhatWouldChangeTheStation(t *testing.T) {
	root := t.TempDir()
	ok := File{Root: root, Runner: filepath.Join(root, "magpie"),
		Env:      map[string]string{"NO_PROXY": "127.0.0.1"},
		Stations: []Station{{Name: "a", Port: 3500, Env: map[string]string{"HTTPS_PROXY": ""}}}}
	if err := Validate(ok); err != nil {
		t.Fatalf("positive control refused: %v", err)
	}
	for name, mutate := range map[string]func(*File){
		"station sets XDG_CONFIG_HOME": func(f *File) { f.Stations[0].Env = map[string]string{"XDG_CONFIG_HOME": "/x"} },
		"station sets MAGPIE_ADDR":     func(f *File) { f.Stations[0].Env = map[string]string{"MAGPIE_ADDR": "127.0.0.1:1"} },
		"file sets MAGPIE_WEB_KEY":     func(f *File) { f.Env = map[string]string{"MAGPIE_WEB_KEY": "k"} },
		"file sets " + StationsVar:     func(f *File) { f.Env = map[string]string{StationsVar: "/x"} },
		"a name that is no variable":   func(f *File) { f.Stations[0].Env = map[string]string{"1X": "y"} },
		"a name with =":                func(f *File) { f.Env = map[string]string{"A=B": "y"} },
		"a relative runner":            func(f *File) { f.Runner = "magpie" },
	} {
		f := ok
		f.Stations = append([]Station(nil), ok.Stations...)
		mutate(&f)
		if err := Validate(f); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}
