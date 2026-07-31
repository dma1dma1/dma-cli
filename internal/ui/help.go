package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The help screen is the whole keymap on one page, and the page is longer than
// most windows. Rather than page through it, you type: every keystroke narrows
// the list, so finding "the key that prunes merged sessions" costs the word
// "prune" instead of a scan.
//
// The search is per word: each word you type has to appear in a row's keys, its
// description, or the section it sits under, and every word has to land
// somewhere for the row to survive. So "pr open" finds "open the PR in your
// browser" without being the phrase, and "diff" pulls up the whole review
// section.
//
// Fuzzy is the fallback rather than the rule. A word that appears literally
// anywhere in the keymap is matched literally; only a word that appears nowhere
// falls back to matching its letters in order, which is what makes "sbs" find
// side-by-side. Fuzzy first was tried and is too loose to be useful at this
// size: "prune" reads out of "previous/next column" as p-r-...-u-n-e, and a
// search for the prune keys came back with a third of the board section.

// helpRow is one keymap entry flattened out of helpText, carrying the section
// heading above it so a search can match on it.
type helpRow struct{ section, keys, desc string }

// helpHit is a row that survived the filter, with the rune positions each query
// word landed on so the view can show why it matched.
type helpHit struct {
	helpRow
	keyHits  []int
	descHits []int
}

// helpRows flattens helpText once per render. The table is a few dozen rows, so
// the cost is noise next to a single terminal repaint.
func helpRows() []helpRow {
	var rows []helpRow
	section := ""
	for _, r := range helpText {
		if r[0] != "" {
			section = r[0]
			continue
		}
		rows = append(rows, helpRow{section: section, keys: r[1], desc: r[2]})
	}
	return rows
}

// filterHelp returns the rows matching every word of the query, in the order
// the keymap declares them.
//
// Source order rather than by score: the sections are how the keymap is
// learned, and a list that reshuffles as you type costs you the map. Scoring
// would only pay off if this list were long enough to need a top result, and it
// is not.
func filterHelp(query string) []helpHit {
	rows := helpRows()
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		hits := make([]helpHit, 0, len(rows))
		for _, r := range rows {
			hits = append(hits, helpHit{helpRow: r})
		}
		return hits
	}

	// Decided per word, not per row: whether a word gets the literal reading or
	// the fuzzy one has to be the same everywhere, or the same query would hold
	// some rows to a stricter test than others.
	loose := make([]bool, len(words))
	for i, w := range words {
		loose[i] = !anyRowContains(rows, w)
	}

	var hits []helpHit
	for _, r := range rows {
		hit := helpHit{helpRow: r}
		matched := true
		for i, w := range words {
			// A word may land in more than one field; every place it does is
			// highlighted, and matching the section carries the whole section
			// through, which is how "diff" shows the Diff block entire.
			kh, inKeys := matchWord(r.keys, w, loose[i])
			dh, inDesc := matchWord(r.desc, w, loose[i])
			_, inSection := matchWord(r.section, w, loose[i])
			if !inKeys && !inDesc && !inSection {
				matched = false
				break
			}
			hit.keyHits = append(hit.keyHits, kh...)
			hit.descHits = append(hit.descHits, dh...)
		}
		if matched {
			hits = append(hits, hit)
		}
	}
	return hits
}

// anyRowContains reports whether the word shows up literally anywhere in the
// keymap, which is what decides between the two readings above.
func anyRowContains(rows []helpRow, word string) bool {
	for _, r := range rows {
		if _, ok := substrMatch(r.keys, word); ok {
			return true
		}
		if _, ok := substrMatch(r.desc, word); ok {
			return true
		}
		if _, ok := substrMatch(r.section, word); ok {
			return true
		}
	}
	return false
}

func matchWord(field, word string, loose bool) ([]int, bool) {
	if loose {
		return fuzzyMatch(field, word)
	}
	return substrMatch(field, word)
}

// substrMatch reports whether pattern appears in s whole, and where its runes
// landed. pattern must already be lowercase; s is folded here.
func substrMatch(s, pattern string) ([]int, bool) {
	if pattern == "" {
		return nil, true
	}
	hay := []rune(strings.ToLower(s))
	pat := []rune(pattern)
	i := runeIndex(hay, pat)
	if i < 0 {
		return nil, false
	}
	idx := make([]int, len(pat))
	for j := range pat {
		idx[j] = i + j
	}
	return idx, true
}

// fuzzyMatch reports whether pattern appears in s as a subsequence, and where
// its runes landed.
//
// A contiguous run is preferred when there is one: "merge" typed against "merge
// the PR" should mark the word, not the m of "merge" and four letters scattered
// through the rest of the line.
func fuzzyMatch(s, pattern string) ([]int, bool) {
	if idx, ok := substrMatch(s, pattern); ok {
		return idx, true
	}
	hay := []rune(strings.ToLower(s))
	pat := []rune(pattern)
	if len(pat) > len(hay) {
		return nil, false
	}

	var idx []int
	p := 0
	for i, r := range hay {
		if r == pat[p] {
			idx = append(idx, i)
			if p++; p == len(pat) {
				return idx, true
			}
		}
	}
	return nil, false
}

