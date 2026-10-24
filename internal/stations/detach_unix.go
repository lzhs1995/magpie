//go:build !windows

package stations

import (
	"os/exec"
	"syscall"
)

// detach puts the station's magpie in a session of its own, so it isn't
// ended with this process's group and goes on once this magpie is quit:
// a station a reader started from the page keeps serving its agents
// whether or not the window is open.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
