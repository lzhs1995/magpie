package stations

import (
	"os/exec"
	"syscall"
)

// detach starts the station's magpie in a process group of its own with
// no console, so Ctrl-C in this one's console doesn't end it and it goes
// on once this magpie is quit.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
}
