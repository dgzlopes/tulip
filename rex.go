package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// useRex selects the Rex backend: worker "sessions" and "windows" become Rex
// tabs in the Rex session tulip is running in, instead of tmux windows.
var useRex bool

// initBackend picks the backend from --backend=rex|tmux (already stripped from
// args by the caller), TULIP_BACKEND, or auto-detection: Rex when REX_SESSION
// is set, tmux otherwise.
func initBackend(flag string) error {
	choice := flag
	if choice == "" {
		choice = os.Getenv("TULIP_BACKEND")
	}
	switch choice {
	case "tmux":
		useRex = false
	case "rex":
		if os.Getenv("REX_SESSION") == "" {
			return fmt.Errorf("--backend=rex needs to run inside a Rex terminal (REX_SESSION is not set)")
		}
		useRex = true
	case "", "auto":
		useRex = os.Getenv("REX_SESSION") != ""
	default:
		return fmt.Errorf("unknown backend %q (want rex or tmux)", choice)
	}
	return nil
}

type rexWindow struct {
	ID    string `json:"window_id"`
	Label string `json:"label"`
	Block string `json:"focused_block_id"`
}

var (
	rexCacheMu   sync.Mutex
	rexCache     []rexWindow
	rexCacheTime time.Time
)

func rexCmd(args ...string) ([]byte, error) {
	full := append([]string{args[0], "-s", os.Getenv("REX_SESSION")}, args[1:]...)
	if args[0] == "window" || args[0] == "block" {
		full = append([]string{args[0], args[1], "-s", os.Getenv("REX_SESSION")}, args[2:]...)
	}
	out, err := exec.Command("rex", full...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("rex %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func rexInvalidate() {
	rexCacheMu.Lock()
	rexCacheTime = time.Time{}
	rexCacheMu.Unlock()
}

// rexWindows lists the tabs of the current Rex session. Results are cached
// briefly because the TUI polls several workers per tick.
func rexWindows() []rexWindow {
	rexCacheMu.Lock()
	defer rexCacheMu.Unlock()
	if time.Since(rexCacheTime) < 400*time.Millisecond {
		return rexCache
	}
	out, err := exec.Command("rex", "window", "ls", "-s", os.Getenv("REX_SESSION"), "--json").Output()
	if err != nil {
		return rexCache
	}
	var resp struct {
		Windows []rexWindow `json:"windows"`
	}
	if json.Unmarshal(out, &resp) != nil {
		return rexCache
	}
	rexCache, rexCacheTime = resp.Windows, time.Now()
	return rexCache
}

var (
	rexMu       sync.RWMutex
	rexPrefix   string                // from config tab-prefix; "" keeps "<branch> · <kind>" names
	rexBranches = map[string]string{} // session name -> real branch name
)

// rexLoad reads the tab prefix and the known workers' branch names, so tab
// labels can use the real branch instead of the slugged session name.
func rexLoad() {
	root, err := findRepoRoot()
	if err != nil {
		return
	}
	rexMu.Lock()
	defer rexMu.Unlock()
	if cfg, err := loadConfig(root); err == nil {
		rexPrefix = cfg.TabPrefix
	}
	if s, err := loadState(root); err == nil {
		for _, w := range s.Workers {
			rexBranches[w.Session] = w.Branch
		}
	}
}

func rexRegister(session, branch string) {
	rexMu.Lock()
	rexBranches[session] = branch
	rexMu.Unlock()
}

// rexShort strips the "tulip-" prefix tulip puts on session names.
func rexShort(session string) string { return strings.TrimPrefix(session, "tulip-") }

// rexGroup is the label shared by all of a worker's tabs: "<prefix><branch>"
// when a prefix is configured, otherwise the slugged session name.
func rexGroup(session string) string {
	rexMu.RLock()
	defer rexMu.RUnlock()
	if rexPrefix == "" {
		return rexShort(session)
	}
	branch := rexBranches[session]
	if branch == "" {
		branch = rexShort(session)
	}
	return rexPrefix + branch
}

// rexLabel names the tab for a (session, window) pair: "<branch> · <kind>",
// where kind is the first path segment of the tmux-style window name.
func rexLabel(session, window string) string {
	kind, _, _ := strings.Cut(window, "/")
	rexMu.RLock()
	prefixed := rexPrefix != ""
	rexMu.RUnlock()
	// With a prefix the Claude tab is just "<prefix><branch>"; other tabs add their kind.
	if prefixed && kind == "claude" {
		return rexGroup(session)
	}
	return rexGroup(session) + " · " + kind
}

// rexSplitTarget splits a tmux-style "session[:window]" target.
func rexSplitTarget(target string) (session, window string) {
	session, window, ok := strings.Cut(target, ":")
	if !ok {
		window = "claude"
	}
	return session, window
}

// rexFind finds a tab by label. A tab whose label extends the base with
// " · <status>" (see rexSetGraftStatus) still matches.
func rexFind(label string) *rexWindow {
	for _, w := range rexWindows() {
		w := w
		if w.Label == label || strings.HasPrefix(w.Label, label+" · graft: ") {
			return &w
		}
	}
	return nil
}

func rexHasSession(name string) bool { return rexFind(rexLabel(name, "claude")) != nil }

func rexHasWindow(session, window string) bool { return rexFind(rexLabel(session, window)) != nil }

func rexNewTab(label, dir, command string, keepOpen bool) error {
	args := []string{"window", "new", label, "-c", dir, "--focus=false", "--json"}
	if keepOpen {
		args = append(args, "--keep-open")
	}
	if command != "" {
		args = append(args, "--", "sh", "-c", command)
	}
	out, err := rexCmd(args...)
	rexInvalidate()
	if err != nil {
		return err
	}
	var created struct {
		WindowID string `json:"window_id"`
	}
	if json.Unmarshal(out, &created) == nil && created.WindowID != "" {
		rexGroupAfterPeers(created.WindowID)
	}
	return nil
}

// rexGroupAfterPeers moves a new tab right after the last existing tab that
// shares the configured prefix, so a repo's tabs stay together and in creation
// order. With no prefix, or no peers, the tab stays where Rex put it.
func rexGroupAfterPeers(newID string) {
	rexMu.RLock()
	prefix := rexPrefix
	rexMu.RUnlock()
	if prefix == "" {
		return
	}
	lastPeer := ""
	for _, w := range rexWindows() {
		if w.ID != newID && strings.HasPrefix(w.Label, prefix) {
			lastPeer = w.ID
		}
	}
	if lastPeer == "" {
		return
	}
	_, _ = rexCmd("window", "move", newID, "--after", lastPeer)
	rexInvalidate()
}

func rexNewSession(name, branch, startDir string) error {
	rexRegister(name, branch)
	return rexNewTab(rexLabel(name, "claude"), startDir, "", false)
}

func rexNewWindow(session, window, startDir, command string) error {
	// Graft watch tabs stay open after the command exits so the failure is visible.
	return rexNewTab(rexLabel(session, window), startDir, command, strings.HasPrefix(window, "watch/"))
}

func rexSendKeys(target, command string) error {
	session, window := rexSplitTarget(target)
	w := rexFind(rexLabel(session, window))
	if w == nil {
		return fmt.Errorf("no Rex tab for %s", target)
	}
	if _, err := rexCmd("send", "-b", w.Block, command); err != nil {
		return err
	}
	_, err := rexCmd("send-key", "-b", w.Block, "enter")
	return err
}

func rexKillWindow(session, window string) {
	if w := rexFind(rexLabel(session, window)); w != nil {
		_, _ = rexCmd("window", "close", w.ID)
		rexInvalidate()
	}
}

func rexKillSession(name string) {
	group := rexGroup(name)
	for _, w := range rexWindows() {
		if w.Label == group || strings.HasPrefix(w.Label, group+" · ") {
			_, _ = rexCmd("window", "close", w.ID)
		}
	}
	rexInvalidate()
}

// rexLastExit reports whether the tab's process has exited, and with what code.
func rexLastExit(session, window string) (dead bool, code int) {
	w := rexFind(rexLabel(session, window))
	if w == nil {
		return false, -1
	}
	out, err := exec.Command("rex", "block", "call", "-s", os.Getenv("REX_SESSION"), "-b", w.Block, "process").Output()
	if err != nil {
		return false, -1
	}
	var p struct {
		LastExit json.RawMessage `json:"last_exit"`
	}
	if json.Unmarshal(out, &p) != nil || len(p.LastExit) == 0 || string(p.LastExit) == "null" {
		return false, -1
	}
	var n int
	if json.Unmarshal(p.LastExit, &n) == nil {
		return true, n
	}
	var obj map[string]any
	if json.Unmarshal(p.LastExit, &obj) == nil {
		for _, k := range []string{"code", "status", "exit_code"} {
			if v, ok := obj[k].(float64); ok {
				return true, int(v)
			}
		}
	}
	return true, -1
}

// rexSetGraftStatus shows the graft state in the worker's Claude tab label.
func rexSetGraftStatus(session, status string) {
	w := rexFind(rexLabel(session, "claude"))
	if w == nil {
		return
	}
	label := rexLabel(session, "claude")
	if status != "" {
		label += " · graft: " + status
	}
	if w.Label != label {
		_, _ = rexCmd("window", "rename", w.ID, label)
		rexInvalidate()
	}
}

// rexFocus brings a tab to the front; it replaces tmux attach.
func rexFocus(target string) error {
	session, window := rexSplitTarget(target)
	w := rexFind(rexLabel(session, window))
	if w == nil {
		return fmt.Errorf("no Rex tab for %s", target)
	}
	_, err := rexCmd("focus", w.ID)
	return err
}
