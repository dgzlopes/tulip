package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Colours ───────────────────────────────────────────────────────────────────

var (
	cCyan   = lipgloss.Color("6")
	cGreen  = lipgloss.Color("2")
	cRed    = lipgloss.Color("1")
	cGrey   = lipgloss.Color("8")
	cPurple = lipgloss.Color("5")
)

// ── Base styles ───────────────────────────────────────────────────────────────

var (
	sCyan    = lipgloss.NewStyle().Foreground(cCyan)
	sGreen   = lipgloss.NewStyle().Foreground(cGreen)
	sRed     = lipgloss.NewStyle().Foreground(cRed)
	sGrey    = lipgloss.NewStyle().Foreground(cGrey)
	sPurple  = lipgloss.NewStyle().Foreground(cPurple)
	sTitle   = lipgloss.NewStyle().Foreground(cPurple).Bold(true)
	sBold    = lipgloss.NewStyle().Foreground(cGrey).Bold(true)
	sDim     = lipgloss.NewStyle().Faint(true)
	sCommand = lipgloss.NewStyle().Foreground(cGrey).Italic(true)
	sKey     = lipgloss.NewStyle().Foreground(cCyan).Bold(true)
	sHeader  = lipgloss.NewStyle().Bold(true).Foreground(cGrey)
)

// ── Mode ──────────────────────────────────────────────────────────────────────

type mode int

const (
	modeNormal      mode = iota
	modeNewMenu          // choose: create / pick / fork
	modeNewDirect        // type a new project name
	modeNewPick          // pick from existing branches
	modeNewForkBase      // pick base branch to fork from
	modeNewForkName      // type name for the new forked branch
	modeDelete
	modeStaleWorktree // confirm prune-and-retry
	modeBranchExists  // confirm start from existing branch
	modePick          // action picker for selected project
	modePublish       // commit message input before publishing
	modeHelp          // help screen
	modeHistory       // deleted projects history
)

// ── Messages ──────────────────────────────────────────────────────────────────

type branchesLoadedMsg []string
type tickMsg struct{}
type slowTickMsg struct{}
type prRefreshedMsg struct {
	branch string
	pr     prInfo
}
type workerCreatedMsg struct{ branch string }
type workerDeletedMsg struct{ branch string }
type errMsg struct{ err error }
type staleWorktreeMsg struct{ branch, base string }
type branchExistsMsg struct{ branch string }
type fetchDoneMsg struct {
	branch string
	base   string // empty means use default remote branch
}
type graftDoneMsg struct {
	branch string
	err    error
}
type publishDoneMsg struct {
	branch string
	err    error
}

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	state              *State
	repoRoot           string
	cursor             int
	mode               mode
	input              textinput.Model
	branches           []string
	filtered           []string
	listCursor         int
	actionWorker       *Worker
	historyCursor      int
	historyScroll      int
	notif              string
	notifIsErr         bool
	notifTick          int
	workersScroll      int
	menuCursor         int
	forkBase           string
	staleBranch        string
	staleBase          string
	branchExistsBranch string
	width              int
	pickCursor         int
	pickedWorker       *Worker
	pickedAction       int // -1 = none
	spinner            spinner.Model
	grafting           bool
	fetching           bool
	prLoading          int
	publishBranch      string
}

func newModel(s *State, repoRoot string) model {
	ti := textinput.New()
	ti.Placeholder = "branch name or filter…"
	ti.CharLimit = 200
	ti.Width = 40
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(cCyan)
	return model{
		state:        s,
		repoRoot:     repoRoot,
		input:        ti,
		width:        80,
		pickedAction: -1,
		spinner:      sp,
		prLoading:    len(s.Workers),
	}
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{loadBranchesCmd(m.repoRoot), tickCmd(), slowTickCmd(), restoreAllWorkersCmd(m.state)}
	for _, w := range m.state.Workers {
		cmds = append(cmds, refreshPRCmd(w.Branch))
	}
	if m.prLoading > 0 {
		cmds = append(cmds, m.spinner.Tick)
	}
	return tea.Batch(cmds...)
}

func loadBranchesCmd(repoRoot string) tea.Cmd {
	return func() tea.Msg { return branchesLoadedMsg(gitListRecentBranches(repoRoot)) }
}

func (m *model) addLog(_ string) {} // logs hidden

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(_ time.Time) tea.Msg { return tickMsg{} })
}

func slowTickCmd() tea.Cmd {
	return tea.Tick(30*time.Second, func(_ time.Time) tea.Msg { return slowTickMsg{} })
}

