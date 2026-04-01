package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Colours ───────────────────────────────────────────────────────────────────

var (
	cCyan  = lipgloss.Color("6")
	cGreen = lipgloss.Color("2")
	cRed   = lipgloss.Color("1")
	cGrey  = lipgloss.Color("8")
)

// ── Base styles ───────────────────────────────────────────────────────────────

var (
	sCyan   = lipgloss.NewStyle().Foreground(cCyan)
	sGreen  = lipgloss.NewStyle().Foreground(cGreen)
	sRed    = lipgloss.NewStyle().Foreground(cRed)
	sGrey   = lipgloss.NewStyle().Foreground(cGrey)
	sBold   = lipgloss.NewStyle().Bold(true)
	sDim    = lipgloss.NewStyle().Faint(true)
	sKey    = lipgloss.NewStyle().Foreground(cCyan).Bold(true)
	sHeader = lipgloss.NewStyle().Bold(true).Foreground(cGrey)
)

// ── Mode ──────────────────────────────────────────────────────────────────────

type mode int

const (
	modeNormal         mode = iota
	modeNewMenu             // choose: create / pick / fork
	modeNewDirect           // type a new project name
	modeNewPick             // pick from existing branches
	modeNewForkBase         // pick base branch to fork from
	modeNewForkName         // type name for the new forked branch
	modeDelete
	modeStaleWorktree // confirm prune-and-retry
	modePick          // action picker for selected project
)

// ── Messages ──────────────────────────────────────────────────────────────────

type branchesLoadedMsg []string
type tickMsg struct{}
type workerCreatedMsg struct{ branch string }
type workerDeletedMsg struct{ branch string }
type errMsg struct{ err error }
type staleWorktreeMsg struct{ branch, base string }

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	state         *State
	repoRoot      string
	cursor        int
	mode          mode
	input         textinput.Model
	branches      []string
	filtered      []string
	listCursor    int
	actionWorker  *Worker
	notif         string
	notifIsErr    bool
	notifTick     int
	workersScroll int
	menuCursor    int
	forkBase      string
	staleBranch   string
	staleBase     string
	width         int
	pickCursor    int
	pickedWorker  *Worker
	pickedAction  int // -1 = none
}

func newModel(s *State, repoRoot string) model {
	ti := textinput.New()
	ti.Placeholder = "branch name or filter…"
	ti.CharLimit = 200
	ti.Width = 40
	return model{
		state:        s,
		repoRoot:     repoRoot,
		input:        ti,
		width:        80,
		pickedAction: -1,
	}
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return tea.Batch(loadBranchesCmd(m.repoRoot), tickCmd(), restoreAllWorkersCmd(m.state))
}

func loadBranchesCmd(repoRoot string) tea.Cmd {
	return func() tea.Msg { return branchesLoadedMsg(gitListRecentBranches(repoRoot)) }
}

