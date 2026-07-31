package gitx

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// Hunk is one contiguous change inside a file.
//
// The rendered diff in the pane is text: it can be scrolled but not reasoned
// about. Hunks are the same change as structure, which is what it takes to jump
// between the parts of a file that moved, and to tell an agent *where* to look
// rather than only which file.
type Hunk struct {
	// Start and End are line numbers in the file as it stands now, which is what
	// a path:line reference has to name for the agent to open the right place.
	Start int
	End   int
	Added int
	// Removed counts deleted lines, which have no line of their own in the new
	// file -- a hunk that only deletes still starts at the line the deletion sat
	// in front of.
	Removed int
	// Header is the context git puts after the @@ marker, usually the enclosing
	// function.
	Header string
	// Anchor is the first line this hunk changed, without its +/- marker. It is
	// how the hunk is found again in output that has been through delta, whose
	// hunk headers are its own invention and carry no line numbers we can trust.
	Anchor string
}

// Hunks parses the structure of one target's diff.
//
// It asks git for a plain patch of its own rather than reusing what the pane is
// showing: that has been through delta and is no longer a patch.
func Hunks(ctx context.Context, wt, base string, mode DiffMode, t DiffTarget) ([]Hunk, error) {
	var args []string
	if t.Untracked {
		args = []string{"diff", "--no-color", "--patch", "--no-index", "--", os.DevNull, t.Path}
	} else {
		args = []string{"diff", "--no-color", "--patch"}
		if rev := diffRange(ctx, wt, base, mode); rev != "" {
			args = append(args, rev)
		}
		if t.Path != "" {
			args = append(args, "--", t.Path)
		}
	}
	// --no-index exits 1 whenever the files differ, which is every time here, so
	// the status is ignored and the output is what matters.
	out, err := RunRaw(ctx, wt, args...)
	if out == "" && err != nil {
		return nil, err
	}
	return parseHunks(out)
}

// parseHunks turns a plain unified patch into hunks.
func parseHunks(patch string) ([]Hunk, error) {
	if strings.TrimSpace(patch) == "" {
		return nil, nil
	}
	files, _, err := gitdiff.Parse(strings.NewReader(patch))
	if err != nil {
		return nil, err
	}

	var hunks []Hunk
	for _, f := range files {
		for _, frag := range f.TextFragments {
			if frag == nil {
				continue
			}
			h := Hunk{
				Start:   int(frag.NewPosition),
				Added:   int(frag.LinesAdded),
				Removed: int(frag.LinesDeleted),
				Header:  strings.TrimSpace(frag.Comment),
			}
			// A hunk that only deletes occupies no lines in the new file, so it
			// collapses onto the line its deletion sat in front of.
			if frag.NewLines > 0 {
				h.End = h.Start + int(frag.NewLines) - 1
			} else {
				h.End = h.Start
			}
			h.Anchor = fragmentAnchor(frag)
			hunks = append(hunks, h)
		}
	}
	return hunks, nil
}

// fragmentAnchor is the first line a hunk changed. Context lines are skipped:
// they are by definition also present elsewhere in the file, so they are the
// worst possible thing to search for.
func fragmentAnchor(frag *gitdiff.TextFragment) string {
	for _, line := range frag.Lines {
		if line.Op == gitdiff.OpContext {
			continue
		}
		if text := strings.TrimRight(line.Line, "\r\n"); strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

// Ref is the path:line-range an agent can be pointed at.
func (h Hunk) Ref(path string) string {
	if h.End > h.Start {
		return fmt.Sprintf("%s:%d-%d", path, h.Start, h.End)
	}
	return fmt.Sprintf("%s:%d", path, h.Start)
}