func refreshPRCmd(branch string) tea.Cmd {
	return func() tea.Msg {
		pr, _ := fetchPRForBranch(branch)
		return prRefreshedMsg{branch: branch, pr: pr}
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width

	case branchesLoadedMsg:
		m.branches = []string(msg)
		m.filtered = m.branches

	case tickMsg:
		changed := false
		for i := range m.state.Workers {
			w := &m.state.Workers[i]
			if _, err := os.Stat(w.Worktree); os.IsNotExist(err) {
				if w.Status != "error" {
					w.Status = "error"
					changed = true
				}
				continue
			}
			var newStatus string
			if gitIsDirty(w.Worktree) {
				newStatus = "dirty"
			} else {
				newStatus = "clean"
			}
			if w.Status != newStatus {
				w.Status = newStatus
				changed = true
			}
				if w.GraftStatus == "active" {
				winName := "watch/" + w.Branch
				if tmuxIsWindowDead(w.Session, winName) {
					code := tmuxWindowExitStatus(w.Session, winName)
					tmuxKillWindow(w.Session, winName)
					if code != 0 {
						w.GraftStatus = "failed"
						tmuxSetGraftStatus(w.Session, "failed")
						m.notif = fmt.Sprintf("✗  graft failed (exit %d) on %s", code, w.Branch)
						m.notifIsErr = true
						m.notifTick = 8
					} else {
						w.GraftStatus = ""
						tmuxSetGraftStatus(w.Session, "")
					}
					changed = true
				} else if !tmuxHasWindow(w.Session, winName) {
					w.GraftStatus = ""
					tmuxSetGraftStatus(w.Session, "")
					changed = true
				}
			}
		}
		if changed {
			_ = saveState(m.state)
		}
		if m.notifTick > 0 {
			m.notifTick--
			if m.notifTick == 0 {
				m.notif = ""
			}
		}
		return m, tickCmd()

	case slowTickMsg:
		cmds := []tea.Cmd{slowTickCmd()}
		for _, w := range m.state.Workers {
			m.prLoading++
			cmds = append(cmds, refreshPRCmd(w.Branch))
		}
		if m.prLoading > 0 {
			cmds = append(cmds, m.spinner.Tick)
		}
		return m, tea.Batch(cmds...)

	case prRefreshedMsg:
		if m.prLoading > 0 {
			m.prLoading--
		}
		for i := range m.state.Workers {
			if m.state.Workers[i].Branch == msg.branch {
				w := &m.state.Workers[i]
				if w.PRNumber != msg.pr.Number || w.PRState != msg.pr.State || w.PRURL != msg.pr.URL {
					w.PRNumber = msg.pr.Number
					w.PRState = msg.pr.State
					w.PRURL = msg.pr.URL
					_ = saveState(m.state)
				}
				break
			}
		}
		return m, nil

	case fetchDoneMsg:
		m.fetching = false
		return m, m.createWorkerAfterFetchCmd(msg.branch, msg.base)

	case workerCreatedMsg:
		m.prLoading++
		return m, tea.Batch(refreshPRCmd(msg.branch), m.spinner.Tick)

	case workerDeletedMsg:

	case errMsg:
		text := msg.err.Error()
		m.notif = "✗  " + text
		m.notifIsErr = true
		m.notifTick = 6
		(&m).addLog(sRed.Render("✗") + "  " + text)

	case staleWorktreeMsg:
		m.staleBranch = msg.branch
		m.staleBase = msg.base
		m.mode = modeStaleWorktree

	case branchExistsMsg:
		m.branchExistsBranch = msg.branch
		m.mode = modeBranchExists

	case graftDoneMsg:
		m.grafting = false
		for i := range m.state.Workers {
			if m.state.Workers[i].Branch == msg.branch {
				gs := "active"
				if msg.err != nil {
					gs = "failed"
				}
				m.state.Workers[i].GraftStatus = gs
				tmuxSetGraftStatus(m.state.Workers[i].Session, gs)
				break
			}
		}
		if msg.err != nil {
			m.notif = "✗  " + msg.err.Error()
			m.notifIsErr = true
		}
		m.notifTick = 6

	case publishDoneMsg:
		if msg.err != nil {
			m.notif = "✗  " + msg.err.Error()
			m.notifIsErr = true
		}
		m.notifTick = 6

	case spinner.TickMsg:
		if m.grafting || m.fetching || m.prLoading > 0 {
			m.spinner, _ = m.spinner.Update(msg)
		}

	case tea.KeyMsg:
		switch m.mode {
		case modeNormal:
			return m.updateNormal(msg)
		case modeNewMenu:
			return m.updateNewMenu(msg)
		case modeNewDirect:
			return m.updateNewDirect(msg)
		case modeNewPick:
			return m.updateNewPick(msg)
		case modeNewForkBase:
			return m.updateNewForkBase(msg)
		case modeNewForkName:
			return m.updateNewForkName(msg)
		case modeDelete:
			return m.updateDelete(msg)
		case modeStaleWorktree:
			return m.updateStaleWorktree(msg)
		case modeBranchExists:
			return m.updateBranchExists(msg)
		case modePick:
			return m.updatePick(msg)
		case modePublish:
			return m.updatePublish(msg)
		case modeHelp:
			return m.updateHelp(msg)
		case modeHistory:
			return m.updateHistory(msg)
		}
	}

	return m, nil
}

func (m model) updateNormal(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			(&m).syncWorkersScroll()
		}
	case "down", "j":
		if m.cursor < len(m.state.Workers)-1 {
			m.cursor++
			(&m).syncWorkersScroll()
		}
	case "enter":
		if m.cursor < len(m.state.Workers) {
			m.pickedWorker = &m.state.Workers[m.cursor]
			m.pickCursor = 0
			m.pickedAction = -1
			m.mode = modePick
		}
	case "n":
		m.mode = modeNewMenu
		m.menuCursor = 0
	case "d":
		if m.cursor < len(m.state.Workers) {
			m.actionWorker = &m.state.Workers[m.cursor]
			m.mode = modeDelete
		}
	case "h":
		if len(m.state.DeletedWorkers) > 0 {
			m.historyCursor = 0
			m.historyScroll = 0
			m.mode = modeHistory
		}
	case "?":
		m.mode = modeHelp
	}
	return m, nil
}

// ── New worker sub-modes ──────────────────────────────────────────────────────

var newMenuItems = []struct{ label, desc string }{
	{"Create new project", "start fresh on a new project from HEAD"},
	{"Pick existing branch", "continue work on a branch you already have"},
	{"Fork existing branch", "new project off an existing one"},
}

