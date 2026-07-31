package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// This file is the worktree as a place to read from rather than a place git
// operates on: listing what is in it, reading one file out of it, and searching
// it. The review view needs all three to show anything other than a diff.

// maxFileBytes bounds what the pane will read into memory and hand to a
// highlighter. A minified bundle or a checked-in database is not a thing anyone
// reviews, and a pane that tries costs a redraw of the whole board.
const maxFileBytes = 2 << 20 // 2 MiB

// FileTooBigError says a file was left unread rather than truncated, because a
// prefix of a file read as if it were the file is the worse failure.
type FileTooBigError struct {
	Path string
	Size int64
}

func (e *FileTooBigError) Error() string {
	return fmt.Sprintf("%s is %s, too large to show", e.Path, humanBytes(e.Size))
}

// BinaryError says a file has no lines to show.
type BinaryError struct{ Path string }

func (e *BinaryError) Error() string { return e.Path + " is a binary file" }

// ReadFile reads one path out of a worktree.
//
// The path is taken as relative to the worktree and is checked to stay inside
// it. Every caller today passes something git itself produced -- a diff entry,
// an ls-files line, a grep hit -- but the pane is reachable from a fuzzy finder
// and a search box, and "the paths all come from git" is a property that holds
// until someone adds a fourth way in.
func ReadFile(wt, path string) ([]byte, error) {
	full, err := ResolveInWorktree(wt, path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	if info.Size() > maxFileBytes {
		return nil, &FileTooBigError{Path: path, Size: info.Size()}
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	// The same call git makes: a NUL early in the file means there are no lines
	// in it to show.
	if _, binary := countLines(data); binary {
		return nil, &BinaryError{Path: path}
	}
	return data, nil
}

// ResolveInWorktree turns a worktree-relative path into an absolute one,
// refusing anything that would leave the worktree.
//
// Symlinks are resolved before the check, so a link pointing out of the tree is
// refused rather than followed -- an agent's worktree is not a place to assume
// every link was put there on purpose.
func ResolveInWorktree(wt, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("no path")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("%s is an absolute path", path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the worktree", path)
	}
	full := filepath.Join(wt, clean)

	// EvalSymlinks fails on a path that does not exist, which is a real answer
	// here and not worth a second error message; Stat reports it next.
	realWT, err := filepath.EvalSymlinks(wt)
	if err != nil {
		realWT = wt
	}
	realFull, err := filepath.EvalSymlinks(full)
	if err != nil {
		return full, nil
	}
	rel, err := filepath.Rel(realWT, realFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s leads outside the worktree", path)
	}
	return realFull, nil
}

// ListFiles is every path in the worktree git would track or could be asked to:
// what is in the index, plus what is untracked and not ignored.
//
// Ignored files are left out, which is the whole reason this asks git rather
// than walking the tree. A repo's build output and its dependencies outnumber
// its source by orders of magnitude, and a file finder that offers them is a
// file finder nobody uses twice.
func ListFiles(ctx context.Context, wt string) ([]string, error) {
	out, err := RunRaw(ctx, wt, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

// Hit is one matching line found by a search of the worktree.
type Hit struct {
	Path string
	Line int
	// Text is the matching line, trimmed of trailing space. It is what the
	// result list shows, so the answer is readable without opening anything.
	Text string
}

// Grep searches the worktree for a literal string.
//
// Literal rather than a regular expression: the overwhelming majority of
// searches in code are for a name, and the failure mode of treating those as
// patterns is an error message about an unbalanced bracket in front of someone
// who typed a function signature.
//
// Ripgrep when it is installed and git grep when it is not -- the same bargain
// as delta. git grep is always available, since git is, so there is no third
// case where the key does nothing.
func Grep(ctx context.Context, wt, query string, limit int) ([]Hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	var args []string
	if hasRipgrep() {
		args = []string{"--line-number", "--no-heading", "--color=never", "--fixed-strings",
			"--smart-case", "--max-count=" + strconv.Itoa(limit), "-e", query}
	} else {
		// git grep has no smart-case, so it is spelled out: a query in all
		// lower case ignores case, one with a capital in it means that capital.
		args = []string{"grep", "--no-color", "-n", "-I", "--untracked", "--fixed-strings"}
		if query == strings.ToLower(query) {
			args = append(args, "-i")
		}
		args = append(args, "-e", query)
	}

	var out string
	var err error
	if hasRipgrep() {
		out, err = runTool(ctx, wt, "rg", args)
	} else {
		out, err = RunRaw(ctx, wt, args...)
	}
	// Both tools exit 1 to mean "nothing matched", which is an answer rather
	// than a failure. Output in hand beats any exit status, so the error is
	// only believed when there is nothing to show for it.
	if out == "" && err != nil {
		if exitCode(err) == 1 {
			return nil, nil
		}
		return nil, err
	}
	return parseGrep(out, limit), nil
}

// parseGrep reads "path:line:text", the one output format ripgrep and git grep
// agree on. It is split out from running either of them so it can be tested
// without ripgrep installed.
func parseGrep(out string, limit int) []Hit {
	var hits []Hit
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if len(hits) >= limit {
			break
		}
		// SplitN with three parts, because the matching text routinely contains
		// colons and only the first two fields are ours.
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			// A path with a colon in it shifts the fields along. Rather than
			// guess which colon was the separator, the row is dropped: a result
			// list that sends you to the wrong line is worse than one row short.
			continue
		}
		hits = append(hits, Hit{Path: parts[0], Line: n, Text: strings.TrimRight(parts[2], " \t\r")})
	}
	return hits
}

// HasRipgrep reports whether ripgrep is installed, for the line that says which
// search is running.
func HasRipgrep() bool { return hasRipgrep() }

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
