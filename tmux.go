package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// tmuxSocket is the path to the dedicated tmux server socket for this repo.
// Set once at startup via initTmuxSocket. All tmux commands use this socket
// so tulip sessions are completely isolated from the user's own tmux.
var tmuxSocket string

// initTmuxSocket sets the socket path derived from the repo's .tulip directory.
func initTmuxSocket(tulipDir string) {
	tmuxSocket = tulipDir + "/tmux.sock"
}

// tmuxArgs prepends the -S socket flag when a socket has been configured.
func tmuxArgs(args []string) []string {
	if tmuxSocket == "" {
		return args
	}
	return append([]string{"-S", tmuxSocket}, args...)
}

// tmuxRun runs a tmux command with the given arguments.
func tmuxRun(args ...string) error {
	cmd := exec.Command("tmux", tmuxArgs(args)...)
	return cmd.Run()
}

// tmuxHasSession returns true if a tmux session with the given name exists.
func tmuxHasSession(name string) bool {
	cmd := exec.Command("tmux", tmuxArgs([]string{"has-session", "-t", name})...)
	return cmd.Run() == nil
}

// tmuxNewSession creates a new detached tmux session with the given name, starting in startDir.
func tmuxNewSession(name, startDir string) error {
	return tmuxRun("new-session", "-d", "-s", name, "-c", startDir)
}

// tmuxSendKeys sends a command followed by Enter to the given tmux session.
func tmuxSendKeys(session, command string) error {
	return tmuxRun("send-keys", "-t", session, command, "Enter")
}

// tmuxNewWindow creates a new detached window in the given session, starting in startDir.
// If command is non-empty it is passed directly to new-window so the window's lifetime
// is tied to the process — when the command exits, the window closes.
func tmuxNewWindow(session, name, startDir, command string) error {
	args := []string{"new-window", "-d", "-t", session, "-n", name, "-c", startDir}
	if command != "" {
		args = append(args, command)
	}
	return tmuxRun(args...)
}


// tmuxHasWindow returns true if a window with the given name exists in the session.
func tmuxHasWindow(session, window string) bool {
	cmd := exec.Command("tmux", tmuxArgs([]string{"list-windows", "-t", session, "-F", "#{window_name}"})...)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == window {
			return true
		}
	}
	return false
}

// tmuxKillWindow kills a named window inside a session. No-op if it doesn't exist.
func tmuxKillWindow(session, window string) {
	_ = tmuxRun("kill-window", "-t", session+":"+window)
}

// tmuxSetWindowOption sets a tmux window option on a specific window.
func tmuxSetWindowOption(session, window, option, value string) {
	_ = tmuxRun("set-window-option", "-t", session+":"+window, option, value)
}

// tmuxIsWindowDead returns true if the window exists but its pane has exited.
func tmuxIsWindowDead(session, window string) bool {
	cmd := exec.Command("tmux", tmuxArgs([]string{
		"display-message", "-p", "-t", session + ":" + window, "#{pane_dead}",
	})...)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

// tmuxWindowExitStatus returns the exit status of a dead pane, or -1 on error.
func tmuxWindowExitStatus(session, window string) int {
	cmd := exec.Command("tmux", tmuxArgs([]string{
		"display-message", "-p", "-t", session + ":" + window, "#{pane_dead_status}",
	})...)
	out, err := cmd.Output()
	if err != nil {
		return -1
	}
	var code int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &code)
	return code
}

// tmuxKillSession kills a tmux session by name. If the session doesn't exist, it's a no-op.
func tmuxKillSession(name string) error {
	if !tmuxHasSession(name) {
		return nil
	}
	return tmuxRun("kill-session", "-t", name)
}
