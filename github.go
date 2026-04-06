package main

import (
	"encoding/json"
	"os/exec"
	"strings"
)

// prInfo holds the PR fields we care about. State is "OPEN", "DRAFT", "MERGED", or "CLOSED".
type prInfo struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	IsDraft bool   `json:"isDraft"`
	URL     string `json:"url"`
}

// fetchPRForBranch uses the gh CLI to find an open PR for the given branch.
// Returns zero-value prInfo and no error if no PR is found or gh is unavailable.
func fetchPRForBranch(branch string) (prInfo, error) {
	out, err := exec.Command("gh", "pr", "list",
		"--head", branch,
		"--state", "all",
		"--limit", "1",
		"--json", "number,state,isDraft,url",
	).Output()
	if err != nil {
		return prInfo{}, nil // gh not available or no repo — silently skip
	}
	out = []byte(strings.TrimSpace(string(out)))
	var prs []prInfo
	if err := json.Unmarshal(out, &prs); err != nil || len(prs) == 0 {
		return prInfo{}, nil
	}
	p := prs[0]
	if p.IsDraft && p.State == "OPEN" {
		p.State = "DRAFT"
	}
	return p, nil
}
