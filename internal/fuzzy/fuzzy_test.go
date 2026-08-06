package fuzzy

import (
	"fmt"
	"strings"
	"testing"
)

func TestMatchRejectsNonSubsequences(t *testing.T) {
	for _, tc := range []struct{ query, candidate string }{
		{"xyz", "internal/ui/review.go"},
		{"oger", "internal/ui/review.go"}, // right letters, wrong order
		{"reviewz", "internal/ui/review.go"},
	} {
		if _, _, ok := Match(tc.query, tc.candidate); ok {
			t.Errorf("%q matched %q", tc.query, tc.candidate)
		}
	}
}

func TestMatchReportsWhereItLanded(t *testing.T) {
	_, match, ok := Match("rev", "internal/ui/review.go")
	if !ok {
		t.Fatal("rev did not match review.go")
	}
	const path = "internal/ui/review.go"
	got := ""
	for _, i := range match {
		got += string(path[i])
	}
	if got != "rev" {
		t.Errorf("matched positions spell %q, want rev", got)
	}
	// And they are the first ones that spell it, taken left to right.
	if want := strings.Index(path, "rev"); match[0] != want {
		t.Errorf("first match at %d, want %d", match[0], want)
	}
}

// The ordering is the whole product. Each case is a query and the candidate a
// person typing it is looking for, against a plausible distractor.
func TestRankPrefersWhatWasMeant(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		want, over string
	}{
		{
			name:  "a run beats scattered letters",
			query: "review", want: "internal/ui/review.go", over: "internal/render/verify_ok.go",
		},
		{
			name:  "the file's own name beats the directories above it",
			query: "ui", want: "internal/ui.go", over: "internal/ui/panel.go",
		},
		{
			name:  "segment initials are how paths get abbreviated",
			query: "iur", want: "internal/ui/review.go", over: "internal/gitx/incurred.go",
		},
		{
			name:  "a shorter path wins a tie",
			query: "model", want: "internal/ui/model.go", over: "internal/ui/deep/model.go",
		},
		{
			name:  "camel humps count as boundaries",
			query: "psr", want: "parseStatusRecord.go", over: "pastures.go",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Rank(tc.query, []string{tc.over, tc.want}, 10)
			if len(got) == 0 {
				t.Fatalf("%q matched nothing", tc.query)
			}
			if got[0].Text != tc.want {
				t.Errorf("%q ranked %q first, want %q (scores: %v)",
					tc.query, got[0].Text, tc.want, scores(got))
			}
		})
	}
}

func scores(rs []Result) map[string]int {
	out := map[string]int{}
	for _, r := range rs {
		out[r.Text] = r.Score
	}
	return out
}

// An empty query opens the finder on the file list rather than on nothing.
func TestRankEmptyQueryKeepsOrder(t *testing.T) {
	in := []string{"b.go", "a.go", "c.go"}
	got := Rank("", in, 10)
	if len(got) != 3 {
		t.Fatalf("got %d results, want all 3", len(got))
	}
	for i, r := range got {
		if r.Text != in[i] {
			t.Errorf("result %d = %q, want %q", i, r.Text, in[i])
		}
	}
}

func TestRankHonorsTheLimit(t *testing.T) {
	var in []string
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		in = append(in, n+"go.go")
	}
	if got := Rank("go", in, 2); len(got) != 2 {
		t.Errorf("got %d results, want the limit of 2", len(got))
	}
	if got := Rank("", in, 2); len(got) != 2 {
		t.Errorf("empty query returned %d, want the limit of 2", len(got))
	}
}

// Index gets the caller back to whatever it was carrying alongside the string.
func TestRankReportsTheOriginalIndex(t *testing.T) {
	in := []string{"zero.go", "one.go", "two.go"}
	got := Rank("one", in, 10)
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if got[0].Index != 1 {
		t.Errorf("index = %d, want 1", got[0].Index)
	}
}

// The same query twice has to draw the same list, or rows swap under the cursor
// between identical keystrokes.
func TestRankIsStable(t *testing.T) {
	in := []string{"a/x.go", "b/x.go", "c/x.go", "d/x.go"}
	first := Rank("x", in, 10)
	for i := 0; i < 5; i++ {
		again := Rank("x", in, 10)
		for j := range first {
			if first[j].Text != again[j].Text {
				t.Fatalf("run %d differs at %d: %q vs %q", i, j, first[j].Text, again[j].Text)
			}
		}
	}
}

func TestMatchIsCaseInsensitive(t *testing.T) {
	if _, _, ok := Match("README", "readme.md"); !ok {
		t.Error("upper-case query did not match a lower-case path")
	}
	if _, _, ok := Match("readme", "README.md"); !ok {
		t.Error("lower-case query did not match an upper-case path")
	}
}

// A one-character query is a subsequence of nearly every path there is, so the
// filter that Rank's ordering was once assumed to run behind does not filter at
// all. This is the shape that made the sort quadratic; the benchmark is here so
// the next person to change the ordering can see what it costs at scale.
func BenchmarkRankWideMatch(b *testing.B) {
	paths := make([]string, 0, 25000)
	for i := 0; i < 25000; i++ {
		paths = append(paths, fmt.Sprintf("packages/svc-%d/src/internal/handler_%d.go", i%40, i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Rank("i", paths, 200)
	}
}