func (m *model) addLog(_ string) {} // logs hidden

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(_ time.Time) tea.Msg { return tickMsg{} })
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
			if tmuxHasSession(w.Session) {
				if w.Status != "waiting" {
					w.Status = "waiting"
					changed = true
				}
			} else if w.Status == "waiting" {
				w.Status = "idle"
				changed = true
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

	case workerCreatedMsg:

	case workerDeletedMsg:
		text := "deleted " + msg.branch
		m.notif = "✓  " + text
		m.notifIsErr = false
		m.notifTick = 4
		(&m).addLog(sGreen.Render("✓") + "  " + text)

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
		case modePick:
			return m.updatePick(msg)
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
			w := m.state.Workers[m.cursor]
			m.pickedWorker = &w
			m.pickCursor = 0
			m.pickedAction = -1
			m.mode = modePick
		}
	case "n":
		m.mode = modeNewMenu
		m.menuCursor = 0
	case "d":
		if m.cursor < len(m.state.Workers) {
			w := m.state.Workers[m.cursor]
			m.actionWorker = &w
			m.mode = modeDelete
		}
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
		return m, m.createWorkerCmd(branch)
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
		branch := strings.TrimSpace(m.input.Value())
		if branch == "" && m.listCursor < len(m.filtered) {
			branch = m.filtered[m.listCursor]
		}
		if branch == "" {
			return m, nil
		}
		m.mode = modeNormal
		m.input.Blur()
		return m, m.createWorkerCmd(branch)
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
		return m, m.createWorkerCmdWithBase(branch, base)
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
			if err := tmuxNewSession(session, w.Worktree); err != nil {
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
			if err := gitCreateWorktreeFromBase(repoRoot, branch, worktreePath, base); err != nil {
				return errMsg{err}
			}
		} else {
			if err := gitCreateWorktree(repoRoot, branch, worktreePath); err != nil {
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
		if m.pickCursor < len(pickActions)-1 {
			m.pickCursor++
		}
	case "enter":
		m.pickedAction = m.pickCursor
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) createWorkerCmd(branch string) tea.Cmd {
	repoRoot, state := m.repoRoot, m.state
	return func() tea.Msg {
		worktreePath := filepath.Join(repoRoot, ".tulip", "worktrees", branch)
		gitEnsureExclude(repoRoot)

		if existing := findWorker(state, branch); existing != nil {
			if tmuxHasSession(existing.Session) {
				return workerCreatedMsg{branch: branch}
			}
		}

		if err := gitCreateWorktree(repoRoot, branch, worktreePath); err != nil {
			var stale StaleWorktreeError
			if errors.As(err, &stale) {
				return staleWorktreeMsg{branch: branch}
			}
			return errMsg{err}
		}

		return startSession(state, branch, worktreePath)
	}
}

func (m *model) deleteWorkerCmd(w Worker) tea.Cmd {
	repoRoot, state := m.repoRoot, m.state
	return func() tea.Msg {
		_ = tmuxKillSession(w.Session)
		_ = gitRemoveWorktree(repoRoot, w.Worktree)
		removeWorker(state, w.ID)
		if err := saveState(state); err != nil {
			return errMsg{err}
		}
		return workerDeletedMsg{branch: w.Branch}
	}
}

func (m *model) createWorkerCmdWithBase(branch, base string) tea.Cmd {
	repoRoot, state := m.repoRoot, m.state
	return func() tea.Msg {
		worktreePath := filepath.Join(repoRoot, ".tulip", "worktrees", branch)
		gitEnsureExclude(repoRoot)

		if err := gitCreateWorktreeFromBase(repoRoot, branch, worktreePath, base); err != nil {
			var stale StaleWorktreeError
			if errors.As(err, &stale) {
				return staleWorktreeMsg{branch: branch, base: base}
			}
			return errMsg{err}
		}

		return startSession(state, branch, worktreePath)
	}
}

// startSession creates or restarts a tmux session for a worker and sends the claude command.
func startSession(state *State, branch, worktreePath string) tea.Msg {
	session := makeSessionName(branch)
	if tmuxHasSession(session) {
		_ = tmuxKillSession(session)
	}
	if err := tmuxNewSession(session, worktreePath); err != nil {
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
	case modePick:
		return m.viewPick()
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

const divWidth = 58

func (m model) div() string {
	return sGrey.Render(strings.Repeat("─", divWidth))
}

func (m model) pageHeader() string {
	repoName := filepath.Base(m.repoRoot)
	h := sBold.Render("tulip") + "  " + sGrey.Render(repoName)
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
	colID, colB, colS := 4, 26, 10
	colHdr := fmt.Sprintf("  %-*s %-*s %-*s %s", colID, "ID", colB, "BRANCH", colS, "STATUS", "CREATED")

	var lines []string
	lines = append(lines, m.pageHeader())
	lines = append(lines, m.div())
	lines = append(lines, sHeader.Render(colHdr))
	lines = append(lines, m.div())

	total := len(m.state.Workers)
	if total == 0 {
		lines = append(lines, sGrey.Render("  no projects — press ")+sKey.Render("n")+" "+sGrey.Render("to create one"))
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
			var status string
			switch wk.Status {
			case "waiting":
				status = sGreen.Render("● active")
			case "error":
				status = sRed.Render("✗ error")
			default:
				status = sGrey.Render("○ idle")
			}
			idStr := fmt.Sprintf("%d", wk.ID)
			row := fmt.Sprintf("%-*s %-*s %-*s %s", colID, idStr, colB, branch, colS, status, sDim.Render(wk.CreatedAt))
			if i == m.cursor {
				lines = append(lines, sCyan.Render("▶")+" "+row)
			} else {
				lines = append(lines, "  "+row)
			}
		}
		if total > maxWorkersVisible {
			lines = append(lines, sGrey.Render(fmt.Sprintf("  … %d more", total-maxWorkersVisible)))
		}
	}

	lines = append(lines, m.div())
	lines = append(lines, "  "+
		sKey.Render("n")+" "+sDim.Render("new project")+"   "+
		sKey.Render("d")+" "+sDim.Render("delete")+"   "+
		sKey.Render("q")+" "+sDim.Render("quit"))
	if total > 0 && m.cursor < total {
		wk := m.state.Workers[m.cursor]
		lines = append(lines, "")
		lines = append(lines, "  "+
			sKey.Render("enter")+" "+sDim.Render("interact with this project")+"   "+
			sDim.Render("or run ")+sCyan.Render(fmt.Sprintf("tulip %d", wk.ID))+" "+sDim.Render("in another terminal"))
	}

	return strings.Join(lines, "\n")
}

func (m model) viewPick() string {
	if m.pickedWorker == nil {
		return ""
	}
	var lines []string
	lines = append(lines, m.pageHeader())
	lines = append(lines, sHeader.Render("  "+m.pickedWorker.Branch))
	lines = append(lines, m.div())
	for i, a := range pickActions {
		if i == m.pickCursor {
			lines = append(lines, "  "+sCyan.Render("▶")+" "+sBold.Render(a.name)+"  "+sDim.Render(a.desc))
		} else {
			lines = append(lines, "    "+sGrey.Render(a.name)+"  "+sDim.Render(a.desc))
		}
	}
	lines = append(lines, m.div())
	lines = append(lines, "  "+
		sKey.Render("↑↓")+" "+sDim.Render("navigate")+"   "+
		sKey.Render("enter")+" "+sDim.Render("select")+"   "+
		sKey.Render("esc")+" "+sDim.Render("back"))
	return strings.Join(lines, "\n")
}

// ── Forms ─────────────────────────────────────────────────────────────────────

func (m model) modalNewFlow() string {
	navHints := "  " +
		sKey.Render("↑↓") + " " + sDim.Render("navigate") + "   " +
		sKey.Render("enter") + " " + sDim.Render("select") + "   " +
		sKey.Render("esc") + " " + sDim.Render("back")

	var lines []string
	lines = append(lines, m.pageHeader())

	switch m.mode {
	case modeNewMenu:
		lines = append(lines, sHeader.Render("  New project"))
		lines = append(lines, m.div())
		for i, item := range newMenuItems {
			num := sGrey.Render(fmt.Sprintf("  %d. ", i+1))
			label := sBold.Render(item.label)
			desc := "     " + sDim.Render(item.desc)
			if i == m.menuCursor {
				lines = append(lines, sCyan.Render("▶")+num[1:]+label)
			} else {
				lines = append(lines, num+label)
			}
			lines = append(lines, desc)
		}
		lines = append(lines, m.div())
		lines = append(lines, navHints)

	case modeNewDirect:
		lines = append(lines, sHeader.Render("  Create new project"))
		lines = append(lines, m.div())
		lines = append(lines, "  "+sCyan.Render("> ")+m.input.View())
		lines = append(lines, m.div())
		lines = append(lines, "  "+
			sKey.Render("enter")+" "+sDim.Render("create")+"   "+
			sKey.Render("esc")+" "+sDim.Render("back"))

	case modeNewPick:
		lines = append(lines, sHeader.Render("  Pick existing branch"))
		lines = append(lines, m.div())
		lines = append(lines, "  "+sCyan.Render("> ")+m.input.View())
		lines = append(lines, m.branchListLines()...)
		lines = append(lines, m.div())
		lines = append(lines, navHints)

	case modeNewForkBase:
		lines = append(lines, sHeader.Render("  Fork — pick base branch"))
		lines = append(lines, m.div())
		lines = append(lines, "  "+sCyan.Render("> ")+m.input.View())
		lines = append(lines, m.branchListLines()...)
		lines = append(lines, m.div())
		lines = append(lines, navHints)

	case modeNewForkName:
		lines = append(lines, sHeader.Render("  Fork  "+sCyan.Render(m.forkBase)))
		lines = append(lines, m.div())
		lines = append(lines, "  "+sCyan.Render("> ")+m.input.View())
		lines = append(lines, m.div())
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
	lines = append(lines, sHeader.Render("  Delete  "+sCyan.Render(m.actionWorker.Branch)))
	lines = append(lines, m.div())
	lines = append(lines, sGrey.Render("  Kills the session and removes the worktree."))
	lines = append(lines, m.div())
	lines = append(lines, "  "+
		sKey.Render("y")+" "+sDim.Render("confirm")+"   "+
		sKey.Render("esc")+" "+sDim.Render("cancel"))
	return strings.Join(lines, "\n")
}



func (m model) modalStaleWorktree() string {
	var lines []string
	lines = append(lines, m.pageHeader())
	lines = append(lines, sHeader.Render("  Stale worktree  "+sCyan.Render(m.staleBranch)))
	lines = append(lines, m.div())
	lines = append(lines, sGrey.Render("  Git has a stale entry for this branch — the"))
	lines = append(lines, sGrey.Render("  worktree path no longer exists on disk."))
	lines = append(lines, "")
	lines = append(lines, "  Prune stale entries and retry?")
	lines = append(lines, m.div())
	lines = append(lines, "  "+
		sKey.Render("y")+" "+sDim.Render("prune & retry")+"   "+
		sKey.Render("esc")+" "+sDim.Render("cancel"))
	return strings.Join(lines, "\n")
}

// ── Entry ─────────────────────────────────────────────────────────────────────

// runTUI runs the TUI and returns the final model so the caller can act on any
// picked action after the screen is restored.
func runTUI(s *State, repoRoot string) (model, error) {
	p := tea.NewProgram(newModel(s, repoRoot), tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return model{}, err
	}
	fm, _ := final.(model)
	_ = saveState(fm.state)
	return fm, nil
}
