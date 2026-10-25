// Package proc starts and stops child process trees.
package proc

import (
	"os/exec"
	"runtime"
)

// Shell runs a command the way a developer would type it.
func Shell(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd.exe", "/d", "/s", "/c", command)
	}
	return exec.Command("sh", "-c", command)
}

// ConfigureChild puts the command in its own process group so lnch can stop the tree.
func ConfigureChild(cmd *exec.Cmd) { configureChild(cmd) }

// ConfigureDetach starts a supervisor that survives the parent exiting.
func ConfigureDetach(cmd *exec.Cmd) { configureDetach(cmd) }

// KillTree stops pid and the processes it spawned.
func KillTree(pid int) error { return killTree(pid) }

// Alive reports whether pid is still a running process.
func Alive(pid int) bool { return alive(pid) }
