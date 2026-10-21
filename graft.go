package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// graftStateFile mirrors the subset of ~/.config/graft/state.json tulip cares about.
type graftStateFile struct {
	EnabledGraftInterception bool     `json:"enabledGraftInterception"`
	EnabledPlugins           []string `json:"enabledPlugins"`
	EnabledDomains           []string `json:"enabledDomains"`
}

// graftConfigFile mirrors the subset of ~/.config/graft/config.json tulip cares about.
type graftConfigFile struct {
	Domains []struct {
		Domain string `json:"domain"`
	} `json:"domains"`
}

// graftSummary is tulip's read-only view of Graft's own global config, used
// to show what Graft is currently set up to do and where.
type graftSummary struct {
	Domains        []string // enabled domains, in Graft's own order
	HasStateInfo   bool     // whether InterceptionEnabled/EnabledPlugins came from state.json
	Interception   bool
	EnabledPlugins []string
}

// graftConfigDir returns the directory where the Graft browser extension's
// native host stores its config and state — separate from tulip's own config.
func graftConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "graft"), nil
}

// readGraftSummary reads Graft's own config/state files. It prefers
// state.json (what's actually enabled right now) and falls back to
// config.json's full domain list if state.json is missing.
func readGraftSummary() (graftSummary, error) {
	dir, err := graftConfigDir()
	if err != nil {
		return graftSummary{}, err
	}

	if data, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		var st graftStateFile
		if err := json.Unmarshal(data, &st); err != nil {
			return graftSummary{}, err
		}
		return graftSummary{
			Domains:        st.EnabledDomains,
			HasStateInfo:   true,
			Interception:   st.EnabledGraftInterception,
			EnabledPlugins: st.EnabledPlugins,
		}, nil
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return graftSummary{}, err
	}
	var cfg graftConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return graftSummary{}, err
	}
	domains := make([]string, len(cfg.Domains))
	for i, d := range cfg.Domains {
		domains[i] = d.Domain
	}
	return graftSummary{Domains: domains}, nil
}

// graftPreviewURLs returns the Grafana Cloud URLs Graft is currently
// configured to intercept and render local plugin builds on. If cfg has a
// GraftDefaultDomain set and it's among the enabled domains, it's moved to
// the front so callers that just want "the" preview URL can take urls[0].
func graftPreviewURLs(cfg *Config) ([]string, error) {
	summary, err := readGraftSummary()
	if err != nil {
		return nil, err
	}
	domains := summary.Domains
	if cfg != nil && cfg.GraftDefaultDomain != "" {
		for i, d := range domains {
			if d == cfg.GraftDefaultDomain {
				reordered := make([]string, 0, len(domains))
				reordered = append(reordered, d)
				reordered = append(reordered, domains[:i]...)
				reordered = append(reordered, domains[i+1:]...)
				domains = reordered
				break
			}
		}
	}
	return domainsToURLs(domains), nil
}

func domainsToURLs(domains []string) []string {
	urls := make([]string, len(domains))
	for i, d := range domains {
		urls[i] = "https://" + d
	}
	return urls
}
