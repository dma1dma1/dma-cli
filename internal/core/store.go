package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type stateFile struct {
	Version  int        `json:"version"`
	Sessions []*Session `json:"sessions"`
}

// LoadSessions reads the persisted session list. A missing file is not an error.
func LoadSessions() ([]*Session, error) {
	data, err := os.ReadFile(StatePath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", StatePath(), err)
	}
	for _, s := range sf.Sessions {
		if s.Lifecycle == "" {
			s.Lifecycle = LifecycleActive
		}
		if s.AgentState == "" {
			s.AgentState = AgentIdle
		}
		if s.AgentStateSince.IsZero() {
			s.AgentStateSince = s.CreatedAt
		}
		if s.PRState == "" {
			s.PRState = PRNone
		}
		if s.PRCI == "" {
			s.PRCI = CINone
		}
		if s.PRReview == "" {
			s.PRReview = ReviewNone
		}
		if s.PRMergeable == "" {
			s.PRMergeable = MergeUnknown
		}
	}
	return sf.Sessions, nil
}

// SaveSessions writes the session list atomically: temp file then rename, so a
// crash mid-write cannot truncate existing state.
func SaveSessions(sessions []*Session) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(stateFile{Version: 1, Sessions: sessions}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(StatePath(), append(data, '\n'))
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// FindByKey returns the session owning the given (repo_id, branch) pair.
// Matching on branch alone would cross-assign PRs between repos that happen to
// share a branch name.
func FindByKey(sessions []*Session, k Key) *Session {
	for _, s := range sessions {
		if s.RepoID == k.RepoID && s.Branch == k.Branch {
			return s
		}
	}
	return nil
}

func FindByID(sessions []*Session, id string) *Session {
	for _, s := range sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// GroupOrder returns the swimlane order: configured groups first (in config
// order), then any groups seen only in sessions, with ungrouped always last.
func GroupOrder(cfg *Config, sessions []*Session) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range cfg.Groups {
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	var extra []string
	hasUngrouped := false
	for _, s := range sessions {
		if s.Group == "" {
			hasUngrouped = true
			continue
		}
		if !seen[s.Group] {
			seen[s.Group] = true
			extra = append(extra, s.Group)
		}
	}
	sort.Strings(extra)
	out = append(out, extra...)
	if hasUngrouped || len(out) == 0 {
		out = append(out, "")
	}
	return out
}

// Rollup counts agent states within a set of sessions, for the group header.
type Rollup struct {
	Working  int
	NeedsYou int
	Done     int
	Idle     int
	Total    int
}

func RollupOf(sessions []*Session) Rollup {
	var r Rollup
	for _, s := range sessions {
		r.Total++
		switch s.AgentState {
		case AgentWorking:
			r.Working++
		case AgentNeedsYou:
			r.NeedsYou++
		case AgentDone:
			r.Done++
		default:
			r.Idle++
		}
	}
	return r
}

// SortColumn orders cards within a column: needs_you first, then longest time
// in state. This is what makes attention-needing sessions findable without a
// dedicated rail.
func SortColumn(in []*Session) {
	sort.SliceStable(in, func(i, j int) bool {
		a, b := in[i], in[j]
		if ra, rb := a.AgentState.SortRank(), b.AgentState.SortRank(); ra != rb {
			return ra < rb
		}
		return a.AgentStateSince.Before(b.AgentStateSince)
	})
}

// Touch records the moment a PR sync completed.
func Touch(s *Session) { s.PRSyncedAt = time.Now() }
