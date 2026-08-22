// Package convo finds the conversations a coding agent keeps on disk.
//
// It exists so a session that was started outside dma can be identified by the
// id its agent already shows for it, and so the two facts attaching needs --
// where the conversation was being held, and what it is about -- can be read
// without asking the user to retype either.
//
// The layouts here are the agents' own, not a format dma defines, so this
// package is deliberately forgiving: a transcript is scanned for the handful of
// records that carry those two facts and every other record shape is skipped.
// A release that renames a field costs a title, not the attach.
//
// Only agents dma has a reader for can be attached. That is a smaller set than
// the configured profiles, and it is keyed on the profile name rather than on
// anything in the profile, because a conversation store is a property of the
// agent binary and not of the command line a profile happens to use to launch
// it.
package convo

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Conversation is one recorded session of one agent.
type Conversation struct {
	// ID is the agent's own identifier for the conversation -- the value its
	// resume command takes.
	ID string
	// Profile is the agent profile that can reopen it.
	Profile string
	// Cwd is the directory the conversation was held in. It is what tells attach
	// which repository the work belongs to.
	Cwd string
	// Title is the opening prompt, or the agent's own name for the conversation
	// where it keeps one.
	Title string
	// Updated is when the transcript last grew, which is the only ordering that
	// matches what a user means by "the session I was just in".
	Updated time.Time
	// Path is the transcript file, reported so an error can name something the
	// user can go and look at.
	Path string
}

// Find returns the conversation an agent recorded under id.
func Find(profile, id string) (Conversation, error) {
	src, err := sourceFor(profile)
	if err != nil {
		return Conversation{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Conversation{}, fmt.Errorf("no session id given")
	}
	path, err := src.locate(id)
	if err != nil {
		return Conversation{}, err
	}
	if path == "" {
		return Conversation{}, &NotFoundError{Profile: profile, ID: id, Root: src.root()}
	}
	c, err := src.read(path)
	if err != nil {
		return Conversation{}, err
	}
	// The id is taken from the argument rather than from the file: for some of
	// these agents it is the filename that carries it, and a transcript that
	// records nothing usable would otherwise produce a conversation whose id
	// cannot be resumed.
	c.ID, c.Profile = id, profile
	return c, nil
}

// List returns an agent's most recently used conversations, newest first.
//
// It reads only as many transcripts as it reports. The file list is ordered by
// modification time first, which is a stat per candidate rather than a parse,
// so a machine with thousands of recorded sessions pays for the ones being
// shown and not for the rest.
func List(profile string, limit int) ([]Conversation, error) {
	src, err := sourceFor(profile)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	files, err := src.candidates()
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })

	out := make([]Conversation, 0, limit)
	for _, f := range files {
		if len(out) == limit {
			break
		}
		c, err := src.read(f.path)
		if err != nil || c.ID == "" {
			// A transcript that will not parse is one row missing from a list,
			// which is a better answer than refusing to show the other nine.
			continue
		}
		c.Profile = profile
		out = append(out, c)
	}
	return out, nil
}

// Supported names the agents attach can read conversations for, in the order
// they should be offered.
func Supported() []string { return []string{"claude", "codex", "pi"} }

// NotFoundError reports that an agent has no conversation under that id. It
// names the directory searched, since the usual cause is an id belonging to a
// different agent.
type NotFoundError struct {
	Profile string
	ID      string
	Root    string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s has no session %s recorded under %s", e.Profile, e.ID, e.Root)
}

// UnsupportedError reports a profile whose conversation store dma cannot read.
type UnsupportedError struct{ Profile string }

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("dma cannot read %s conversations; it knows how to attach %s",
		e.Profile, prose(Supported()))
}