func (m model) updateNewMenu(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNormal
	case "up", "k":
		if m.menuCursor > 0 {
			m.menuCursor--
		}
	case "down", "j":
		if m.menuCursor < len(newMenuItems)-1 {
			m.menuCursor++
		}
	case "enter", "1", "2", "3":
		choice := m.menuCursor
		if k.String() == "1" {
			choice = 0
		} else if k.String() == "2" {
			choice = 1
		} else if k.String() == "3" {
			choice = 2
		}
		switch choice {
		case 0: // create new
			m.mode = modeNewDirect
			m.input.SetValue("")
			m.input.Placeholder = "new project name…"
			m.input.Focus()
		case 1: // pick existing
			m.mode = modeNewPick
			m.input.SetValue("")
			m.input.Placeholder = "filter branches…"
			m.input.Focus()
			m.filtered = m.branches
			m.listCursor = 0
		case 2: // fork
			m.mode = modeNewForkBase
			m.input.SetValue("")
			m.input.Placeholder = "filter branches…"
			m.input.Focus()
			m.filtered = m.branches
			m.listCursor = 0
		}
	}
	return m, nil
}

func (m model) updateNewDirect(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNewMenu
		m.input.Blur()
		return m, nil
	case "enter":
		branch := strings.TrimSpace(m.input.Value())
		if branch == "" {
			return m, nil
		}
		m.mode = modeNormal
		m.input.Blur()
		m.fetching = true
		return m, tea.Batch(m.createWorkerCmd(branch), m.spinner.Tick)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

func (m model) updateNewPick(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNewMenu
		m.input.Blur()
		return m, nil
	case "enter":
		var branch string
		if m.listCursor < len(m.filtered) {
			branch = m.filtered[m.listCursor]
		}
		if branch == "" {
			return m, nil
		}
		m.mode = modeNormal
		m.input.Blur()
		m.fetching = true
		return m, tea.Batch(m.createWorkerCmd(branch), m.spinner.Tick)
	case "up", "ctrl+p":
		if m.listCursor > 0 {
			m.listCursor--
		}
		return m, nil
	case "down", "ctrl+n":
		if m.listCursor < len(m.filtered)-1 {
			m.listCursor++
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	m.filtered = filterBranches(m.branches, m.input.Value())
	if m.listCursor >= len(m.filtered) && len(m.filtered) > 0 {
		m.listCursor = len(m.filtered) - 1
	}
	return m, cmd
}

func (m model) updateNewForkBase(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNewMenu
		m.input.Blur()
		return m, nil
	case "enter":
		if m.listCursor < len(m.filtered) {
			m.forkBase = m.filtered[m.listCursor]
			m.mode = modeNewForkName
			m.input.SetValue("")
			m.input.Placeholder = "new project name…"
		}
		return m, nil
	case "up", "ctrl+p":
		if m.listCursor > 0 {
			m.listCursor--
		}
		return m, nil
	case "down", "ctrl+n":
		if m.listCursor < len(m.filtered)-1 {
			m.listCursor++
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	m.filtered = filterBranches(m.branches, m.input.Value())
	if m.listCursor >= len(m.filtered) && len(m.filtered) > 0 {
		m.listCursor = len(m.filtered) - 1
	}
	return m, cmd
}

func (m model) updateNewForkName(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNewForkBase
		m.input.SetValue("")
		m.input.Placeholder = "filter branches…"
		m.filtered = m.branches
		m.listCursor = 0
		return m, nil
	case "enter":
		branch := strings.TrimSpace(m.input.Value())
		if branch == "" {
			return m, nil
		}
		base := m.forkBase
		m.forkBase = ""
		m.mode = modeNormal
		m.input.Blur()
		m.fetching = true
		return m, tea.Batch(m.createWorkerCmdWithBase(branch, base), m.spinner.Tick)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

func filterBranches(branches []string, q string) []string {
	q = strings.ToLower(q)
	var out []string
	for _, b := range branches {
		if strings.Contains(strings.ToLower(b), q) {
			out = append(out, b)
		}
	}
	return out
}

func (m model) updateDelete(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "Y":
		if m.actionWorker == nil {
			m.mode = modeNormal
			return m, nil
		}
		w := *m.actionWorker
		m.actionWorker = nil
		m.mode = modeNormal
		if m.cursor > 0 && m.cursor >= len(m.state.Workers)-1 {
			m.cursor--
		}
		return m, m.deleteWorkerCmd(w)
	default:
		m.mode = modeNormal
		m.actionWorker = nil
	}
	return m, nil
}

// ── Commands ──────────────────────────────────────────────────────────────────

func restoreAllWorkersCmd(state *State) tea.Cmd {
	return func() tea.Msg {
		changed := false
		for i := range state.Workers {
			w := &state.Workers[i]
			// Mark as error if worktree no longer exists on disk.
			if _, err := os.Stat(w.Worktree); os.IsNotExist(err) {
				if w.Status != "error" {
					w.Status = "error"
					changed = true
				}
				continue
			}
			if tmuxHasSession(w.Session) {
				continue
			}
			session := makeSessionName(w.Branch)
			if err := tmuxNewSession(session, w.Branch, w.Worktree); err != nil {
				w.Status = "error"
				changed = true
				continue
			}
			claudeCmd := fmt.Sprintf("claude --name %q", w.Branch)
			if w.SessionStarted {
				claudeCmd = fmt.Sprintf("claude --resume %q", w.Branch)
			}
			_ = tmuxSendKeys(session, claudeCmd)
			w.SessionStarted = true
			w.Status = "waiting"
			w.Session = session
			changed = true
		}
		if changed {
			_ = saveState(state)
		}
		return nil
	}
}

func (m *model) pruneAndRetryCmd(branch, base string) tea.Cmd {
	repoRoot, state := m.repoRoot, m.state
	return func() tea.Msg {
		if err := gitPruneWorktrees(repoRoot); err != nil {
			return errMsg{err}
		}
		worktreePath := filepath.Join(repoRoot, ".tulip", "worktrees", branch)
		if base != "" {
			baseRef := "origin/" + base
			if gitBranchExistsLocally(repoRoot, base) {
				baseRef = base
			}
			if err := gitCreateWorktreeFromBase(repoRoot, branch, worktreePath, baseRef); err != nil {
				return errMsg{err}
			}
		} else {
			if err := gitCreateWorktree(repoRoot, branch, worktreePath); err != nil {
				var exists BranchExistsError
				if errors.As(err, &exists) {
					return branchExistsMsg{branch: branch}
				}
				return errMsg{err}
			}
		}
		return startSession(state, branch, worktreePath)
	}
}

func (m model) updateStaleWorktree(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "Y":
		branch, base := m.staleBranch, m.staleBase
		m.staleBranch, m.staleBase = "", ""
		m.mode = modeNormal
		return m, m.pruneAndRetryCmd(branch, base)
	default:
		m.staleBranch, m.staleBase = "", ""
		m.mode = modeNormal
	}
	return m, nil
}

func (m model) updateBranchExists(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	branch := m.branchExistsBranch
	switch k.String() {
	case "y", "Y":
		m.branchExistsBranch = ""
		m.mode = modeNormal
		m.fetching = true
		return m, tea.Batch(m.createWorkerCmd(branch), m.spinner.Tick)
	default:
		m.branchExistsBranch = ""
		m.mode = modeNormal
	}
	return m, nil
}

func (m model) updatePublish(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter":
		msg := strings.TrimSpace(m.input.Value())
		if msg == "" {
			m.mode = modeNormal
			m.publishBranch = ""
			return m, nil
		}
		branch := m.publishBranch
		m.publishBranch = ""
		m.mode = modeNormal
		m.input.Blur()
		return m, m.publishCmd(branch, msg)
	case "esc":
		m.mode = modeNormal
		m.publishBranch = ""
		m.input.Blur()
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(k)
		return m, cmd
	}
	return m, nil
}

func (m *model) publishCmd(branch, message string) tea.Cmd {
	return func() tea.Msg {
		w := findWorker(m.state, branch)
		if w == nil {
			return publishDoneMsg{branch: branch, err: fmt.Errorf("no project found for %q", branch)}
		}
		if err := gitStageAndCommit(w.Worktree, message); err != nil {
			return publishDoneMsg{branch: branch, err: err}
		}
		if err := gitPush(w.Worktree, w.Branch); err != nil {
			return publishDoneMsg{branch: branch, err: err}
		}
		return publishDoneMsg{branch: branch}
	}
}

func (m model) pickedActions() []struct{ name, desc string } {
	actions := pickActions[:]
	if m.pickedWorker != nil && m.pickedWorker.Status == "dirty" {
		actions = append(actions, struct{ name, desc string }{"difit", "open difit for this worktree"})
	}
	if m.pickedWorker != nil && m.pickedWorker.PRURL != "" {
		actions = append(actions, struct{ name, desc string }{"open-pr", "open pull request in browser"})
	}
	return actions
}

func (m model) updatePick(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q":
		m.mode = modeNormal
		m.pickedWorker = nil
	case "up", "k":
		if m.pickCursor > 0 {
			m.pickCursor--
		}
	case "down", "j":
		if m.pickCursor < len(m.pickedActions())-1 {
			m.pickCursor++
		}
	case "enter":
		branch := m.pickedWorker.Branch
		switch m.pickCursor {
		case 0: // claude
			m.pickedAction = 0
			return m, tea.Quit
		case 1: // shell
			m.pickedAction = 1
			return m, tea.Quit
		case 3: // graft-debug — only quit if the watch window exists
			wk := m.pickedWorker
			if !tmuxHasWindow(wk.Session, "watch/"+branch) {
				m.mode = modeNormal
				m.pickedWorker = nil
				m.notif = "✗  " + branch + " is not being grafted"
				m.notifIsErr = true
				m.notifTick = 6
				return m, nil
			}
			m.pickedAction = 3
			return m, tea.Quit
		case 2: // graft — run inline
			m.mode = modeNormal
			m.pickedWorker = nil
			m.grafting = true
			return m, tea.Batch(m.graftCmd(branch), m.spinner.Tick)
		case 4: // vscode — fire and forget
			m.mode = modeNormal
			m.pickedWorker = nil
			go func() { _ = cmdVSCode(branch) }()
		case 5: // publish — collect commit message inline
			m.publishBranch = branch
			m.pickedWorker = nil
			m.input.Placeholder = "commit message…"
			m.input.SetValue("")
			m.input.Focus()
			m.mode = modePublish
			return m, textinput.Blink
		case 6: // difit — only shown when dirty
			m.mode = modeNormal
			m.pickedWorker = nil
			if _, err := exec.LookPath("difit"); err != nil {
				m.notif = "✗  difit not installed — https://github.com/yoshiko-pg/difit"
				m.notifIsErr = true
				m.notifTick = 10
				return m, nil
			}
			go func() { _ = cmdDiffit(branch) }()
		case 7: // open-pr — only shown when PR exists
			m.mode = modeNormal
			url := m.pickedWorker.PRURL
			m.pickedWorker = nil
			go func() { _ = openInBrowser(url) }()
		}
	}
	return m, nil
}

