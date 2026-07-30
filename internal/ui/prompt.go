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
	promptGroup
	promptPRTitle
	promptAddRepo
)

type prompt struct {
	kind   promptKind
	label  string
	input  textinput.Model
	target string // session id the prompt applies to
}

func (m *Model) startPrompt(kind promptKind, label, initial, target string) {
	ti := textinput.New()
	ti.SetWidth(max(m.width-len(label)-16, 20))
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
		case promptGroup:
			s := core.FindByID(m.sessions, p.target)
			if s == nil {
				return m, nil
			}
			s.Group = val
			// Typing a label that does not exist creates it.
			if m.cfg.AddGroup(val) {
				_ = core.SaveConfig(m.cfg)
			}
			m.save()
			m.rebuild()
			return m, status("project set")

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
