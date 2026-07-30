package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

// Compose field keys.
const (
	fieldTask    = "task"
	fieldRepo    = "repo"
	fieldGroup   = "group"
	fieldProfile = "profile"
	fieldBase    = "base"
)

// compose is the inline new-session flow. It lives in the bottom bar rather
// than a modal overlay so the board stays visible while composing -- it should
// be obvious if a session for that work already exists.
//
// Values live in a map keyed by field name rather than in struct fields
// addressed by pointer: the model is copied by value on every update, so any
// pointer into it would be writing to a stale copy a frame later.
type compose struct {
	active  bool
	input   textinput.Model
	fields  []string
	values  map[string]string
	focused int
	// fresh marks a field just tabbed into, whose value is still the inferred
	// default. The first character typed replaces it rather than appending, so
	// overriding a field is "tab, type" instead of "tab, clear, type".
	fresh bool
}

func (c compose) get(key string) string { return c.values[key] }

// startCompose seeds the fields by inference, so the common path stays
// n → type → enter.
func (m *Model) startCompose() {
	c := compose{active: true, values: map[string]string{}}

	// Group comes from the selected swimlane.
	c.values[fieldGroup] = m.currentGroup()

	// Repo comes from the selected card's repo, else the configured default.
	// It is never inferred from the group: group and repo are orthogonal.
	repo := m.cfg.DefaultRepo
	if s := m.selected(); s != nil {
		repo = s.RepoID
	}
	if m.repoFilter != "" {
		repo = m.repoFilter
	}
	c.values[fieldRepo] = repo

	c.values[fieldProfile] = m.cfg.DefaultProfile
	if r, ok := m.cfg.Repo(repo); ok {
		c.values[fieldBase] = r.BaseBranch
	}

	ti := textinput.New()
	ti.Placeholder = "what should the agent do?"
	ti.SetWidth(composeInputWidth(m.width))
	ti.SetVirtualCursor(true)
	c.input = ti

	c.fields = []string{fieldTask}
	// A single-option control is noise: hide the repo field entirely when only
	// one repo is registered.
	if m.cfg.MultiRepo() {
		c.fields = append(c.fields, fieldRepo)
	}
	c.fields = append(c.fields, fieldGroup, fieldProfile, fieldBase)

	c.focused = 0
	c.input.SetValue(c.values[fieldTask])
	m.compose = c
	m.compose.input.Focus()
}

// composeInputWidth leaves room for the unfocused fields and the key trailer,
// so the whole compose state stays readable on one row.
func composeInputWidth(total int) int {
	return max(total-72, 16)
}

// syncField writes the input back into the focused field.
func (c *compose) syncField() {
	if c.focused >= 0 && c.focused < len(c.fields) {
		c.values[c.fields[c.focused]] = c.input.Value()
	}
}

// cycle moves focus to the next field, carrying its current value in.
func (c *compose) cycle(dir int) {
	c.syncField()
	n := len(c.fields)
	c.focused = ((c.focused+dir)%n + n) % n
	c.input.SetValue(c.values[c.fields[c.focused]])
	c.input.CursorEnd()
	c.fresh = true
}

// consumeFresh clears a just-focused field the first time the user types into
// it. Editing keys (arrows, backspace) keep the existing value instead.
func (c *compose) consumeFresh(printable bool) {
	if !c.fresh {
		return
	}
	c.fresh = false
	if printable {
		c.input.SetValue("")
	}
}

// viewCompose renders the compose bar into one row.
func (m Model) viewCompose() string {
	st := m.styles
	c := m.compose

	var parts []string
	for i, key := range c.fields {
		if i == c.focused {
			parts = append(parts,
				st.KeyHint.Render(key+":")+" "+c.input.View())
			continue
		}
		val := c.values[key]
		if val == "" {
			val = "—"
		}
		parts = append(parts, st.KeyDesc.Render(key+":")+" "+st.Meta.Render(val))
	}
	line := strings.Join(parts, st.Meta.Render("  "))
	trailer := st.KeyDesc.Render("  tab · enter · esc")
	return lipgloss.NewStyle().Width(m.width).Render(truncate(line+trailer, m.width))
}

// currentGroup is the group of the selected swimlane, used to seed compose.
func (m Model) currentGroup() string {
	p := m.layout.find(m.selectedID)
	if p.OK && p.Lane < len(m.layout.Lanes) {
		return m.layout.Lanes[p.Lane].Group
	}
	return ""
}

// promptKind distinguishes the single-field prompts that reuse one input.
type promptKind int

const (
	promptNone promptKind = iota
	promptGroup
	promptRepoFilter
	promptPRTitle
)

// prompt is a one-shot single-field input in the bottom bar.
type prompt struct {
	active bool
	kind   promptKind
	label  string
	input  textinput.Model
	target string // session id the prompt applies to
}

func (m *Model) startPrompt(kind promptKind, label, initial, target string) {
	ti := textinput.New()
	ti.SetWidth(max(m.width-len(label)-8, 20))
	ti.SetValue(initial)
	ti.SetVirtualCursor(true)
	ti.CursorEnd()
	m.prompt = prompt{active: true, kind: kind, label: label, input: ti, target: target}
	m.prompt.input.Focus()
}

func (m Model) viewPrompt() string {
	st := m.styles
	line := st.KeyHint.Render(m.prompt.label+":") + " " + m.prompt.input.View() +
		st.KeyDesc.Render("   enter confirm · esc cancel")
	return lipgloss.NewStyle().Width(m.width).Render(truncate(line, m.width))
}