func (m *model) graftCmd(branch string) tea.Cmd {
	state, repoRoot := m.state, m.repoRoot
	return func() tea.Msg {
		var target *Worker
		for i := range state.Workers {
			if state.Workers[i].Branch == branch {
				target = &state.Workers[i]
				break
			}
		}
		if target == nil {
			return graftDoneMsg{branch: branch, err: fmt.Errorf("no project found for %q", branch)}
		}
		if !tmuxHasSession(target.Session) {
			return graftDoneMsg{branch: branch, err: fmt.Errorf("%q is not running — start it first", branch)}
		}
		for _, w := range state.Workers {
			winName := "watch/" + w.Branch
			if tmuxHasWindow(w.Session, winName) {
				tmuxKillWindow(w.Session, winName)
			}
		}
		if err := graftSymlinkDist(repoRoot, target.Worktree); err != nil {
			return graftDoneMsg{branch: branch, err: fmt.Errorf("could not symlink dist: %w", err)}
		}
		winName := "watch/" + branch
		if err := tmuxNewWindow(target.Session, winName, target.Worktree, "yarn install && yarn run watch"); err != nil {
			return graftDoneMsg{branch: branch, err: fmt.Errorf("could not start graft: %w", err)}
		}
		tmuxSetWindowOption(target.Session, winName, "remain-on-exit", "on")
		return graftDoneMsg{branch: branch}
	}
}

