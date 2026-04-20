package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

const usage = `tulip — parallel Claude Code sessions on a single repo

Usage:
  tulip                              launch TUI
  tulip claude <branch>              attach to the Claude session (restart if needed)
  tulip shell <branch>               open an empty terminal in the worktree
  tulip graft <branch>               switch active Graft preview to this branch
  tulip graft-debug <branch>         attach to the Graft watch output
  tulip publish <branch>             stage all, commit (signed), and push
  tulip vscode <branch>              open the worktree in VS Code
  tulip config graft-command <cmd>   set the command run when grafting (default: yarn install && yarn run watch)
  tulip reset                        wipe all projects

Options:
  -h, --help    show this help
`

func main() {
	// Initialise the dedicated tmux socket as early as possible so all tmux
	// helpers use the isolated server for this repo. Best-effort — if the repo
	// root can't be found (e.g. -h/--version flags) we just leave it empty.
	if root, err := findRepoRoot(); err == nil {
		initTmuxSocket(filepath.Join(root, ".tulip"))
		// Re-apply global tmux settings on every tulip invocation so that
		// existing sessions pick up binding fixes from newer versions.
		tmuxApplyGlobalSettings()
	}

	args := os.Args[1:]

	if len(args) == 0 {
		if err := cmdTUI(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	switch args[0] {
	case "-h", "--help":
		fmt.Print(usage)

	case "-v", "--version", "version":
		fmt.Println(version)

	case "claude", "open", "attach": // open/attach kept as aliases
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: tulip claude <branch>")
			os.Exit(1)
		}
		if err := cmdOpen(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := cmdTUI(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "shell":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: tulip shell <branch>")
			os.Exit(1)
		}
		if err := cmdShell(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := cmdTUI(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "graft":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: tulip graft <branch>")
			os.Exit(1)
		}
		if err := cmdWatch(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "graft-debug":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: tulip graft-debug <branch>")
			os.Exit(1)
		}
		if err := cmdGraftDebug(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := cmdTUI(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "publish":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: tulip publish <branch> <message>")
			os.Exit(1)
		}
		if err := cmdPublish(args[1], strings.Join(args[2:], " ")); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "vscode":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: tulip vscode <branch>")
			os.Exit(1)
		}
		if err := cmdVSCode(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "reset":
		if err := cmdReset(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "config":
		if len(args) < 3 || args[1] != "graft-command" {
			fmt.Fprintln(os.Stderr, "usage: tulip config graft-command <command>")
			os.Exit(1)
		}
		if err := cmdConfigSet("graft-command", strings.Join(args[2:], " ")); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	default:
		// Treat as a branch name — show an interactive command picker.
		if err := cmdPick(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}

var pickActions = []struct {
	name string
	desc string
}{
	{"claude", "attach to Claude session"},
	{"shell", "open a terminal in the worktree"},
	{"graft", "switch Graft preview to this branch"},
	{"graft-debug", "view Graft watch output"},
	{"vscode", "open in VS Code"},
	{"publish", "stage, commit, and push"},
}

// cmdPick resolves a branch name or numeric project ID, then shows an
// interactive one-keypress menu to choose which command to run.
func cmdPick(nameOrID string) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	s, err := loadState(repoRoot)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	w := findWorker(s, nameOrID)
	if w == nil {
		return fmt.Errorf("no project found for %q", nameOrID)
	}
	chosen, err := runPicker(w.Branch)
	if err != nil || chosen < 0 {
		return err
	}
	switch chosen {
	case 0:
		if err := cmdOpen(w.Branch); err != nil {
			return err
		}
	case 1:
		if err := cmdShell(w.Branch); err != nil {
			return err
		}
	case 2:
		return cmdWatch(w.Branch)
	case 3:
		return cmdVSCode(w.Branch)
	case 4:
		fmt.Print("  commit message: ")
		reader := bufio.NewReader(os.Stdin)
		msg, _ := reader.ReadString('\n')
		msg = strings.TrimSpace(msg)
		if msg == "" {
			fmt.Println("  cancelled.")
			return nil
		}
		return cmdPublish(w.Branch, msg)
	default:
		return nil
	}
	return cmdTUI()
}

type pickerModel struct {
	branch  string
	cursor  int
	chosen  int
}

func (p pickerModel) Init() tea.Cmd { return nil }

func (p pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if p.cursor > 0 {
				p.cursor--
			}
		case "down", "j":
			if p.cursor < len(pickActions)-1 {
				p.cursor++
			}
		case "enter":
			p.chosen = p.cursor
			return p, tea.Quit
		case "q", "esc", "ctrl+c":
			p.chosen = -1
			return p, tea.Quit
		}
	}
	return p, nil
}

func (p pickerModel) View() string {
	var b strings.Builder
	b.WriteString("\n  " + sBold.Render(p.branch) + "\n\n")
	for i, a := range pickActions {
		if i == p.cursor {
			b.WriteString("  " + sCyan.Render("▶") + " " + sBold.Render(a.name) + "  " + sDim.Render(a.desc) + "\n")
		} else {
			b.WriteString("    " + sGrey.Render(a.name) + "  " + sDim.Render(a.desc) + "\n")
		}
	}
	b.WriteString("\n  " + sDim.Render("↑↓ navigate   enter select   esc cancel"))
	return b.String()
}

func runPicker(branch string) (int, error) {
	m := pickerModel{branch: branch, chosen: -1}
	prog := tea.NewProgram(m)
	final, err := prog.Run()
	if err != nil {
		return -1, err
	}
	return final.(pickerModel).chosen, nil
}

// cmdTUI launches the Bubble Tea TUI and executes any action chosen inside it.
// After terminal-taking actions (claude, shell, graft-debug) return, the TUI
// is shown again so the user can pick another action without restarting tulip.
func cmdTUI() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	gitEnsureExclude(repoRoot)

	resumeBranch := ""
	for {
		s, err := loadState(repoRoot)
		if err != nil {
			return fmt.Errorf("load state: %w", err)
		}

		fm, err := runTUI(s, repoRoot, resumeBranch)
		if err != nil {
			return err
		}
		resumeBranch = ""

		if fm.pickedAction < 0 || fm.pickedWorker == nil {
			return nil
		}
		branch := fm.pickedWorker.Branch
		switch fm.pickedAction {
		case 0:
			if err := cmdOpen(branch); err != nil {
				return err
			}
			resumeBranch = branch
		case 1:
			if err := cmdShell(branch); err != nil {
				return err
			}
			resumeBranch = branch
		case 2:
			return cmdWatch(branch)
		case 3:
			if err := cmdGraftDebug(branch); err != nil {
				return err
			}
			resumeBranch = branch
		case 4:
			return cmdVSCode(branch)
		case 5:
			fmt.Print("commit message: ")
			reader := bufio.NewReader(os.Stdin)
			msg, _ := reader.ReadString('\n')
			msg = strings.TrimSpace(msg)
			if msg == "" {
				fmt.Println("cancelled.")
				return nil
			}
			return cmdPublish(branch, msg)
		}
	}
}

// requireWorker loads state, finds the worker for branch, and ensures its tmux
// session exists (auto-restarting with --resume/--name if needed).
func requireWorker(branch string) (*State, *Worker, error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return nil, nil, err
	}

	s, err := loadState(repoRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("load state: %w", err)
	}

	w := findWorker(s, branch)
	if w == nil {
		return nil, nil, fmt.Errorf("no project found for branch %q", branch)
	}

	if _, err := os.Stat(w.Worktree); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("worktree for %q no longer exists at %s — run `tulip reset` or delete and recreate the branch", w.Branch, w.Worktree)
	}

	if !tmuxHasSession(w.Session) {
		if err := tmuxNewSession(w.Session, w.Branch, w.Worktree); err != nil {
			return nil, nil, fmt.Errorf("restart session: %w", err)
		}
		claudeCmd := fmt.Sprintf("claude --name %q", w.Branch)
		if w.SessionStarted {
			claudeCmd = fmt.Sprintf("claude --resume %q", w.Branch)
		}
		if err := tmuxSendKeys(w.Session, claudeCmd); err != nil {
			return nil, nil, fmt.Errorf("send keys: %w", err)
		}
		w.SessionStarted = true
		w.Status = "waiting"
		if err := saveState(s); err != nil {
			return nil, nil, fmt.Errorf("save state: %w", err)
		}
	}

	return s, w, nil
}

// tmuxAttach clears the screen then runs tmux attach-session as a subprocess.
// When the user detaches (Ctrl+B D), the subprocess exits and control returns
// to the caller so the TUI can be shown again.
func tmuxAttach(target string) error {
	fmt.Print("\033[H\033[2J\033[3J")
	// Suppress WheelUpPane during the attach transition: some terminals fire a
	// spurious scroll event as tmux takes over the terminal, which would enter
	// copy mode immediately. Restore the binding after a short delay.
	_ = tmuxRun("bind-key", "-n", "WheelUpPane", "send-keys", "-M")
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = tmuxRun("bind-key", "-n", "WheelUpPane", "copy-mode", "-e")
	}()
	cmd := exec.Command("tmux", tmuxArgs([]string{"attach-session", "-t", target})...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// cmdOpen attaches to (or restarts) the Claude session for the given branch.
func cmdOpen(branch string) error {
	_, w, err := requireWorker(branch)
	if err != nil {
		return err
	}
	return tmuxAttach(w.Session + ":claude")
}

// cmdShell opens an interactive shell in a persistent tmux window inside the worker's session.
// The window is reused across visits so scrollback and history are preserved.
func cmdShell(branch string) error {
	_, w, err := requireWorker(branch)
	if err != nil {
		return err
	}
	winName := "shell/" + branch
	if !tmuxHasWindow(w.Session, winName) {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		if err := tmuxNewWindow(w.Session, winName, w.Worktree, shell); err != nil {
			return err
		}
	}
	return tmuxAttach(w.Session + ":" + winName)
}

// cmdWatch switches the active Graft preview to the given branch.
// Any existing watch window (across all workers) is killed first.
func cmdWatch(branch string) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	s, err := loadState(repoRoot)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(repoRoot)
	if err != nil {
		return err
	}
	// Validate the target before touching anything.
	var target *Worker
	for i := range s.Workers {
		if s.Workers[i].Branch == branch {
			target = &s.Workers[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("no project found for %q", branch)
	}
	if !tmuxHasSession(target.Session) {
		return fmt.Errorf("%q is not running — start it first with: tulip claude %s", branch, branch)
	}
	// Kill any existing watch window across all workers.
	for _, w := range s.Workers {
		winName := "watch/" + w.Branch
		if tmuxHasWindow(w.Session, winName) {
			fmt.Printf("stopped grafting %s\n", w.Branch)
			tmuxKillWindow(w.Session, winName)
		}
	}
	winName := "watch/" + branch
	if err := tmuxNewWindow(target.Session, winName, target.Worktree, ""); err != nil {
		return fmt.Errorf("could not start graft for %q: %w", branch, err)
	}
	tmuxSetWindowOption(target.Session, winName, "remain-on-exit", "on")
	if err := graftSymlinkDist(repoRoot, target.Worktree); err != nil {
		tmuxKillWindow(target.Session, winName)
		return fmt.Errorf("could not symlink dist: %w", err)
	}
	if err := tmuxSendKeys(target.Session+":"+winName, cfg.GraftCmd()); err != nil {
		return fmt.Errorf("could not send graft command: %w", err)
	}
	fmt.Printf("grafting %s\n", branch)
	return nil
}

// cmdGraftDebug attaches to the active watch window for the given branch.
func cmdGraftDebug(branch string) error {
	_, w, err := requireWorker(branch)
	if err != nil {
		return err
	}
	winName := "watch/" + branch
	if !tmuxHasWindow(w.Session, winName) {
		return fmt.Errorf("%q is not currently being grafted", branch)
	}
	return tmuxAttach(w.Session + ":" + winName)
}

// cmdPublish stages all changes, commits (signed), and pushes.
func cmdPublish(branch, message string) error {
	_, w, err := requireWorker(branch)
	if err != nil {
		return err
	}
	if err := gitStageAndCommit(w.Worktree, message); err != nil {
		return err
	}
	return gitPush(w.Worktree, w.Branch)
}

// openInBrowser opens a URL in the default browser.
func openInBrowser(url string) error {
	return exec.Command("open", url).Run()
}

// cmdDiffit runs `difit .` in a tmux window inside the worker's session.
func cmdDiffit(branch string) error {
	_, w, err := requireWorker(branch)
	if err != nil {
		return err
	}
	return tmuxNewWindow(w.Session, "difit", w.Worktree, "difit --include-untracked .")
}

// cmdVSCode opens the worktree in VS Code.
func cmdVSCode(branch string) error {
	_, w, err := requireWorker(branch)
	if err != nil {
		return err
	}
	cmd := exec.Command("code", "--add", w.Worktree)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// cmdReset wipes all workers, sessions, and worktrees after user confirmation.
func cmdConfigSet(key, value string) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(repoRoot)
	if err != nil {
		return err
	}
	switch key {
	case "graft-command":
		cfg.GraftCommand = value
		if err := saveConfig(repoRoot, cfg); err != nil {
			return err
		}
		fmt.Printf("graft-command set to: %s\n", value)
	default:
		return fmt.Errorf("unknown config key %q", key)
	}
	return nil
}

func cmdReset() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	s, err := loadState(repoRoot)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	if len(s.Workers) == 0 {
		fmt.Println("No projects to reset.")
		return nil
	}

	var dirty []string
	for _, w := range s.Workers {
		if gitIsDirty(w.Worktree) {
			dirty = append(dirty, w.Branch)
		}
	}
	if len(dirty) > 0 {
		fmt.Println("Warning: the following projects have uncommitted changes that will be lost:")
		for _, b := range dirty {
			fmt.Printf("  - %s\n", b)
		}
	}
	fmt.Printf("This will kill %d session(s) and remove their worktrees. Branches are kept.\n", len(s.Workers))
	fmt.Print("Reset everything? [y/N] ")

	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "y" {
		fmt.Println("Aborted.")
		return nil
	}

	for _, w := range s.Workers {
		fmt.Printf("  killing session %s…\n", w.Session)
		if err := tmuxKillSession(w.Session); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not kill session %s: %v\n", w.Session, err)
		}

		fmt.Printf("  removing worktree %s…\n", w.Worktree)
		if err := gitRemoveWorktree(repoRoot, w.Worktree); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not remove worktree %s: %v\n", w.Worktree, err)
		}
	}

	tulipDir := filepath.Join(repoRoot, ".tulip")
	fmt.Printf("  removing %s…\n", tulipDir)
	if err := os.RemoveAll(tulipDir); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not remove %s: %v\n", tulipDir, err)
	}

	fmt.Println("Done.")
	return nil
}
