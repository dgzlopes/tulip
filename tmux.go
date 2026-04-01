package main

import (
	"os/exec"
)

// tmuxRun runs a tmux command with the given arguments.
func tmuxRun(args ...string) error {
	cmd := exec.Command("tmux", args...)
	return cmd.Run()
}

// tmuxHasSession returns true if a tmux session with the given name exists.
func tmuxHasSession(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", name)
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

// tmuxNewWindow creates a new detached window in the given session, starting in startDir,
// and optionally runs a command in it. Returns the window index as a string (e.g. "1").
func tmuxNewWindow(session, name, startDir, command string) error {
	args := []string{"new-window", "-d", "-t", session, "-n", name, "-c", startDir}
	if err := tmuxRun(args...); err != nil {
		return err
	}
	if command != "" {
		// target the newly created window by its name
		return tmuxRun("send-keys", "-t", session+":"+name, command, "Enter")
	}
	return nil
}

// tmuxKillSession kills a tmux session by name. If the session doesn't exist, it's a no-op.
func tmuxKillSession(name string) error {
	if !tmuxHasSession(name) {
		return nil
	}
	return tmuxRun("kill-session", "-t", name)
}