func (m *model) createWorkerCmd(branch string) tea.Cmd {
	repoRoot, state := m.repoRoot, m.state
	return func() tea.Msg {
		if findWorker(state, branch) != nil {
			return errMsg{fmt.Errorf("project %q already exists", branch)}
		}
		_ = gitFetch(repoRoot)
		return fetchDoneMsg{branch: branch}
	}
}

func (m *model) createWorkerCmdWithBase(branch, base string) tea.Cmd {
	repoRoot, state := m.repoRoot, m.state
	return func() tea.Msg {
		if findWorker(state, branch) != nil {
			return errMsg{fmt.Errorf("project %q already exists", branch)}
		}
		_ = gitFetch(repoRoot)
		return fetchDoneMsg{branch: branch, base: base}
	}
}

func (m *model) createWorkerAfterFetchCmd(branch, base string) tea.Cmd {
	repoRoot, state := m.repoRoot, m.state
	return func() tea.Msg {
		worktreePath := filepath.Join(repoRoot, ".tulip", "worktrees", branch)
		gitEnsureExclude(repoRoot)

		var err error
		if base != "" {
			baseRef := "origin/" + base
			if gitBranchExistsLocally(repoRoot, base) {
				baseRef = base
			}
			err = gitCreateWorktreeFromBase(repoRoot, branch, worktreePath, baseRef)
			if err != nil {
				var stale StaleWorktreeError
				if errors.As(err, &stale) {
					return staleWorktreeMsg{branch: branch, base: base}
				}
				var exists BranchExistsError
				if errors.As(err, &exists) {
					return branchExistsMsg{branch: branch}
				}
				return errMsg{err}
			}
		} else if gitBranchExistsLocally(repoRoot, branch) {
			err = gitCreateWorktree(repoRoot, branch, worktreePath)
			if err != nil {
				var stale StaleWorktreeError
				if errors.As(err, &stale) {
					return staleWorktreeMsg{branch: branch}
				}
				return errMsg{err}
			}
		} else if gitBranchExistsRemotely(repoRoot, branch) {
			err = gitCreateWorktreeFromBase(repoRoot, branch, worktreePath, "origin/"+branch)
			if err != nil {
				var stale StaleWorktreeError
				if errors.As(err, &stale) {
					return staleWorktreeMsg{branch: branch}
				}
				var exists BranchExistsError
				if errors.As(err, &exists) {
					return branchExistsMsg{branch: branch}
				}
				return errMsg{err}
			}
		} else {
			defaultBase := gitDefaultRemoteBranch(repoRoot)
			err = gitCreateWorktreeFromBase(repoRoot, branch, worktreePath, defaultBase)
			if err != nil {
				var stale StaleWorktreeError
				if errors.As(err, &stale) {
					return staleWorktreeMsg{branch: branch}
				}
				return errMsg{err}
			}
		}

		return startSession(state, branch, worktreePath)
	}
}

func (m *model) deleteWorkerCmd(w Worker) tea.Cmd {
	repoRoot, state := m.repoRoot, m.state
	return func() tea.Msg {
		_ = tmuxKillSession(w.Session)
		_ = gitRemoveWorktree(repoRoot, w.Worktree)
		archiveWorker(state, w.ID)
		if err := saveState(state); err != nil {
			return errMsg{err}
		}
		return workerDeletedMsg{branch: w.Branch}
	}
}

// startSession creates or restarts a tmux session for a worker and sends the claude command.
func startSession(state *State, branch, worktreePath string) tea.Msg {
	session := makeSessionName(branch)
	if tmuxHasSession(session) {
		_ = tmuxKillSession(session)
	}
	if err := tmuxNewSession(session, branch, worktreePath); err != nil {
		return errMsg{err}
	}

	w := findWorker(state, branch)
	if w == nil {
		w = addWorker(state, branch, worktreePath)
	}

	claudeCmd := fmt.Sprintf("claude --name %q", branch)
	if w.SessionStarted {
		claudeCmd = fmt.Sprintf("claude --resume %q", branch)
	}
	if err := tmuxSendKeys(session, claudeCmd); err != nil {
		return errMsg{err}
	}

	w.SessionStarted = true
	w.Status = "waiting"
	w.Session = session
	if err := saveState(state); err != nil {
		return errMsg{err}
	}
	return workerCreatedMsg{branch: branch}
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m model) View() string {
	switch m.mode {
	case modeNewMenu, modeNewDirect, modeNewPick, modeNewForkBase, modeNewForkName:
		return m.modalNewFlow()
	case modeDelete:
		return m.modalDelete()
	case modeStaleWorktree:
		return m.modalStaleWorktree()
	case modeBranchExists:
		return m.modalBranchExists()
	case modePick:
		return m.viewPick()
	case modePublish:
		return m.modalPublish()
	case modeHelp:
		return m.viewHelp()
	case modeHistory:
		return m.viewHistory()
	}
	return m.viewMain()
}

