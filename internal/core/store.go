package core

import (
	"encoding/hex"
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
	sf, err := loadStateFile()
	if err != nil {
		return nil, err
	}
	filtered := sf.Sessions[:0]
	for _, s := range sf.Sessions {
		if !sessionPruned(s.ID) {
			filtered = append(filtered, s)
		}
	}
	sf.Sessions = filtered
	normalizeSessions(sf.Sessions)
	return sf.Sessions, nil
}

func loadStateFile() (stateFile, error) {
	data, err := os.ReadFile(StatePath())
	if os.IsNotExist(err) {
		return stateFile{Version: 1}, nil
	}
	if err != nil {
		return stateFile{}, fmt.Errorf("read state: %w", err)
	}
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return stateFile{}, fmt.Errorf("parse %s: %w", StatePath(), err)
	}
	return sf, nil
}

func normalizeSessions(sessions []*Session) {
	for _, s := range sessions {
		// "review" was a manual-only column that has been replaced by the
		// agent-driven idle/active split; anything still in it lands in idle.
		if s.Lifecycle == "" || s.Lifecycle == "review" {
			s.Lifecycle = LifecycleIdle
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
		// An agent cannot still be working across a restart of the board: its
		// state is re-established by the first hook or probe.
		if !s.Lifecycle.PRDriven() && s.AgentState == AgentWorking {
			s.Lifecycle = LifecycleActive
		}
	}
}

// SaveSessions writes the session list atomically: temp file then rename, so a
// crash mid-write cannot truncate existing state. It is the replacement form,
// used for migrations and tests; a running board uses UpsertSessions so one
// process cannot erase a session another process just added.
func SaveSessions(sessions []*Session) error {
	return withStateLock(func() error {
		return writeSessions(filterPruned(sessions))
	})
}

// UpsertSessions persists the board's current copies without removing records
// it has not seen yet. Boards are long-lived and keep state in memory, so a
// whole-list save from one process would otherwise erase a concurrent attach or
// resurrect a prune with its stale copy of the card.
func UpsertSessions(sessions []*Session) error {
	return withStateLock(func() error {
		sf, err := loadStateFile()
		if err != nil {
			return err
		}
		byID := make(map[string]int, len(sf.Sessions)+len(sessions))
		out := make([]*Session, 0, len(sf.Sessions)+len(sessions))
		for _, s := range sf.Sessions {
			if sessionPruned(s.ID) {
				continue
			}
			byID[s.ID] = len(out)
			out = append(out, s)
		}
		for _, s := range sessions {
			if sessionPruned(s.ID) {
				continue
			}
			if i, ok := byID[s.ID]; ok {
				// Pruning is a claim held by another board process. An ordinary
				// stale save must not cancel its crash-recovery marker; only the
				// explicit SetSessionPruning(false) failure path may do that.
				if out[i].Pruning && !s.Pruning {
					copy := *s
					copy.Pruning = true
					s = &copy
				}
				out[i] = s
				continue
			}
			byID[s.ID] = len(out)
			out = append(out, s)
		}
		return writeSessions(out)
	})
}

// SetSessionPruning changes the durable teardown claim without replacing any
// other fields a different board may have refreshed. It is also the only path
// that clears a claim after teardown reports an error.
func SetSessionPruning(id string, pruning bool) error {
	return withStateLock(func() error {
		sf, err := loadStateFile()
		if err != nil {
			return err
		}
		for _, s := range sf.Sessions {
			if s.ID == id {
				s.Pruning = pruning
				return writeSessions(filterPruned(sf.Sessions))
			}
		}
		return nil
	})
}

// DeleteSession makes a prune win over every stale board that may still hold
// the card in memory. The tombstone is written first and is never removed: IDs
// are unique, and a later UpsertSessions filters the stale copy instead of
// bringing it back.
func DeleteSession(id string) error {
	if id == "" {
		return fmt.Errorf("delete session: empty id")
	}
	return withStateLock(func() error {
		if err := os.MkdirAll(prunedDir(), 0o755); err != nil {
			return err
		}
		if !sessionPruned(id) {
			if err := writeAtomic(prunedPath(id), []byte("pruned\n")); err != nil {
				return fmt.Errorf("record pruned session: %w", err)
			}
		}

		sf, err := loadStateFile()
		if err != nil {
			return err
		}
		out := sf.Sessions[:0]
		for _, s := range sf.Sessions {
			if s.ID != id && !sessionPruned(s.ID) {
				out = append(out, s)
			}
		}
		return writeSessions(out)
	})
}

func writeSessions(sessions []*Session) error {
	data, err := json.MarshalIndent(stateFile{Version: 1, Sessions: sessions}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(StatePath(), append(data, '\n'))
}

func filterPruned(sessions []*Session) []*Session {
	out := make([]*Session, 0, len(sessions))
	for _, s := range sessions {
		if !sessionPruned(s.ID) {
			out = append(out, s)
		}
	}
	return out
}

func prunedDir() string { return filepath.Join(Dir(), "pruned-sessions") }

// Hex keeps an agent-provided or hand-edited ID from becoming a path. It also
// makes every possible non-empty ID a portable filename.
func prunedPath(id string) string {
	return filepath.Join(prunedDir(), hex.EncodeToString([]byte(id)))
}

func sessionPruned(id string) bool {
	if id == "" {
		return false
	}
	_, err := os.Stat(prunedPath(id))
	return err == nil
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
	for _, p := range cfg.Groups {
		if p.Name == "" || seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		out = append(out, p.Name)
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
