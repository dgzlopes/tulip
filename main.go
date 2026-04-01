package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

const usage = `tulip — parallel Claude Code sessions on a single repo

Usage:
  tulip                     launch TUI
  tulip claude <branch>     attach to the Claude session (restart if needed)
  tulip shell <branch>      open an empty terminal in the worktree
  tulip graft <branch>      yarn install + watch (for Graft live preview)
  tulip publish <branch>    stage all, commit (signed), and push
  tulip vscode <branch>     open the worktree in VS Code
  tulip reset               wipe all projects

Options:
  -h, --help    show this help
`

func main() {
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

	case "shell":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: tulip shell <branch>")
			os.Exit(1)
		}
		if err := cmdShell(args[1]); err != nil {
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
	{"graft", "yarn install + watch (Graft live preview)"},
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
		return cmdOpen(w.Branch)
	case 1:
		return cmdShell(w.Branch)
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
	}
	return nil
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
func cmdTUI() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	gitEnsureExclude(repoRoot)

	s, err := loadState(repoRoot)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	fm, err := runTUI(s, repoRoot)
	if err != nil {
		return err
	}

	if fm.pickedAction < 0 || fm.pickedWorker == nil {
		return nil
	}
	branch := fm.pickedWorker.Branch
	switch fm.pickedAction {
	case 0:
		return cmdOpen(branch)
	case 1:
		return cmdShell(branch)
	case 2:
		return cmdWatch(branch)
	case 3:
		return cmdVSCode(branch)
	case 4:
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
	return nil
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
		if err := tmuxNewSession(w.Session, w.Worktree); err != nil {
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

// tmuxAttach clears the screen then replaces the current process with tmux attach-session.
func tmuxAttach(target string) error {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}
	fmt.Print("\033[H\033[2J\033[3J")
	return syscall.Exec(tmuxBin, []string{"tmux", "attach-session", "-t", target}, os.Environ())
}

// cmdOpen attaches to (or restarts) the Claude session for the given branch.
func cmdOpen(branch string) error {
	_, w, err := requireWorker(branch)
	if err != nil {
		return err
	}
	return tmuxAttach(w.Session)
}

// cmdShell opens an interactive shell in the worktree, replacing the current process.
func cmdShell(branch string) error {
	_, w, err := requireWorker(branch)
	if err != nil {
		return err
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	fmt.Print("\033[H\033[2J\033[3J")
	cmd := exec.Command(shell)
	cmd.Dir = w.Worktree
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// cmdWatch runs yarn install then yarn watch in the worktree (for Graft live preview).
func cmdWatch(branch string) error {
	_, w, err := requireWorker(branch)
	if err != nil {
		return err
	}
	winName := "watch/" + branch
	if err := tmuxNewWindow(w.Session, winName, w.Worktree, "yarn install && yarn run watch"); err != nil {
		return fmt.Errorf("create watch window: %w", err)
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

	fmt.Printf("This will delete %d branch(es), kill their sessions, and remove their worktrees.\n", len(s.Workers))
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