const maxWorkersVisible = 8

// syncWorkersScroll adjusts workersScroll to keep the cursor visible.
func (m *model) syncWorkersScroll() {
	if m.cursor < m.workersScroll {
		m.workersScroll = m.cursor
	}
	if m.cursor >= m.workersScroll+maxWorkersVisible {
		m.workersScroll = m.cursor - maxWorkersVisible + 1
	}
	if m.workersScroll < 0 {
		m.workersScroll = 0
	}
}

func prBadge(number int, state, url string) string {
	label := fmt.Sprintf("PR #%d", number)
	var styled string
	switch state {
	case "OPEN":
		styled = sGreen.Render(label)
	case "DRAFT":
		styled = sGrey.Render(label)
	case "MERGED":
		styled = sPurple.Render(label)
	case "CLOSED":
		styled = sRed.Render(label)
	default:
		styled = sDim.Render(label)
	}
	if url != "" {
		styled = "\x1b]8;;" + url + "\x1b\\" + styled + "\x1b]8;;\x1b\\"
	}
	return styled
}

func (m model) pageHeader() string {
	h := sTitle.Render("🌷 tulip") + " " + sDim.Render("— a nicer way to work with Claude Code")
	if m.grafting {
		h += "  " + m.spinner.View() + sDim.Render(" grafting…")
	}
	if m.fetching {
		h += "  " + m.spinner.View() + sDim.Render(" fetching…")
	}
	if m.prLoading > 0 {
		h += "  " + m.spinner.View() + sDim.Render(" loading PRs…")
	}
	if m.notif != "" {
		var ns string
		if m.notifIsErr {
			ns = sRed.Render(m.notif)
		} else {
			ns = sGreen.Render(m.notif)
		}
		h += "  " + ns
	}
	return h
}

func (m model) viewMain() string {
	colB := 0
	maxID := 0
	for _, w := range m.state.Workers {
		if len(w.Branch) > colB {
			colB = len(w.Branch)
		}
		if w.ID > maxID {
			maxID = w.ID
		}
	}
	if colB > 26 {
		colB = 26
	}
	colID := len(fmt.Sprintf("%d", maxID))

	var lines []string
	lines = append(lines, m.pageHeader())
	lines = append(lines, "")

	total := len(m.state.Workers)
	if total == 0 {
		lines = append(lines, sGrey.Render("  no projects"))
	} else {
		end := m.workersScroll + maxWorkersVisible
		if end > total {
			end = total
		}
		for i := m.workersScroll; i < end; i++ {
			wk := m.state.Workers[i]
			branch := wk.Branch
			if len(branch) > colB {
				branch = branch[:colB-1] + "…"
			}
			var dot string
			switch wk.Status {
			case "dirty":
				dot = sGreen.Render("●")
			case "error":
				dot = sRed.Render("●")
			default: // clean
				dot = sGrey.Render("●")
			}
			branch = fmt.Sprintf("%-*s", colB, branch)
			num := sDim.Render(fmt.Sprintf("%*d", colID, wk.ID))
			row := num + "  " + dot + " " + branch
			if wk.PRNumber > 0 {
				row += "  " + prBadge(wk.PRNumber, wk.PRState, wk.PRURL)
			} else {
				row += "  " + sDim.Render("PR: -")
			}
			switch wk.GraftStatus {
			case "active":
				row += " " + sGreen.Render("🐝")
			case "failed":
				row += " " + sRed.Render("🐝 failed") + " " + sDim.Render("(see graft-debug)")
			}
			if i == m.cursor {
				lines = append(lines, "  "+sCyan.Render("▶")+" "+row)
			} else {
				lines = append(lines, "    "+row)
			}
		}
		if total > maxWorkersVisible {
			lines = append(lines, sGrey.Render(fmt.Sprintf("  … %d more", total-maxWorkersVisible)))
		}
	}

	lines = append(lines, "")
	footer := "  " + sKey.Render("n") + " " + sDim.Render("new project")
	if total > 0 {
		footer += "   " + sKey.Render("d") + " " + sDim.Render("delete project")
	}
	if len(m.state.DeletedWorkers) > 0 {
		footer += "   " + sKey.Render("h") + " " + sDim.Render("history")
	}
	footer += "   " + sKey.Render("q") + " " + sDim.Render("quit")
	footer += "   " + sKey.Render("?") + " " + sDim.Render("help")
	lines = append(lines, footer)
	if total > 0 && m.cursor < total {
		wk := m.state.Workers[m.cursor]
		lines = append(lines, "")
		lines = append(lines, "  "+sDim.Render("to interact press")+" "+sCyan.Render("↵ enter")+" "+sDim.Render("or run")+" "+sCommand.Render(fmt.Sprintf("tulip %s", wk.Branch)))
	}

	return strings.Join(lines, "\n")
}

func (m model) updateHelp(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q", "?":
		m.mode = modeNormal
	}
	return m, nil
}

