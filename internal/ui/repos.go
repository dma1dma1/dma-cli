package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ops"
)

// repoPicker is the in-app repo manager. Repos are managed here rather than by
// hand-editing a config file, because choosing where work happens is part of
// the normal flow, not setup.
type repoPicker struct {
	cursor int
}

// activeRepoID is the repo new sessions default to. It starts as the repo the
// tool was launched from, so `cd ~/project && dma` needs no further choosing.
func (m Model) activeRepoID() string {
	if m.activeRepo != "" {
		if _, ok := m.cfg.Repo(m.activeRepo); ok {
			return m.activeRepo
		}
	}
	if m.cfg.DefaultRepo != "" {
		return m.cfg.DefaultRepo
	}
	if len(m.cfg.Repos) > 0 {
		return m.cfg.Repos[0].ID
	}
	return ""
}

func (m Model) viewRepos() string {
	st := m.styles
	var b strings.Builder

	b.WriteString("\n  " + st.Title.Render("Repositories") + "\n\n")

	if len(m.cfg.Repos) == 0 {
		b.WriteString("  " + st.Status.Render("None yet.") + "\n\n")
		b.WriteString("  " + st.KeyHint.Render("a") + " " +
			st.KeyDesc.Render("add one by path — dependencies and env files are detected for you") + "\n")
		return b.String()
	}

	active := m.activeRepoID()
	counts := map[string]int{}
	for _, s := range m.sessions {
		counts[s.RepoID]++
	}

	for i, r := range m.cfg.Repos {
		marker := "  "
		if r.ID == active {
			marker = st.KeyHint.Render("▸ ")
		}
		name := r.ID
		if i == m.repos.cursor {
			name = lipgloss.NewStyle().Foreground(st.P.Focus).Bold(true).Render(r.ID)
		} else {
			name = st.Title.Render(r.ID)
		}

		meta := []string{r.Path}
		if r.Remote != "" {
			meta = append(meta, r.Remote)
		} else {
			meta = append(meta, "no remote — PR features off")
		}
		meta = append(meta, fmt.Sprintf("%d session(s)", counts[r.ID]))

		b.WriteString("  " + marker + name + "\n")
		b.WriteString("      " + st.Meta.Render(truncate(strings.Join(meta, "  ·  "), max(m.contentWidth()-8, 20))) + "\n")
		b.WriteString("      " + st.Meta.Render(truncate(ops.SummarizeBootstrap(r.Bootstrap), max(m.contentWidth()-8, 20))) + "\n")
		b.WriteString("      " + st.Meta.Render(truncate(m.shepherdSummary(r), max(m.contentWidth()-8, 20))) + "\n\n")
	}
	return b.String()
}

func (m Model) keyRepos(key string) (tea.Model, tea.Cmd) {
	n := len(m.cfg.Repos)

	switch key {
	case "esc", "q", "r":
		m.mode = modeBoard
		return m, nil

	case "j", "down":
		if n > 0 {
			m.repos.cursor = (m.repos.cursor + 1) % n
		}
		return m, nil

	case "k", "up":
		if n > 0 {
			m.repos.cursor = (m.repos.cursor - 1 + n) % n
		}
		return m, nil

	case "a":
		m.startPrompt(promptAddRepo, "repo path", "", "")
		m.mode = modePrompt
		return m, nil

	case "enter":
		if n == 0 {
			return m, nil
		}
		r := m.cfg.Repos[m.repos.cursor]
		m.setActiveRepo(r.ID)
		m.mode = modeBoard
		// The repo selector on the board already reads back the active repo.
		return m, nil

	case "o":
		if n == 0 {
			return m, nil
		}
		return m, m.cycleRepoShepherd(m.cfg.Repos[m.repos.cursor])

	case "O":
		// The typed line is the rare case and gets the shifted key: the three
		// settings o cycles are what a repo actually wants nearly every time.
		if n == 0 {
			return m, nil
		}
		m.startRepoShepherdPrompt(m.cfg.Repos[m.repos.cursor])
		return m, nil

	case "x":
		if n == 0 {
			return m, nil
		}
		r := m.cfg.Repos[m.repos.cursor]
		for _, s := range m.sessions {
			if s.RepoID == r.ID {
				return m, errStatus(fmt.Errorf("%s still has sessions; prune them first", r.ID))
			}
		}
		return m.askConfirm(fmt.Sprintf("Unregister %s? (the repo itself is untouched)", r.ID),
			func(mm *Model) tea.Cmd { return mm.removeRepo(r.ID) })
	}
	return m, nil
}

// removeRepo drops a repo from the config. Nothing on disk is touched -- this
// only stops the board tracking it.
func (m *Model) removeRepo(id string) tea.Cmd {
	out := m.cfg.Repos[:0]
	for _, r := range m.cfg.Repos {
		if r.ID != id {
			out = append(out, r)
		}
	}
	m.cfg.Repos = out
	// Projects that worked here no longer point anywhere, and saying so is
	// better than leaving a binding that silently does nothing.
	m.cfg.UnbindRepo(id)
	if m.cfg.DefaultRepo == id {
		m.cfg.DefaultRepo = ""
		if len(m.cfg.Repos) > 0 {
			m.cfg.DefaultRepo = m.cfg.Repos[0].ID
		}
	}
	if m.activeRepo == id {
		m.activeRepo = m.cfg.DefaultRepo
	}
	if m.repos.cursor >= len(m.cfg.Repos) {
		m.repos.cursor = max(len(m.cfg.Repos)-1, 0)
	}
	if err := core.SaveConfig(m.cfg); err != nil {
		return errStatus(err)
	}
	m.rebuild()
	// The repo is gone from the list, which is the confirmation.
	return nil
}

// adoptCmd registers a repo by path, detecting its bootstrap paths.
func adoptCmd(cfg *core.Config, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := contextWithTimeout()
		defer cancel()
		repo, added, err := ops.Adopt(ctx, cfg, expandPath(path))
		return adoptedMsg{repo: repo, added: added, err: err}
	}
}
