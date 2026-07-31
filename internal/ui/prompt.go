package ui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// promptKind distinguishes the one-shot single-field prompts that share the
// status line.
type promptKind int

const (
	promptNone promptKind = iota
	promptNewProject
	promptPRTitle
	promptAddRepo
	// promptProfileShepherd and promptRepoShepherd edit an on-PR-open line. The
	// two are separate kinds rather than one with a flag because they mean
	// different things when submitted empty: an agent with no line simply has
	// none, while a repo with an empty override is refusing the one its agent
	// sets.
	promptProfileShepherd
	promptRepoShepherd
)

type prompt struct {
	kind  promptKind
	label string
	input textinput.Model
	// target is what the prompt applies to -- a session id, a repo id or a
	// profile name, according to kind.
	target string
}

func (m *Model) startPrompt(kind promptKind, label, initial, target string) {
	ti := textinput.New()
	// The label already introduces the field; textinput's default "> " would
	// render as "project: > value".
	ti.Prompt = ""
	ti.SetWidth(max(m.contentWidth()-len(label)-16, 20))
	ti.SetValue(initial)
	ti.SetVirtualCursor(true)
	ti.CursorEnd()
	m.prompt = prompt{kind: kind, label: label, input: ti, target: target}
	m.prompt.input.Focus()
}

func (m Model) keyPrompt(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.prompt = prompt{}
		m.mode = modeBoard
		return m, nil

	case "enter":
		p := m.prompt
		val := strings.TrimSpace(p.input.Value())
		m.prompt = prompt{}
		m.mode = modeBoard

		switch p.kind {
		case promptNewProject:
			if val == "" {
				return m, nil
			}
			// Opened from a card, the new project takes that session, and the
			// repo that session runs in. Opened from the chip it takes the
			// board: a project you just named is the one you meant to be working
			// in, so the next session starts there, in the repo the chip names.
			if p.target != "" {
				m.setSessionProject(p.target, val)
				return m, nil
			}
			if m.cfg.AddProject(val, m.activeRepoID()) {
				_ = core.SaveConfig(m.cfg)
			}
			m.selectProject(val)
			m.rebuild()
			return m, nil

		case promptPRTitle:
			s := core.FindByID(m.sessions, p.target)
			if s == nil {
				return m, nil
			}
			if val == "" {
				val = s.Title
			}
			return m, tea.Batch(shipCmd(m.cfg, s, val), status("pushing and opening PR…"))

		case promptAddRepo:
			if val == "" {
				return m, nil
			}
			return m, tea.Batch(adoptCmd(m.cfg, val), status("registering "+val+"…"))

		case promptProfileShepherd:
			return m, m.setProfileShepherd(p.target, val)

		case promptRepoShepherd:
			// Submitted empty this is "nothing here", not a cancellation: esc is how
			// you back out, and O is how a repo goes back to inheriting.
			return m, m.setRepoShepherd(p.target, &val)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.prompt.input, cmd = m.prompt.input.Update(msg)
	return m, cmd
}

// now and timeSince are wrapped so the click handling reads cleanly.
func now() time.Time                      { return time.Now() }
func timeSince(t time.Time) time.Duration { return time.Since(t) }