func (m model) viewHelp() string {
	sTitle2 := lipgloss.NewStyle().Bold(true).Foreground(cCyan)
	var lines []string
	lines = append(lines, "")
	lines = append(lines, "  "+sTitle2.Render("tulip — help"))
	lines = append(lines, "")
	lines = append(lines, "  "+sBold.Render("status dots"))
	lines = append(lines, "")
	lines = append(lines, "  "+sGrey.Render("●")+"  "+sDim.Render("no uncommitted changes"))
	lines = append(lines, "  "+sGreen.Render("●")+"  "+sDim.Render("uncommitted changes in worktree"))
	lines = append(lines, "  "+sRed.Render("●")+"  "+sDim.Render("worktree error"))
	lines = append(lines, "")
	lines = append(lines, "  "+sBold.Render("graft status"))
	lines = append(lines, "")
	lines = append(lines, "  "+sGreen.Render("🐝")+"  "+sDim.Render("graft watch is running"))
	lines = append(lines, "  "+sRed.Render("graft: failed")+"   "+sDim.Render("graft watch exited unexpectedly"))
	lines = append(lines, "")
	lines = append(lines, "  "+sKey.Render("esc")+" "+sDim.Render("to go back"))
	return strings.Join(lines, "\n")
}

func (m model) updateHistory(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	deleted := m.state.DeletedWorkers
	switch k.String() {
	case "esc", "q", "h":
		m.mode = modeNormal
	case "up", "k":
		if m.historyCursor > 0 {
			m.historyCursor--
			if m.historyCursor < m.historyScroll {
				m.historyScroll = m.historyCursor
			}
		}
	case "down", "j":
		if m.historyCursor < len(deleted)-1 {
			m.historyCursor++
			if m.historyCursor >= m.historyScroll+maxWorkersVisible {
				m.historyScroll = m.historyCursor - maxWorkersVisible + 1
			}
		}
	}
	return m, nil
}

func (m model) viewHistory() string {
	deleted := m.state.DeletedWorkers
	var lines []string
	lines = append(lines, m.pageHeader())
	lines = append(lines, "")
	lines = append(lines, sHeader.Render("  History"))
	lines = append(lines, "")

	colB := 0
	for _, w := range deleted {
		if len(w.Branch) > colB {
			colB = len(w.Branch)
		}
	}
	if colB > 26 {
		colB = 26
	}

	end := m.historyScroll + maxWorkersVisible
	if end > len(deleted) {
		end = len(deleted)
	}
	for i := m.historyScroll; i < end; i++ {
		w := deleted[i]
		branch := w.Branch
		if len(branch) > colB {
			branch = branch[:colB-1] + "…"
		}
		branch = fmt.Sprintf("%-*s", colB, branch)
		row := sDim.Render(w.DeletedAt) + "  " + branch
		if w.PRNumber > 0 {
			row += "  " + prBadge(w.PRNumber, w.PRState, w.PRURL)
		} else {
			row += "  " + sDim.Render("PR: -")
		}
		if i == m.historyCursor {
			lines = append(lines, "  "+sCyan.Render("▶")+" "+row)
		} else {
			lines = append(lines, "    "+row)
		}
	}
	if len(deleted) > maxWorkersVisible {
		lines = append(lines, sGrey.Render(fmt.Sprintf("  … %d more", len(deleted)-maxWorkersVisible)))
	}

	lines = append(lines, "")
	lines = append(lines, "  "+sKey.Render("esc")+" "+sDim.Render("to go back"))
	return strings.Join(lines, "\n")
}

func (m model) viewPick() string {
	if m.pickedWorker == nil {
		return ""
	}
	var lines []string
	lines = append(lines, m.pageHeader())
	lines = append(lines, "")
	lines = append(lines, sHeader.Render("  "+m.pickedWorker.Branch))
	lines = append(lines, "")
	actions := m.pickedActions()
	nameWidth := 0
	for _, a := range actions {
		if len(a.name) > nameWidth {
			nameWidth = len(a.name)
		}
	}
	for i, a := range actions {
		name := fmt.Sprintf("%-*s", nameWidth, a.name)
		if i == m.pickCursor {
			lines = append(lines, "  "+sCyan.Render("▶")+" "+sBold.Render(name)+"  "+sDim.Render(a.desc))
		} else {
			lines = append(lines, "    "+sGrey.Render(name)+"  "+sDim.Render(a.desc))
		}
	}
	lines = append(lines, "")
	action := actions[m.pickCursor].name
	branch := m.pickedWorker.Branch
	lines = append(lines, "  "+
		sDim.Render("to run press")+" "+sKey.Render("↵ enter")+" "+
		sDim.Render("or")+" "+sCommand.Render(fmt.Sprintf("tulip %s %s", action, branch)))
	lines = append(lines, "")
	lines = append(lines, "  "+sKey.Render("esc")+" "+sDim.Render("to go back"))
	return strings.Join(lines, "\n")
}

// ── Forms ─────────────────────────────────────────────────────────────────────