// runeIndex is strings.Index over runes, so the positions it returns index the
// same slice the caller highlights. Byte offsets would be off by the width of
// every arrow and ellipsis in the keymap.
func runeIndex(hay, pat []rune) int {
	for i := 0; i+len(pat) <= len(hay); i++ {
		if string(hay[i:i+len(pat)]) == string(pat) {
			return i
		}
	}
	return -1
}

// --- keys ---

// keyHelp runs the search box. Every printable key types into it, which is why
// this screen no longer closes on any key: a filter you cannot type a q into is
// not a filter.
func (m Model) keyHelp(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "esc":
		// Escape backs out one layer at a time. With a narrowed list on screen,
		// closing outright would mean retyping the search to get it back.
		if m.helpQuery != "" {
			m.helpQuery = ""
			return m, nil
		}
		m.mode = modeBoard
		return m, nil

	case "enter", "?":
		// ? both opens and closes, and it is the one printable key nobody needs to
		// search for: the row it would find is the row telling you what ? does.
		m.mode = modeBoard
		m.helpQuery = ""
		return m, nil

	case "backspace":
		if r := []rune(m.helpQuery); len(r) > 0 {
			m.helpQuery = string(r[:len(r)-1])
		}
		return m, nil

	case "ctrl+u":
		m.helpQuery = ""
		return m, nil

	case "ctrl+w":
		m.helpQuery = strings.TrimRightFunc(strings.TrimRight(m.helpQuery, " "), func(r rune) bool {
			return r != ' '
		})
		return m, nil
	}

	// Anything that produced text goes into the query; chords and cursor keys are
	// ignored rather than closing the screen out from under a search.
	if msg.Text != "" && !msg.Mod.Contains(tea.ModCtrl) && !msg.Mod.Contains(tea.ModAlt) {
		m.helpQuery += msg.Text
	}
	return m, nil
}

// --- view ---

// helpKeyWidth is the column the descriptions start in: wide enough for the
// longest chord in the keymap.
const helpKeyWidth = 12

// padRight pads to a display width rather than a byte count, which is what the
// arrow rows need: "← →" is three cells and seven bytes, and padding by the
// latter left the descriptions beside it four columns out of line.
func padRight(s string, n int) string {
	if w := lipgloss.Width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

func (m Model) viewHelp() string {
	st := m.styles
	hits := filterHelp(m.helpQuery)

	lines := []string{"", "  " + m.helpSearchLine(len(hits))}
	if len(hits) == 0 {
		lines = append(lines, "", "  "+st.Faint.Render("no keys match — backspace to widen the search"))
	}

	section := ""
	for _, h := range hits {
		if h.section != section {
			section = h.section
			lines = append(lines, "", "  "+st.Title.Render(section))
		}
		lines = append(lines, "    "+
			highlight(padRight(h.keys, helpKeyWidth), h.keyHits, st.KeyHint, st.Match)+"  "+
			highlight(h.desc, h.descHits, st.KeyDesc, st.Match))
	}

	tail := []string{
		"",
		"  " + st.Faint.Render("hook listener: "+m.hookURL),
	}
	// The keymap is longer than a short window. Clip the list rather than let the
	// frame clip the whole view, so the search line -- the way out of a list this
	// long -- is never the row that falls off the bottom of it.
	if avail := max(m.height-len(m.footer())-len(tail), 1); len(lines) > avail {
		lines = lines[:avail-1]
		lines = append(lines, "  "+st.Faint.Render("… more keys below — search, or grow the window"))
	}
	return strings.Join(append(lines, tail...), "\n")
}

// helpSearchLine is the top row: what has been typed, and how much of the
// keymap is left after it.
func (m Model) helpSearchLine(n int) string {
	st := m.styles
	line := st.KeyHint.Render("search ") + st.Title.Render(m.helpQuery) + st.Match.Render("▏")
	if m.helpQuery == "" {
		return line + "  " + st.Faint.Render("type to filter · esc back")
	}
	return line + "  " + st.Faint.Render(fmt.Sprintf("%s · esc clears", plural(n, "key", "keys")))
}

// plural renders a count with the right noun. "1 keys" in a UI this careful
// about its wording would read as a bug.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// highlight renders s in base, with the runes at idx in hit instead.
//
// Contiguous runs share one styled span: a help screen redrawn on every
// keystroke should not carry an escape sequence per character.
func highlight(s string, idx []int, base, hit lipgloss.Style) string {
	if len(idx) == 0 {
		return base.Render(s)
	}
	marked := make(map[int]bool, len(idx))
	for _, i := range idx {
		marked[i] = true
	}

	var b strings.Builder
	runs := []rune(s)
	for i := 0; i < len(runs); {
		on := marked[i]
		j := i
		for j < len(runs) && marked[j] == on {
			j++
		}
		span := string(runs[i:j])
		if on {
			b.WriteString(hit.Render(span))
		} else {
			b.WriteString(base.Render(span))
		}
		i = j
	}
	return b.String()
}
