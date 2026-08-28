package probe

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// DstackSchemaVersion is the supported schema version for dstack status files.
const DstackSchemaVersion = "dstack.status.v1"

// StatusHealth names the health classification for a dstack status snapshot.
type StatusHealth string

const (
	HealthLive     StatusHealth = "live"
	HealthStale    StatusHealth = "stale"
	HealthCrashed  StatusHealth = "crashed"
	HealthShutdown StatusHealth = "shutdown"
)

// StatusSemantic carries child or root progress detail.
type StatusSemantic struct {
	Phase     string `json:"phase,omitempty"`
	Note      string `json:"note,omitempty"`
	Blocking  bool   `json:"blocking,omitempty"`
	BlockedOn string `json:"blockedOn,omitempty"` // "human" | "approval" | "dependency" | "external"
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// StatusSnapshot is the subset of dstack.status.v1 parsed by the probe.
type StatusSnapshot struct {
	SchemaVersion string `json:"schemaVersion"`
	SessionID     string `json:"sessionId"`
	Process       struct {
		PID       int    `json:"pid"`
		StartedAt string `json:"startedAt"`
		Hostname  string `json:"hostname,omitempty"`
		Cwd       string `json:"cwd"`
		ExecPath  string `json:"execPath,omitempty"`
	} `json:"process"`
	Heartbeat struct {
		UpdatedAt  string `json:"updatedAt"`
		IntervalMs int    `json:"intervalMs"`
	} `json:"heartbeat"`
	Rollup string `json:"rollup"`
	Root   struct {
		State  string          `json:"state"`
		Status *StatusSemantic `json:"status,omitempty"`
	} `json:"root"`
	Shutdown *struct {
		Clean bool   `json:"clean"`
		At    string `json:"at,omitempty"`
	} `json:"shutdown,omitempty"`
}

// ProcInfo is one entry in the system process table.
type ProcInfo struct {
	PID       int
	PPID      int
	StartTime time.Time
}

// ProcessTable maps PIDs to their parent PID and start time.
type ProcessTable struct {
	Procs map[int]ProcInfo
}

// Distance returns the number of ancestry hops from ancestorPID down to childPID.
// Distance is 0 if childPID == ancestorPID, 1 if childPID is a direct child of ancestorPID,
// and so on. If childPID is not a descendant of ancestorPID, ok is false.
func (pt *ProcessTable) Distance(ancestorPID, childPID int) (int, bool) {
	if pt == nil || pt.Procs == nil || ancestorPID <= 0 || childPID <= 0 {
		return -1, false
	}
	depth := 0
	curr := childPID
	for {
		if curr == ancestorPID {
			return depth, true
		}
		parent, exists := pt.Procs[curr]
		if !exists || parent.PPID <= 0 || parent.PPID == curr {
			return -1, false
		}
		curr = parent.PPID
		depth++
		if depth > 100 {
			return -1, false
		}
	}
}

// IsProcessLive checks if the given PID exists in the process table and its start time
// matches startedAtStr within sensible subsecond tolerance.
func (pt *ProcessTable) IsProcessLive(pid int, startedAtStr string) bool {
	if pt == nil || pt.Procs == nil || pid <= 0 {
		return false
	}
	proc, ok := pt.Procs[pid]
	if !ok {
		return false
	}
	startedAt, err := time.Parse(time.RFC3339Nano, startedAtStr)
	if err != nil {
		return false
	}
	diff := proc.StartTime.Sub(startedAt)
	if diff < 0 {
		diff = -diff
	}
	// ps lstart has 1s resolution. Allow up to 2.5s tolerance for subsecond offset and clock alignment.
	return diff <= 2500*time.Millisecond
}

// ParseProcessTable parses the output of `ps -axo pid=,ppid=,lstart=`.
func ParseProcessTable(output string, loc *time.Location) (*ProcessTable, error) {
	if loc == nil {
		loc = time.Local
	}
	pt := &ProcessTable{
		Procs: make(map[int]ProcInfo),
	}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		startStr := strings.Join(fields[2:7], " ")
		startTime, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", startStr, loc)
		if err != nil {
			startTime, err = time.ParseInLocation("Mon Jan _2 15:04:05 2006", startStr, loc)
		}
		if err != nil {
			startTime, err = time.ParseInLocation(time.ANSIC, startStr, loc)
		}
		if err != nil {
			continue
		}
		pt.Procs[pid] = ProcInfo{
			PID:       pid,
			PPID:      ppid,
			StartTime: startTime,
		}
	}
	return pt, nil
}

// LoadProcessTable reads the system process table via ps.
func LoadProcessTable(ctx context.Context) (*ProcessTable, error) {
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,lstart=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ps: %w (stderr: %s)", err, stderr.String())
	}
	return ParseProcessTable(stdout.String(), time.Local)
}

// EncodedSessionID encodes a session ID to base64url without padding.
func EncodedSessionID(sessionID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sessionID))
}

// DstackStatusPath computes the status file path for a session ID.
func DstackStatusPath(statusDir, sessionID string) string {
	return filepath.Join(statusDir, EncodedSessionID(sessionID)+".json")
}