func (m model) modalNewFlow() string {
	navHints := "  " +
		sKey.Render("enter") + " " + sDim.Render("select") + "   " +
		sKey.Render("esc") + " " + sDim.Render("back")

	var lines []string
	lines = append(lines, m.pageHeader())
	lines = append(lines, "")

	switch m.mode {
	case modeNewMenu:
		lines = append(lines, sHeader.Render("  New project"))
		lines = append(lines, "")
		for i, item := range newMenuItems {
			label := sBold.Render(item.label)
			desc := "  " + sDim.Render(item.desc)
			if i == m.menuCursor {
				lines = append(lines, " "+sCyan.Render("▶")+" "+sGrey.Render(fmt.Sprintf("%d. ", i+1))+label)
			} else {
				lines = append(lines, "   "+sGrey.Render(fmt.Sprintf("%d. ", i+1))+label)
			}
			lines = append(lines, desc)
		}
		lines = append(lines, "")
		lines = append(lines, navHints)

	case modeNewDirect:
		lines = append(lines, sHeader.Render("  Create new project"))
		lines = append(lines, "")
		lines = append(lines, "  "+sCyan.Render("> ")+m.input.View())
		lines = append(lines, "")
		lines = append(lines, "  "+
			sKey.Render("enter")+" "+sDim.Render("create")+"   "+
			sKey.Render("esc")+" "+sDim.Render("back"))

	case modeNewPick:
		lines = append(lines, sHeader.Render("  Pick existing branch"))
		lines = append(lines, "")
		lines = append(lines, "  "+sCyan.Render("> ")+m.input.View())
		lines = append(lines, m.branchListLines()...)
		lines = append(lines, "")
		lines = append(lines, navHints)

	case modeNewForkBase:
		lines = append(lines, sHeader.Render("  Fork — pick base branch"))
		lines = append(lines, "")
		lines = append(lines, "  "+sCyan.Render("> ")+m.input.View())
		lines = append(lines, m.branchListLines()...)
		lines = append(lines, "")
		lines = append(lines, navHints)

	case modeNewForkName:
		lines = append(lines, sHeader.Render("  Fork  "+sCyan.Render(m.forkBase)))
		lines = append(lines, "")
		lines = append(lines, "  "+sCyan.Render("> ")+m.input.View())
		lines = append(lines, "")
		lines = append(lines, "  "+
			sKey.Render("enter")+" "+sDim.Render("create")+"   "+
			sKey.Render("esc")+" "+sDim.Render("back"))
	}

	return strings.Join(lines, "\n")
}

func (m model) branchListLines() []string {
	limit := 8
	if len(m.filtered) < limit {
		limit = len(m.filtered)
	}
	if limit == 0 {
		if len(m.branches) > 0 {
			return []string{sGrey.Render("  no matches")}
		}
		return nil
	}
	lines := []string{sDim.Render("  recently active:")}
	for i := 0; i < limit; i++ {
		b := m.filtered[i]
		if i == m.listCursor {
			lines = append(lines, sCyan.Render("▶ ")+sBold.Render(b))
		} else {
			lines = append(lines, "  "+sGrey.Render(b))
		}
	}
	return lines
}

func (m model) modalDelete() string {
	if m.actionWorker == nil {
		return ""
	}
	var lines []string
	lines = append(lines, m.pageHeader())
	lines = append(lines, "")
	lines = append(lines, sHeader.Render("  Delete  "+sCyan.Render(m.actionWorker.Branch)))
	lines = append(lines, "")
	lines = append(lines, sGrey.Render("  Kills the session and removes the worktree."))
	lines = append(lines, "")
	lines = append(lines, "  "+
		sKey.Render("y")+" "+sDim.Render("confirm")+"   "+
		sKey.Render("esc")+" "+sDim.Render("cancel"))
	return strings.Join(lines, "\n")
}

func (m model) modalStaleWorktree() string {
	var lines []string
	lines = append(lines, m.pageHeader())
	lines = append(lines, "")
	lines = append(lines, sHeader.Render("  Stale worktree  "+sCyan.Render(m.staleBranch)))
	lines = append(lines, "")
	lines = append(lines, sGrey.Render("  Git has a stale entry for this branch — the"))
	lines = append(lines, sGrey.Render("  worktree path no longer exists on disk."))
	lines = append(lines, "")
	lines = append(lines, "  Prune stale entries and retry?")
	lines = append(lines, "")
	lines = append(lines, "  "+
		sKey.Render("y")+" "+sDim.Render("prune & retry")+"   "+
		sKey.Render("esc")+" "+sDim.Render("cancel"))
	return strings.Join(lines, "\n")
}

func (m model) modalBranchExists() string {
	var lines []string
	lines = append(lines, m.pageHeader())
	lines = append(lines, "")
	lines = append(lines, sHeader.Render("  Branch already exists  "+sCyan.Render(m.branchExistsBranch)))
	lines = append(lines, "")
	lines = append(lines, sGrey.Render("  A branch named ")+sCyan.Render(m.branchExistsBranch)+sGrey.Render(" already exists locally."))
	lines = append(lines, "")
	lines = append(lines, "  Start a project from it?")
	lines = append(lines, "")
	lines = append(lines, "  "+
		sKey.Render("y")+" "+sDim.Render("yes, start from there")+"   "+
		sKey.Render("esc")+" "+sDim.Render("cancel"))
	return strings.Join(lines, "\n")
}

func (m model) modalPublish() string {
	var lines []string
	lines = append(lines, m.pageHeader())
	lines = append(lines, "")
	lines = append(lines, sHeader.Render("  Publish  "+sCyan.Render(m.publishBranch)))
	lines = append(lines, "")
	lines = append(lines, "  "+sCyan.Render("> ")+m.input.View())
	lines = append(lines, "")
	lines = append(lines, "  "+
		sKey.Render("enter")+" "+sDim.Render("commit & push")+"   "+
		sKey.Render("esc")+" "+sDim.Render("cancel"))
	return strings.Join(lines, "\n")
}

// ── Entry ─────────────────────────────────────────────────────────────────────

// runTUI runs the TUI and returns the final model so the caller can act on any
// picked action after the screen is restored.
func runTUI(s *State, repoRoot, resumeBranch string) (model, error) {
	m := newModel(s, repoRoot)
	if resumeBranch != "" {
		if w := findWorker(s, resumeBranch); w != nil {
			m.pickedWorker = w
			m.pickedAction = -1
			m.mode = modePick
		}
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return model{}, err
	}
	fm, _ := final.(model)
	_ = saveState(fm.state)
	return fm, nil
}