// prose joins names the way a sentence does, so an error naming three agents
// does not read as "claude and codex and pi".
func prose(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// --- sources ---

// source is one agent's on-disk conversation store.
type source struct {
	// root is the directory transcripts live under, resolved at call time so a
	// test can move it with an environment variable.
	root func() string
	// locate finds the transcript holding one id, returning "" when there is
	// none. It is separate from candidates because every agent here can answer it
	// from a path or a filename, without reading anything.
	locate func(id string) (string, error)
	// candidates lists every transcript, for the recent list.
	candidates func() ([]candidate, error)
	// read parses one transcript.
	read func(path string) (Conversation, error)
}

type candidate struct {
	path string
	mod  time.Time
}

func sourceFor(profile string) (source, error) {
	switch profile {
	case "claude":
		return claudeSource(), nil
	case "codex":
		return codexSource(), nil
	case "pi":
		return piSource(), nil
	}
	return source{}, &UnsupportedError{Profile: profile}
}

// --- Claude Code ---

// claudeRoot is where Claude Code files transcripts: one directory per working
// directory the agent has been run in, holding one file per conversation named
// for its id.
func claudeRoot() string {
	if d := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); d != "" {
		return filepath.Join(d, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude/projects"
	}
	return filepath.Join(home, ".claude", "projects")
}

func claudeSource() source {
	return source{
		root: claudeRoot,
		locate: func(id string) (string, error) {
			// The id is the filename, so the whole search is one glob -- no
			// transcript is opened to find out whether it is the right one.
			// The directory it lands in is not consulted: those names are the
			// working directory with its separators replaced, which is a lossy
			// encoding and not something to resolve a path back out of.
			matches, err := filepath.Glob(filepath.Join(claudeRoot(), "*", safeGlob(id)+".jsonl"))
			if err != nil || len(matches) == 0 {
				return "", nil
			}
			return matches[0], nil
		},
		candidates: func() ([]candidate, error) {
			return statAll(filepath.Join(claudeRoot(), "*", "*.jsonl"))
		},
		read: readClaude,
	}
}

// readClaude pulls the working directory and a title out of a Claude Code
// transcript.
//
// Both come off the first user turn, which is at the front of the file. The
// agent's own generated title is preferred over that turn's text, since it is a
// name rather than the first sentence of a paragraph -- but it is written after
// the turn it describes, so finding it means reading past the answer already in
// hand. That is what titleLookahead bounds: the better title is worth some more
// records, and not worth reading a hundred-megabyte transcript to the end for.
func readClaude(path string) (Conversation, error) {
	c := Conversation{Path: path, ID: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	if info, err := os.Stat(path); err == nil {
		c.Updated = info.ModTime()
	}

	var rec struct {
		Type    string `json:"type"`
		Cwd     string `json:"cwd"`
		AITitle string `json:"aiTitle"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	seen := 0
	err := scanJSONL(path, func(line []byte) bool {
		seen++
		rec.Type, rec.Cwd, rec.AITitle = "", "", ""
		rec.Message.Content = nil
		if json.Unmarshal(line, &rec) != nil {
			return true
		}
		if c.Cwd == "" && rec.Cwd != "" {
			c.Cwd = rec.Cwd
		}
		switch rec.Type {
		case "ai-title":
			if t := strings.TrimSpace(rec.AITitle); t != "" {
				c.Title = t
				// The agent's own title is the best answer available, so
				// nothing later can improve on it.
				return c.Cwd == ""
			}
		case "user":
			if c.Title == "" {
				c.Title = firstUserText(messageText(rec.Message.Content))
			}
		}
		return c.Cwd == "" || c.Title == "" || seen < titleLookahead
	})
	return c, err
}

// messageText flattens the two shapes a message body arrives in: a bare string,
// or the list of typed blocks a turn with attachments produces.
func messageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// --- Codex ---

// codexRoot is where Codex files transcripts: one "rollout" file per session,
// filed under the date it started and named with its id.
func codexRoot() string {
	if d := strings.TrimSpace(os.Getenv("CODEX_HOME")); d != "" {
		return filepath.Join(d, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex/sessions"
	}
	return filepath.Join(home, ".codex", "sessions")
}

func codexSource() source {
	return source{
		root: codexRoot,
		// The id is the tail of the filename, but the date directories in front
		// of it are not derivable from the id, so this is a walk rather than a
		// glob. It reads directory entries only -- no transcript is opened.
		locate:     func(id string) (string, error) { return walkFor(codexRoot(), "-"+id+".jsonl") },
		candidates: func() ([]candidate, error) { return walkAll(codexRoot()) },
		read:       readCodex,
	}
}

// readCodex pulls the working directory and a title out of a Codex rollout.
//
// The directory is on the header record, which is the first line. The title
// comes from the event stream rather than from the model's own message list:
// the list opens with several injected turns that are addressed to the agent
// and were never typed by anyone, while the event stream records only what the
// user actually sent.
func readCodex(path string) (Conversation, error) {
	c := Conversation{Path: path}
	if info, err := os.Stat(path); err == nil {
		c.Updated = info.ModTime()
	}

	var rec struct {
		Type    string `json:"type"`
		Payload struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
			Cwd       string `json:"cwd"`
			Message   string `json:"message"`
		} `json:"payload"`
	}
	err := scanJSONL(path, func(line []byte) bool {
		rec.Type = ""
		rec.Payload.Type, rec.Payload.SessionID, rec.Payload.Cwd, rec.Payload.Message = "", "", "", ""
		if json.Unmarshal(line, &rec) != nil {
			return true
		}
		switch {
		case rec.Type == "session_meta":
			if c.Cwd == "" {
				c.Cwd = rec.Payload.Cwd
			}
			if c.ID == "" {
				c.ID = rec.Payload.SessionID
			}
		case rec.Type == "event_msg" && rec.Payload.Type == "user_message":
			if c.Title == "" {
				c.Title = firstUserText(rec.Payload.Message)
			}
		}
		return c.Cwd == "" || c.Title == "" || c.ID == ""
	})
	if c.ID == "" {
		// The header is the only record carrying the id, so a rollout missing
		// one still has it in the filename: rollout-<timestamp>-<id>.jsonl.
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if i := strings.LastIndex(base, "-"); i >= 0 && len(base) > i+1 {
			c.ID = base[i+1:]
		}
	}
	return c, err
}

// --- pi ---

// piRoot is where pi files sessions: one directory per working directory the
// agent has been run in, each holding one file per session named for the moment
// it started and the id it was given.
//
// A configured session directory is taken as it is rather than having "sessions"
// appended, because that is what pi does with it -- and files land in it directly
// rather than under a directory per working directory. Nothing below depends on
// which of the two layouts it is looking at: both are searched by walking.
func piRoot() string {
	if d := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_SESSION_DIR")); d != "" {
		return d
	}
	if d := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); d != "" {
		return filepath.Join(d, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pi/agent/sessions"
	}
	return filepath.Join(home, ".pi", "agent", "sessions")
}

func piSource() source {
	return source{
		root:       piRoot,
		locate:     func(id string) (string, error) { return walkFor(piRoot(), "_"+id+".jsonl") },
		candidates: func() ([]candidate, error) { return walkAll(piRoot()) },
		read:       readPi,
	}
}

// readPi pulls the working directory, the id and a title out of a pi session
// file.
//
// The first two are on the header, which is the first line. The title prefers the
// name the session was given over its opening prompt, for the reason readClaude
// prefers a generated title: a name is a name, where a prompt is the first
// sentence of a paragraph. A session can be renamed at any point, so the scan
// goes on past a usable answer looking for a later one, bounded by
// titleLookahead.
func readPi(path string) (Conversation, error) {
	c := Conversation{Path: path}
	if info, err := os.Stat(path); err == nil {
		c.Updated = info.ModTime()
	}

	var rec struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Cwd     string `json:"cwd"`
		Name    string `json:"name"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	seen := 0
	err := scanJSONL(path, func(line []byte) bool {
		seen++
		rec.Type, rec.ID, rec.Cwd, rec.Name = "", "", "", ""
		rec.Message.Role, rec.Message.Content = "", nil
		if json.Unmarshal(line, &rec) != nil {
			return true
		}
		switch rec.Type {
		case "session":
			// The header, and the only record whose id names the session: every
			// entry after it carries an id of its own, which is its place in the
			// tree the session is stored as.
			if c.Cwd == "" {
				c.Cwd = rec.Cwd
			}
			if c.ID == "" {
				c.ID = rec.ID
			}
		case "session_info":
			// A rename, and the later one wins. An entry that clears the name is
			// left alone rather than clearing the title: a card with no name on it
			// is worse than one carrying the name it had a moment ago.
			if name := strings.TrimSpace(rec.Name); name != "" {
				c.Title = name
			}
		case "message":
			if c.Title == "" && rec.Message.Role == "user" {
				c.Title = firstUserText(messageText(rec.Message.Content))
			}
		}
		return c.Cwd == "" || c.ID == "" || c.Title == "" || seen < titleLookahead
	})
	if c.ID == "" {
		// A file whose header did not parse still has the id in its name:
		// <timestamp>_<id>.jsonl, where the timestamp has had its colons and
		// dots replaced and so carries no underscore of its own.
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if i := strings.LastIndex(base, "_"); i >= 0 && len(base) > i+1 {
			c.ID = base[i+1:]
		}
	}
	return c, err
}

// --- shared search ---

// walkFor is the path of the first file under root whose name ends in suffix, or
// "" when there is none.
//
// The suffix is compared rather than matched, so an id carrying a character a
// glob would read as a pattern fails to find anything rather than finding
// something else.
func walkFor(root, suffix string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory somewhere in the tree is not a reason to
			// abandon the search of the rest of it.
			return nil //nolint:nilerr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), suffix) {
			return nil
		}
		found = p
		return fs.SkipAll
	})
	if os.IsNotExist(err) {
		return "", nil
	}
	return found, err
}

