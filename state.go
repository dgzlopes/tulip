package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Worker represents a single Claude Code worker tied to a git worktree and tmux session.
type Worker struct {
	ID             int    `json:"id"`
	Branch         string `json:"branch"`
	Worktree       string `json:"worktree"`
	Session        string `json:"session"`
	Status         string `json:"status"`
	Grafting       bool   `json:"grafting,omitempty"`
	CreatedAt      string `json:"created_at"`
	SessionStarted bool   `json:"session_started"`
}

// State is the persistent state for garrison in a given repo.
type State struct {
	Repo    string   `json:"repo"`
	NextID  int      `json:"next_id"`
	Workers []Worker `json:"workers"`
}

// findRepoRoot walks up from cwd until it finds a directory containing .git.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("not inside a git repository")
		}
		dir = parent
	}
}

// statePath returns the path to the tulip state file for a given repo root.
func statePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".tulip", "state.json")
}

// loadState loads state from disk, returning an empty State if the file doesn't exist.
func loadState(repoRoot string) (*State, error) {
	path := statePath(repoRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{
				Repo:   repoRoot,
				NextID: 1,
			}, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// saveState writes state to disk, creating directories as needed.
func saveState(s *State) error {
	path := statePath(s.Repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// makeSessionName converts a branch name to a tmux-safe session name.
func makeSessionName(branch string) string {
	name := strings.ReplaceAll(branch, "/", "-")
	name = strings.ReplaceAll(name, ".", "-")
	name = strings.ReplaceAll(name, ":", "-")
	return "tulip-" + name
}

// addWorker creates a new Worker and appends it to the state, returning a pointer to it.
func addWorker(s *State, branch, worktree string) *Worker {
	w := Worker{
		ID:             s.NextID,
		Branch:         branch,
		Worktree:       worktree,
		Session:        makeSessionName(branch),
		Status:         "waiting",
		CreatedAt:      time.Now().Format("Jan 02 15:04"),
		SessionStarted: false,
	}
	s.NextID++
	s.Workers = append(s.Workers, w)
	return &s.Workers[len(s.Workers)-1]
}

// findWorker finds a worker by branch name or numeric ID string, returning nil if not found.
func findWorker(s *State, nameOrID string) *Worker {
	var id int
	if n, _ := fmt.Sscanf(nameOrID, "%d", &id); n == 1 {
		for i := range s.Workers {
			if s.Workers[i].ID == id {
				return &s.Workers[i]
			}
		}
	}
	for i := range s.Workers {
		if s.Workers[i].Branch == nameOrID {
			return &s.Workers[i]
		}
	}
	return nil
}

// removeWorker removes a worker from the state by ID.
func removeWorker(s *State, id int) {
	filtered := s.Workers[:0]
	for _, w := range s.Workers {
		if w.ID != id {
			filtered = append(filtered, w)
		}
	}
	s.Workers = filtered
}