// DefaultStatusDir returns ~/.pi/agent/dstack/status.
func DefaultStatusDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent", "dstack", "status")
}

// ReadStatusFile parses a single dstack status JSON file.
func ReadStatusFile(path string) (*StatusSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap StatusSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	if snap.SchemaVersion != DstackSchemaVersion || snap.SessionID == "" || snap.Process.PID <= 0 ||
		snap.Process.StartedAt == "" || snap.Heartbeat.UpdatedAt == "" || snap.Heartbeat.IntervalMs <= 0 ||
		!validRollup(snap.Rollup) {
		return nil, fmt.Errorf("invalid dstack status file: schemaVersion=%q", snap.SchemaVersion)
	}
	return &snap, nil
}

func validRollup(rollup string) bool {
	switch rollup {
	case "working", "waiting_on_input", "waiting_on_approval", "idle", "completed", "failed":
		return true
	default:
		return false
	}
}

// FindNearestStatusWriter finds the status snapshot written by a process nearest
// to panePID in the process ancestry tree.
func FindNearestStatusWriter(statusDir string, panePID int, pt *ProcessTable) (*StatusSnapshot, error) {
	if statusDir == "" || panePID <= 0 || pt == nil {
		return nil, os.ErrNotExist
	}
	entries, err := os.ReadDir(statusDir)
	if err != nil {
		return nil, err
	}

	var bestSnap *StatusSnapshot
	bestDepth := math.MaxInt

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		snap, err := ReadStatusFile(filepath.Join(statusDir, entry.Name()))
		if err != nil || snap == nil || snap.Process.PID <= 0 {
			continue
		}
		depth, ok := pt.Distance(panePID, snap.Process.PID)
		if !ok || !pt.IsProcessLive(snap.Process.PID, snap.Process.StartedAt) {
			continue
		}
		if depth < bestDepth {
			bestDepth = depth
			bestSnap = snap
		}
	}

	if bestSnap == nil {
		return nil, os.ErrNotExist
	}
	return bestSnap, nil
}

// ClassifyHealth evaluates snapshot health against heartbeat staleness, clean shutdown, and process liveness.
func ClassifyHealth(snapshot *StatusSnapshot, pt *ProcessTable, now time.Time) StatusHealth {
	if snapshot.Shutdown != nil && snapshot.Shutdown.Clean {
		return HealthShutdown
	}
	heartbeatAt, err := time.Parse(time.RFC3339Nano, snapshot.Heartbeat.UpdatedAt)
	if err != nil {
		heartbeatAt, err = time.Parse(time.RFC3339, snapshot.Heartbeat.UpdatedAt)
	}
	interval := time.Duration(snapshot.Heartbeat.IntervalMs) * time.Millisecond
	stale := err != nil || now.Sub(heartbeatAt) > 2*interval
	if !stale {
		return HealthLive
	}
	if pt == nil || pt.IsProcessLive(snapshot.Process.PID, snapshot.Process.StartedAt) {
		return HealthStale
	}
	return HealthCrashed
}

// MapStatusToAgentState maps a structured dstack snapshot and its health to core.AgentState and detail.
func MapStatusToAgentState(snapshot *StatusSnapshot, health StatusHealth) (core.AgentState, string) {
	if health == HealthShutdown {
		return core.AgentIdle, ""
	}
	if health == HealthCrashed {
		return core.AgentNeedsYou, "agent crashed"
	}

	switch snapshot.Rollup {
	case "waiting_on_input":
		return core.AgentNeedsYou, extractBlockerDetail(snapshot, "human")
	case "waiting_on_approval":
		return core.AgentNeedsYou, extractBlockerDetail(snapshot, "approval")
	case "working":
		return core.AgentWorking, extractWorkingDetail(snapshot)
	case "completed":
		return core.AgentDone, ""
	case "failed":
		return core.AgentNeedsYou, extractFailureDetail(snapshot)
	case "idle":
		return core.AgentIdle, ""
	default:
		return core.AgentIdle, ""
	}
}

func extractBlockerDetail(snapshot *StatusSnapshot, blocker string) string {
	if snapshot.Root.Status != nil && snapshot.Root.Status.BlockedOn == blocker && snapshot.Root.Status.Note != "" {
		return truncate(snapshot.Root.Status.Note, 60)
	}
	if blocker == "approval" {
		return "waiting for approval"
	}
	return "waiting for input"
}

func extractFailureDetail(snapshot *StatusSnapshot) string {
	if snapshot.Root.Status != nil && snapshot.Root.Status.Note != "" {
		return truncate(snapshot.Root.Status.Note, 60)
	}
	return "dstack task failed"
}

func extractWorkingDetail(snapshot *StatusSnapshot) string {
	if snapshot.Root.Status != nil {
		if snapshot.Root.Status.Phase != "" && snapshot.Root.Status.Note != "" {
			return truncate(snapshot.Root.Status.Phase+": "+snapshot.Root.Status.Note, 60)
		}
		if snapshot.Root.Status.Note != "" {
			return truncate(snapshot.Root.Status.Note, 60)
		}
		if snapshot.Root.Status.Phase != "" {
			return truncate(snapshot.Root.Status.Phase, 60)
		}
	}
	return ""
}