// walkAll lists every transcript under root with its modification time. A store
// that does not exist yet is an empty list rather than an error: an agent that
// has never been run is not a failure to search.
func walkAll(root string) ([]candidate, error) {
	var out []candidate
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err == nil {
			out = append(out, candidate{path: p, mod: info.ModTime()})
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return out, err
}

// --- shared parsing ---

const (
	// maxLineBytes bounds one record. Transcripts carry pasted files and encoded
	// images inline, so a single line can be megabytes; anything past this is
	// skipped rather than buffered, and none of the records this package wants
	// is ever near it.
	maxLineBytes = 1 << 20
	// maxScanBytes bounds the whole read. Everything being looked for is in the
	// opening turns, so a long conversation is not a reason to read a hundred
	// megabytes off disk.
	maxScanBytes = 8 << 20
	// titleLookahead is how far past a usable answer the scan goes on looking
	// for a better one. See readClaude.
	titleLookahead = 500
)

// scanJSONL calls fn with each record of a JSON-lines file until fn returns
// false or the budget above runs out.
func scanJSONL(path string, fn func(line []byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64<<10)
	read := 0
	for read < maxScanBytes {
		line, err := readLine(r)
		read += len(line)
		if len(line) > 0 && len(line) <= maxLineBytes {
			if !fn(line) {
				return nil
			}
		}
		if err != nil {
			return nil
		}
	}
	return nil
}

// readLine returns one line, discarding the tail of any line past the record
// limit so the reader stays aligned on the next one.
func readLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(out)+len(chunk) <= maxLineBytes {
			out = append(out, chunk...)
		} else {
			out = out[:0]
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return out, err
	}
}

// firstUserText reduces an opening prompt to something that can name a card.
//
// A prompt that opens with a tag is skipped: these agents inject context of
// their own into the turn -- available plugins, the expansion of a slash
// command, the contents of an attached file -- and those arrive as user text with
// markup around them. What the person typed is the first line that is not one of
// those.
func firstUserText(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "<") {
			continue
		}
		return line
	}
	return ""
}

// statAll expands a glob into candidates with their modification times.
func statAll(pattern string) ([]candidate, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	out := make([]candidate, 0, len(matches))
	for _, p := range matches {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		out = append(out, candidate{path: p, mod: info.ModTime()})
	}
	return out, nil
}

// safeGlob neutralizes the pattern characters a session id should never
// contain, so an id typed with a bracket in it fails to match rather than
// matching something else.
func safeGlob(s string) string {
	return strings.NewReplacer("*", `\*`, "?", `\?`, "[", `\[`, "]", `\]`, `\`, `\\`).Replace(s)
}
