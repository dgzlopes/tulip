package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitIsDirty returns true if the worktree has uncommitted changes.
func gitIsDirty(worktree string) bool {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktree
	out, err := cmd.Output()
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// gitListRecentBranches returns recently-active remote branches, sorted by committer date.
// Returns an empty slice on error. HEAD is stripped from results.
func gitListRecentBranches(repoRoot string) []string {
	cmd := exec.Command(
		"git",
		"for-each-ref",
		"--sort=-committerdate",
		"--format=%(refname:lstrip=3)",
		"refs/remotes/origin/",
	)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return []string{}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var branches []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || l == "HEAD" {
			continue
		}
		branches = append(branches, l)
	}
	return branches
}

// gitBranchExistsLocally returns true if the given branch exists as a local ref.
func gitBranchExistsLocally(repoRoot, branch string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

// BranchExistsError is returned when trying to create a new branch that already exists locally.
type BranchExistsError struct{ Branch string }

func (e BranchExistsError) Error() string {
	return fmt.Sprintf("branch %q already exists", e.Branch)
}

// StaleWorktreeError is returned when a branch is "already checked out" at a
// path that no longer exists on disk — the worktree entry is stale.
type StaleWorktreeError struct{ Branch string }

func (e StaleWorktreeError) Error() string {
	return fmt.Sprintf("stale worktree entry for %q — prune and retry?", e.Branch)
}

// stalePath extracts the conflicting path from a "already checked out at '/path'"
// error message and returns it if that path does not exist on disk.
func stalePath(errOutput []byte) string {
	s := string(errOutput)
	const marker = "already checked out at '"
	idx := strings.Index(s, marker)
	if idx == -1 {
		return ""
	}
	rest := s[idx+len(marker):]
	end := strings.IndexByte(rest, '\'')
	if end == -1 {
		return ""
	}
	p := rest[:end]
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	return ""
}

// gitCreateWorktree creates a git worktree at worktreePath for the given branch.
// If the branch exists locally it checks it out directly; otherwise it creates a new branch.
// Returns StaleWorktreeError when the branch is registered in a now-missing path.
func gitCreateWorktree(repoRoot, branch, worktreePath string) error {
	var args []string
	if gitBranchExistsLocally(repoRoot, branch) {
		args = []string{"worktree", "add", worktreePath, branch}
	} else {
		args = []string{"worktree", "add", "-b", branch, worktreePath}
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		if bytes.Contains(out, []byte("already checked out")) {
			if stalePath(out) != "" {
				return StaleWorktreeError{Branch: branch}
			}
			return fmt.Errorf("branch %q is already checked out in another worktree", branch)
		}
		return fmt.Errorf("git worktree add failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// gitPruneWorktrees removes stale worktree administrative files.

func gitPruneWorktrees(repoRoot string) error {
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree prune failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// gitCreateWorktreeFromBase creates a new branch off baseBranch and adds a worktree for it.
func gitCreateWorktreeFromBase(repoRoot, branch, worktreePath, baseBranch string) error {
	args := []string{"worktree", "add", "-b", branch, worktreePath, baseBranch}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		if bytes.Contains(out, []byte("already checked out")) {
			if stalePath(out) != "" {
				return StaleWorktreeError{Branch: branch}
			}
			return fmt.Errorf("branch %q is already checked out in another worktree", branch)
		}
		if bytes.Contains(out, []byte("already exists")) {
			return BranchExistsError{Branch: branch}
		}
		return fmt.Errorf("git worktree add failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// gitRemoveWorktree forcibly removes a git worktree at the given path.
func gitRemoveWorktree(repoRoot, worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// gitEnsureExclude adds .tulip/ and .claude/ to .git/info/exclude if they're not already there.
func gitEnsureExclude(repoRoot string) {
	excludePath := filepath.Join(repoRoot, ".git", "info", "exclude")
	data, err := os.ReadFile(excludePath)
	if err != nil {
		// if the file doesn't exist, try to create the directory and file
		_ = os.MkdirAll(filepath.Dir(excludePath), 0o755)
		data = []byte{}
	}

	content := string(data)
	var additions []string
	if !strings.Contains(content, ".tulip/") {
		additions = append(additions, ".tulip/")
	}
	if !strings.Contains(content, ".claude/") {
		additions = append(additions, ".claude/")
	}
	if !strings.Contains(content, "dist") {
		additions = append(additions, "dist")
	}
	if len(additions) == 0 {
		return
	}

	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	for _, a := range additions {
		content += a + "\n"
	}
	_ = os.WriteFile(excludePath, []byte(content), 0o644)
}

// graftSymlinkDist replaces <repoRoot>/dist with a symlink pointing at <worktree>/dist,
// so Graft always serves the active worktree's build output.
// If dist exists and is a real directory (not a symlink), it refuses to touch it.
func graftSymlinkDist(repoRoot, worktree string) error {
	dst := filepath.Join(repoRoot, "dist")
	src := filepath.Join(worktree, "dist")
	fi, err := os.Lstat(dst)
	if err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			if err := os.RemoveAll(dst); err != nil {
				return fmt.Errorf("could not remove %s: %w", dst, err)
			}
		} else {
			if err := os.Remove(dst); err != nil {
				return fmt.Errorf("could not remove existing symlink: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	gitEnsureExclude(repoRoot)
	return os.Symlink(src, dst)
}

// gitStageAndCommit stages all changes in the worktree and creates a signed commit with the given message.
func gitStageAndCommit(worktree, message string) error {
	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = worktree
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add failed: %s", strings.TrimSpace(string(out)))
	}

	commitCmd := exec.Command("git", "commit", "-sm", message)
	commitCmd.Dir = worktree
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// gitFetch fetches and prunes remote refs from origin.
func gitFetch(repoRoot string) error {
	cmd := exec.Command("git", "fetch", "--prune", "origin")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// gitPush pushes the given branch to origin, setting the upstream tracking ref.
func gitPush(worktree, branch string) error {
	cmd := exec.Command("git", "push", "-u", "origin", branch)
	cmd.Dir = worktree
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
