// Package fuzzy ranks paths against a typed query.
//
// It is in-process rather than a call out to fzf. Two reasons: fzf is nowhere
// near as widely installed as the other optional tools, so the common case
// would be the fallback; and a subprocess hands back an order without saying
// which characters it matched, which is exactly what the list has to underline
// for the ranking to look like anything but an opinion.
package fuzzy

import (
	"sort"
	"strings"
)

// Result is one candidate that matched.
type Result struct {
	// Index is the candidate's position in the slice passed to Rank, so the
	// caller can get back to whatever it was carrying alongside the string.
	Index int
	Text  string
	Score int
	// Match is the byte offset of each character the query matched, in order.
	// It is what the list underlines.
	Match []int
}

// Rank filters candidates to those matching query and orders them best first,
// returning at most limit of them. An empty query matches everything, in the
// order it arrived: the finder opens on the file list rather than on nothing.
func Rank(query string, candidates []string, limit int) []Result {
	out := make([]Result, 0, min(limit, len(candidates)))
	if query == "" {
		for i, c := range candidates {
			if len(out) == limit {
				break
			}
			out = append(out, Result{Index: i, Text: c})
		}
		return out
	}

	for i, c := range candidates {
		score, match, ok := Match(query, c)
		if !ok {
			continue
		}
		out = append(out, Result{Index: i, Text: c, Score: score, Match: match})
	}
	// A stable insertion order and a stable comparison, so the same query twice
	// draws the same list: a finder whose rows swap under the cursor between
	// identical keystrokes is unusable.
	sortResults(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Scoring weights. They are relative to each other and to nothing else; what
// matters is the order they produce.
const (
	// bonusBoundary rewards a match at the start of a path segment or a word,
	// which is how people abbreviate: "iur" for internal/ui/review.go.
	//
	// It deliberately outranks a consecutive run. For prose the reverse would
	// be right, but these are paths, and the two ways people reach for one are
	// typing part of the file's name -- which lands on a boundary anyway -- or
	// typing the initials of the segments. Scoring runs higher than boundaries
	// makes the second kind lose to any file that merely happens to contain the
	// letters side by side: "iur" picks internal/gitx/incurred.go over
	// internal/ui/review.go on the strength of the "ur" in "incurred".
	bonusBoundary = 20
	// bonusConsecutive rewards a run of characters matched back to back, which
	// is what makes "modl" prefer model.go over m-o-d-scattered-l.
	bonusConsecutive = 10
	// bonusBasename rewards matching in the file's own name rather than in the
	// directories above it. You are looking for a file.
	bonusBasename = 6
	// penaltyGap is charged per character skipped between two matches, so a
	// tight match in a long path still beats a scattered one.
	penaltyGap = 2
)

// Match scores one candidate, reporting where the query landed in it.
//
// The query matches as a subsequence, case-insensitively, in two passes. The
// first runs forward and greedily, which finds the earliest position the query
// can finish at. The second runs backward from there, which slides every
// character as late as it will go and so produces the tightest match ending in
// the same place.
//
// The second pass is what makes the scores mean anything. Forward-greedy alone
// matches "rev" against internal/ui/review.go by taking the r out of
// "internal", scattering the match across the whole path and charging it the
// gap penalty for a file whose name is literally the query. Running back from
// the end collapses it onto "rev" in review.go, where it belongs.
//
// Neither pass is exhaustive -- a run later in the string could still score
// higher -- but both are linear, and this runs over every path in the repo on
// every keystroke.
func Match(query, candidate string) (score int, match []int, ok bool) {
	if query == "" {
		return 0, nil, true
	}
	lowQuery, lowCand := strings.ToLower(query), strings.ToLower(candidate)

	// Forward: where can the query first finish?
	at := 0
	for q := 0; q < len(lowQuery); q++ {
		i := strings.IndexByte(lowCand[at:], lowQuery[q])
		if i < 0 {
			return 0, nil, false
		}
		at += i + 1
	}

	// Backward: tighten every character toward that end.
	match = make([]int, len(lowQuery))
	end := at - 1
	for q := len(lowQuery) - 1; q >= 0; q-- {
		pos := strings.LastIndexByte(lowCand[:end+1], lowQuery[q])
		match[q] = pos
		end = pos - 1
	}

	basename := strings.LastIndexByte(candidate, '/') + 1
	prev := -1
	for _, pos := range match {
		switch {
		case prev >= 0 && pos == prev+1:
			score += bonusConsecutive
		case prev >= 0:
			score -= penaltyGap * (pos - prev - 1)
		}
		if isBoundary(candidate, pos) {
			score += bonusBoundary
		}
		if pos >= basename {
			score += bonusBasename
		}
		prev = pos
	}
	// Length is deliberately not scored. It is a tiebreaker in less() instead:
	// charged per character it swamps everything else, so a long name made of
	// exactly the right initials loses to a short one that merely contains the
	// letters -- parseStatusRecord.go losing "psr" to pastures.go.
	return score, match, true
}

// isBoundary reports whether the character at i starts a path segment, a word,
// or a run of capitals -- the places an abbreviation is built from.
func isBoundary(s string, i int) bool {
	if i == 0 {
		return true
	}
	prev, cur := s[i-1], s[i]
	switch prev {
	case '/', '_', '-', '.', ' ':
		return true
	}
	return isLower(prev) && isUpper(cur)
}

func isLower(b byte) bool { return b >= 'a' && b <= 'z' }
func isUpper(b byte) bool { return b >= 'A' && b <= 'Z' }

// sortResults orders by score, then by the shorter candidate, then by name.
// The last two are there to make the order total: any pair of candidates has a
// defined order, so the list cannot depend on the sort's internals.
//
// That totality is what lets this use an unstable sort. Rank's contract is that
// the same query twice draws the same list -- rows that swapped under the cursor
// between identical keystrokes would make the finder unusable -- and with no two
// distinct results comparing equal, the order is fixed by less() alone rather
// than by the order they happened to be appended in.
//
// This was an insertion sort, on the reasoning that the slice is a few thousand
// at worst and a handful once filtered. That holds for a repo the size of this
// one and breaks badly on a large one, because the filter does not filter: a
// one-character query is a subsequence of nearly every path there is. On a
// 24,860-path monorepo "i" matches 23,284 candidates, and the quadratic sort
// spent 512ms of the 515ms Rank took -- on the update goroutine, so the whole
// board sat frozen for half a second per keystroke, worst on the first one.
// Matching all 24,860 costs 3ms; sorting them here now costs 3ms as well.
func sortResults(rs []Result) {
	sort.Slice(rs, func(i, j int) bool { return less(rs[i], rs[j]) })
}

func less(a, b Result) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if len(a.Text) != len(b.Text) {
		return len(a.Text) < len(b.Text)
	}
	return a.Text < b.Text
}
