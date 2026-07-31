// Package hooks receives Claude Code lifecycle hooks over HTTP and turns them
// into agent-state events.
//
// State is driven by hooks rather than by scraping tmux pane output: pane text
// is a rendering of the agent's UI, not a state machine, and any heuristic over
// it is wrong the moment the agent's output format changes.
package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// Event is one decoded hook payload. Correlation to a board session happens in
// the UI, which owns the session list; the server stays stateless.
type Event struct {
	EventName        string
	NotificationType string
	Message          string
	ToolName         string
	Cwd              string
	ClaudeSessionID  string
	At               time.Time
}

// payload mirrors the subset of the hook input schema this tool reads.
type payload struct {
	SessionID        string `json:"session_id"`
	Cwd              string `json:"cwd"`
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
	Message          string `json:"message"`
	ToolName         string `json:"tool_name"`
}

// response is what we send back. Read-only state reporting must never block the
// agent, so this never carries a "block" decision, and the Stop handler is
// strictly passive -- a Stop hook that caused the agent to act would loop
// forever.
type response struct {
	SuppressOutput   bool   `json:"suppressOutput"`
	TerminalSequence string `json:"terminalSequence,omitempty"`
}

type Server struct {
	ln     net.Listener
	srv    *http.Server
	events chan Event
	port   int
}

// Path is the endpoint hooks POST to.
const Path = "/hook"

// Start binds a listener on loopback only. Port 0 asks the OS for a free port.
func Start(port int) (*Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("hook listener on port %d: %w", port, err)
	}
	s := &Server{
		ln:     ln,
		events: make(chan Event, 256),
		port:   ln.Addr().(*net.TCPAddr).Port,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(Path, s.handle)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

func (s *Server) Port() int            { return s.port }
func (s *Server) Events() <-chan Event { return s.events }
func (s *Server) URL() string          { return fmt.Sprintf("http://127.0.0.1:%d%s", s.port, Path) }

func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var p payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		// A malformed payload must not surface as a hook failure inside the
		// agent's session, so still answer 200.
		writeJSON(w, response{SuppressOutput: true})
		return
	}

	ev := Event{
		EventName:        p.HookEventName,
		NotificationType: p.NotificationType,
		Message:          strings.TrimSpace(p.Message),
		ToolName:         p.ToolName,
		Cwd:              p.Cwd,
		ClaudeSessionID:  p.SessionID,
		At:               time.Now(),
	}

	// Never block the agent on a slow or stopped UI: drop the event instead.
	// Heartbeats are frequent and individually worthless; losing one is fine.
	select {
	case s.events <- ev:
	default:
	}

	resp := response{SuppressOutput: true}
	if needsAttention(ev) {
		// The terminal bell has to be requested through the hook response;
		// hooks run without a controlling terminal and cannot emit escape
		// sequences to Claude Code directly.
		resp.TerminalSequence = "\a"
	}
	writeJSON(w, resp)
}

// needsAttention decides whether an event deserves a bell. It follows Interpret
// so the bell and the badge cannot disagree: only a genuine request for input
// rings, never the idle timer.
func needsAttention(ev Event) bool {
	return Interpret(ev).State == core.AgentNeedsYou
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}
