# dma

`dma` runs and monitors multiple AI coding-agent sessions from one terminal.
Each task gets its own git worktree and persistent tmux session, while the board
shows which agents need attention and the state of their GitHub pull requests.

Use it when you want to run several Claude Code, Codex, pi, or other
command-line coding agents in parallel without manually creating worktrees,
switching terminals, or checking every session for progress.

Agents keep running when you quit the board.

```text
┏━ idle 2 · waiting on you ━━━━━┓ ╭─ active 1 · agent working ────╮ ╭─ pr open 1 · pushed ──────────╮ ╭─ merged · done ───────────────╮
┃ ▌ rate limiter                ┃ │ ▌ token refresh               │ │ ▌ session cookies             │ │   —                           │
┃ ▌ feat/rate-limiter           ┃ │ ▌ feat/token-refresh          │ │ ▌ #412 ✓ ci ✓ approved        │ │                               │
┃ ▌ ◆ needs you 8m              ┃ │ ▌ ● working 4m                │ │ ▌ ○ idle 1h20m                │ │                               │
┃ ▌ Bash(rm -rf build)          ┃ │ ▌ +212 −38                    │ │ ▌ +88 −12                     │ │                               │
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛ ╰───────────────────────────────╯ ╰───────────────────────────────╯ ╰───────────────────────────────╯
╭─ rate limiter · dma-cli-rate-limiter ──────────────────────────────────────────────────────────────────────────────────────────────╮
│  ▾ agent claude   ▾ repo dma-cli                                                                          ▾ project all projects  │
│ ────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────── │
│ The agent's live terminal appears here.                                                                                            │
│ ❯ press i to describe a task for a new agent session                                                                               │
╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

## Support and requirements

`dma` supports macOS and Linux. Windows is not currently supported.

Required at runtime:

| Tool | Purpose |
|---|---|
| `git` | Creates worktrees and manages branches and diffs |
| `tmux` | Hosts persistent agent sessions |
| A coding-agent CLI | `claude`, `codex`, `pi`, or another configured command |
| `terminal-notifier` | Desktop notifications on macOS |

Install and authenticate at least one coding-agent CLI before starting a
session. `dma` includes profiles for Claude Code, Codex and pi, and uses Claude
Code by default. If you have one of the others installed instead, select it with
`A` before starting your first task.

On macOS, install the notifier:

```sh
brew install terminal-notifier
```

Notifications are how a session that needs you reaches you when the board is not
on screen, which is what makes a board of parallel agents worth having. Without
`terminal-notifier`, `dma` falls back to AppleScript: notifications still
arrive, but macOS attributes them to Script Editor, so they carry its icon,
clicking one opens Script Editor rather than returning you to the board, and they
take focus. The board says so once on launch and `dma doctor` treats it as an
incomplete setup.

For GitHub pull-request features, also install `gh` and authenticate it:

```sh
gh auth login
```

The board can run without `gh`, but PR status, link, and merge features will not
be available. `dma doctor` treats missing GitHub integration as an incomplete
setup. `s` and `S` ask the agent to open the pull request, so the agent needs
`gh` too.

Optional tools:

| Tool | Purpose |
|---|---|
| `delta` | Improved diff rendering, and the side-by-side layout |
| `ripgrep` (`rg`) | Faster worktree search; `git grep` is used without it |
| `notify-send` | Desktop notifications on Linux |
| `wl-copy`, `xclip`, or `xsel` | Copying PR links on Linux |
| `xdg-open` | Opening PRs in a browser on Linux |

## Install

The source installation requires Go 1.26.5 or newer.

```sh
go install github.com/dma1dma1/dma-cli/cmd/dma@latest
```

Make sure Go's binary directory is on your `PATH`. For a standard Go
installation:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```

To build from a clone instead:

```sh
git clone https://github.com/dma1dma1/dma-cli.git
cd dma-cli
mkdir -p "$HOME/bin"
go build -o "$HOME/bin/dma" ./cmd/dma
```

Add `$HOME/bin` to your `PATH` if it is not already there.

Verify the installation:

```sh
command -v dma
command -v claude || command -v codex || command -v pi
dma doctor
dma version
```

## Update

`dma` is not released under version tags, so an upgrade means taking whatever
is currently on `main`. Re-run the command you installed with:

```sh
go install github.com/dma1dma1/dma-cli/cmd/dma@latest
```

Or, from a clone:

```sh
git pull && go build -o "$HOME/bin/dma" ./cmd/dma
```

`dma version` names the commit the binary was built from, which is how you
confirm the upgrade landed:

```text
dma v0.0.0-20260731073902-3d57053a8f79
```

Because there are no tags, `@latest` means the newest commit on `main`, and the
Go module proxy caches that answer for a few minutes. If `dma version` still
reports the build you already had, ask for the commit directly instead of
waiting:

```sh
GOPROXY=direct go install github.com/dma1dma1/dma-cli/cmd/dma@latest
```

Upgrading does not disturb sessions that are already running: agents live in
their own tmux sessions, so quit the board with `q`, install, and start it
again.

Your configuration is carried forward rather than replaced. A newer `dma` adds
built-in agent profiles that `~/.dma/config.json` has never heard of, and moves
a built-in profile onto a new default command only when you never edited it. An
edited command, a wrapper script, or a different binary is a deliberate choice
and is left alone. Existing worktrees keep the Claude hook configuration written
when they were created, so a change to the hooks reaches a session only when you
start a new one.

## Start your first session

Run `dma` from an existing git checkout:

```sh
cd ~/code/my-project
dma
```

No per-repository configuration is required. On first launch, `dma` registers
the repository you are in and selects it for new sessions.

In the board:

1. Press `A` if you want to change the selected agent.
2. Press `i` and describe the task.
3. Optionally press `ctrl-v` to attach an image from the clipboard.
4. Press `enter`. The agent starts in the background, leaving the panel on the
   session you were already watching.

In the task input, `ctrl-v` attaches a clipboard image or pastes ordinary text.
Pasted text keeps its line breaks. Use `shift-enter` to type a line break, or
`alt-enter` or `ctrl-j` if your terminal reports `shift-enter` as plain enter.
Press `ctrl-u` to start the task over: it clears the text and any attached
images.
Press `backspace` at the start of the input to remove the last attached image.

Describe the task in as much detail as the agent needs. The whole description is
sent to the agent. The card shows that text at first and renames itself to a
short summary of it a few seconds later.

`dma` fetches the configured base branch, creates a detached worktree under
`~/.dma/worktrees`, prepares it, starts a tmux session, and launches the agent
with your task. The worktree is named from the opening of the description, and
keeps that name after the card is renamed.

The agent is responsible for creating and naming its branch. Until it does, the
card displays `no branch` and there is nothing for `dma` to track a pull request
against.

### Starting a session in the background

`enter` starts the agent and leaves the panel showing the session you were
already watching.

Sessions start in the background because a start is not instant: the fetch and
the worktree take seconds, so moving the panel to the new session would move it
whenever the start finishes — usually once you have gone back to reading another
agent. Lining up work therefore costs you nothing of the session in front of you.
The card appears immediately as `preparing`, while dma fetches the base branch,
creates the worktree, and prepares its dependencies. It changes to the agent's
live state when the terminal is ready; the notice line names what started since
nothing else on screen moves. Select the card when you want to watch it.

An empty panel is the exception: with no session to be pulled away from, the
pending card fills it immediately and explains what it is waiting for.

### Attaching a session you already started

`dma attach` takes a conversation you are already having with an agent — one you
started in an ordinary terminal, before you thought to put it on the board — and
gives it a worktree and a card.

```sh
dma attach claude                   # list your recent claude conversations
dma attach claude 033386ad-09ec-40e3-af51-348bc2b680ef
dma attach codex 019ff3ca-7e37-7b61-9c5e-b2eb790ab560
dma attach pi 01a026e1-2d8d-797c-81fe-28cb9e09b760
```

The conversation is not restarted: the agent comes back knowing everything it
knew a moment ago. What changes is where it is standing — it moves from wherever
you were working into a worktree of its own, alongside every other session on
the board.

For Claude Code and Codex the same conversation is reopened by id. pi is
different, because a pi session records the directory it was started in and runs
its tools there wherever you resume it from — reopening one in a new worktree
would leave the agent editing the checkout it came from. So an attached pi
conversation is copied into the worktree instead, with its full history, under a
new id `dma` chooses. The card says `forked from` when that happened. The
original is left exactly as it was, and turns taken on the board do not appear
in it.

Because a conversation that has been running for a while has usually been
editing files, the work in progress moves with it. The new worktree is cut from
the commit your directory is sitting on, and that directory's uncommitted
changes — modified files, new files, deletions — are replayed into it. Ignored
paths such as `node_modules` are not copied; the usual worktree setup provides
those. Pass `-clean` to skip all of this and start from the base branch instead.

**The directory you were working in is never modified.** It keeps its files, its
branch and its own copy of the work. Note the other side of that: after
attaching, the work exists in two places, and edits in one do not reach the
other. Carry on in the worktree, and treat the original as the copy you left
behind.

The repository is worked out from where the conversation was running, and
registered if `dma` has not seen it before. Use `-repo <id>` to override that,
which is also what to do when the conversation was not being held inside a
repository at all.

| Flag | Effect |
|---|---|
| `-repo <id>` | Cut the worktree in this repository instead of the inferred one |
| `-project <name>` | File the session under a project |
| `-title <text>` | Name the card yourself instead of using the opening prompt |
| `-clean` | Start from the base branch, carrying nothing over |

If the board is already open in another terminal, the attached session appears
on it within one poll interval. Otherwise run `dma` and it will be there.

Only agents `dma` can both read the conversations of and open one in a new
worktree can be attached — Claude Code, Codex and pi out of the box. A custom
profile needs a `resume_id_command`, or a `fork_command` if it behaves the way pi
does; see [Agent permissions and profiles](#agent-permissions-and-profiles).

Session ids come from the agent: `/status` in Claude Code, the header line in
Codex, `/session` in pi. `dma attach <agent>` with no id lists recent
conversations with their ids, directories and opening prompts, which is usually
the faster way to find one.

### Repository expectations

The checkout must be a git repository. An `origin` remote is strongly
recommended:

- The base branch is read from `origin/HEAD`, then falls back to `main` or
  `master`.
- New sessions start from a freshly fetched `origin/<base>` when available.
- GitHub PR features require an origin that `dma` can identify as
  `owner/repository`.

If fetching fails, `dma` warns and uses the last locally available ref so work
can still start offline.

## Automatic worktree setup

When a repository is registered, `dma` looks for ignored dependency trees and
local configuration that a fresh worktree would otherwise be missing.

It handles detected paths in three ways:

- **Symlinked and shared:** caches and vendored source whose contents do not
  name their own location, such as `.pnpm-store`, `.yarn/cache`, `vendor`,
  `.bundle`, and `.terraform`. One copy on disk serves every worktree.
- **Cloned per worktree:** dependency trees that record the absolute path they
  were built for — `node_modules`, `.venv`, `venv`, and `.tox`. Each worktree
  gets a private copy, made with copy-on-write APFS clones where the platform
  offers them, so a large tree costs seconds and no initial file-data copy. The
  clone is issued in bounded chunks rather than as one call over the whole tree,
  so a monorepo's node_modules does not hold the filesystem for the duration and
  the rest of the desktop stays responsive. Other platforms fall back to a
  recursive copy.
- **Copied per worktree:** local configuration such as `.env`, `.env.local`,
  and related files.

Dependency trees are cloned rather than shared because they are content *plus
the path they were materialized at*. A Python venv records its own location in
`pyvenv.cfg` and the source directory of every editable install in a `.pth`
file, so a shared venv is rewritten to point at whichever worktree activated
last — taking the main checkout's imports with it. pnpm resolves its root
`node_modules` through symlinks, finds the virtual store where another checkout
left it, and offers to delete and reinstall the tree that every other worktree
is reading.

A repository's own toolchain cache is deliberately left alone. Those caches hold
the hash files an activation hook checks to decide whether to install anything,
so handing a worktree a populated one tells the hook its work is already done —
and the step it then skips is the one that would point a cloned venv at this
worktree's sources. A slow first activation is the cheaper mistake.

Detection also covers common monorepo directories such as `packages/*`,
`apps/*`, and `services/*`. Tracked files are not bootstrapped because git
already places them in every worktree.

Tracked `.envrc` files are authorized before the agent starts when the file is
byte-for-byte identical to the registered checkout's `.envrc` and that checkout
is already trusted by direnv. A changed or untrusted `.envrc` stays blocked so
newly fetched shell code is never approved automatically.

The registration notice summarizes what was detected:

```text
registered devops-copilot — shares .pnpm-store, clones .venv, node_modules +40 more, copies .env
```

Review this behavior before starting agents in repositories with sensitive
configuration:

- Copied `.env` files may contain secrets that the selected agent can access.
- Symlinked directories are shared, so changes made by one worktree are visible
  to the others.

Detected paths are stored in `~/.dma/config.json`. You can edit the repository's
`bootstrap.symlink`, `bootstrap.clone` and `bootstrap.copy` lists, or register a
repository explicitly:

```sh
dma repo add --clone node_modules,.venv --symlink .pnpm-store --copy .env ~/code/my-project
```

A configuration written before cloning existed lists dependency trees under
`bootstrap.symlink`. They are moved to `bootstrap.clone` when the config loads,
so an already-registered repository picks this up without being re-registered.
Paths that are not recognized dependency trees stay where you put them.

Bootstrap paths and the Claude hook settings installed by `dma` are added to
the repository's local git exclude file so they do not make worktrees appear
dirty.

Cloned trees are large enough that deleting them is the slow half of a prune: a
worktree of a monorepo with 42 configured trees holds around 347k files, and
unlinking them takes half a minute. So `x` and `X` do not wait for it. The
worktree is renamed into `~/.dma/worktrees/<repo>/.trash`, which takes
milliseconds and is what frees the board, and the files are unlinked afterwards
in the background. The trash is also swept when `dma` starts, so a prune
interrupted by a quit leaves nothing permanent behind.

## Agent permissions and profiles

The built-in profiles are:

```json
[
  {
    "name": "claude",
    "command": "claude --permission-mode auto",
    "resume_command": "claude --permission-mode auto --continue",
    "resume_id_command": "claude --permission-mode auto --resume {session}",
    "hooks": true
  },
  {
    "name": "codex",
    "command": "codex",
    "image_argument": "--image {path}",
    "resume_command": "codex resume --last",
    "resume_id_command": "codex resume {session}",
    "hooks": false
  },
  {
    "name": "pi",
    "command": "pi -a --",
    "image_argument": "@{path}",
    "resume_command": "pi -a -c",
    "resume_id_command": "pi -a --session {session}",
    "fork_command": "pi -a --fork {session} --session-id {new}",
    "hooks": false
  }
]
```

Claude Code is the default and starts with `--permission-mode auto` so parallel
sessions can make progress without stopping at every ordinary permission
prompt. Review whether that permission mode is appropriate for your environment
before using it. You can change the command in `~/.dma/config.json`.

pi needs no permission flag: it has no per-tool approval prompt, so a pi session
never stops to ask before running a command. Its `-a` answers a different
question — whether to trust the project's own `.pi` settings, extensions and
skills — which pi would otherwise ask at startup, before the opening prompt is
read. Change it to `-na` if you would rather those stayed unloaded. Either way,
review whether an agent that never asks is appropriate for your environment.

Claude Code reports its state through hooks installed only in worktrees created
by `dma`. Codex, pi and custom profiles are read off their terminal instead, in
this order of trust: the agent's own interrupt hint (`esc to interrupt`) says a turn
is running, a menu with a selection marker on one row or a line naming a key to
press means `needs you`, and for an agent that shows neither, a pane quiet for 25
seconds means the turn ended. It is a good signal rather than an exact one — a
turn shorter than one poll can pass unnoticed.

A hook is exact but it is not durable: it is an HTTP post to the running board,
so an agent that finishes its turn while the board is restarting has its `Stop`
refused, and the transition is lost. What is left on disk is the last event that
did land, which is why a card could come back from a restart claiming an agent
was working and stay that way for good. On launch, a hook-backed session found
in `working` is read off its terminal until a hook confirms it, and the first
one to arrive hands the session back to its own reports. Nothing else is
second-guessed, because nothing else strands: an agent behind an `idle` card is
running and reports within seconds, and one behind `needs you` is blocked on a
question, which is what the badge says.

`resume_command` is what `c` and `C` run to bring an agent back in a worktree it
has already worked in — see [Restarting sessions](#restarting-sessions). It must
identify the conversation from the working directory alone, which all three
built-in resume commands do: `claude --continue` continues the most recent
conversation in the current directory, `codex resume --last` picks the most
recent session in it unless asked for `--all`, and `pi -c` looks only in the
directory it is run in. That is what makes restarting a whole board at once
correct, since each session has a worktree of its own.

`resume_id_command` names one conversation instead of a directory, with
`{session}` replaced by its id. It is what [`dma attach`](#attaching-a-session-you-already-started)
runs for Claude Code and Codex, and what `c` and `C` run for any session that was
attached. The distinction
matters because an attached conversation's transcript stays filed under the
directory it began in, even after the agent is resumed elsewhere — so
"the most recent conversation here" finds nothing in the worktree `dma` made for
it, and only the id reaches across. A profile without a `resume_id_command`
cannot be attached at all: falling back to a plain launch would put an agent with
no memory of the task behind a card named after your conversation.

`fork_command` is for an agent that reopens a conversation in the directory the
conversation remembers rather than the one it is run in. pi does, so attaching a
pi conversation by id would leave the agent working in the checkout it came from
while the new worktree sat empty. The fork line copies the history into the
worktree instead: `{session}` is the conversation copied from, and `{new}` the id
`dma` mints for the copy, so later restarts can name it. Attach prefers this line
over `resume_id_command` where a profile has both, and a restart never runs it —
forking again would abandon everything the session had done since.

You can add another agent by adding an entry to `agent_profiles`. The command
runs inside the new worktree. The task is appended as a positional argument;
use `{prompt}` in the command if it needs to appear somewhere else. Add a
`resume_command` if the agent can continue a previous conversation; without one,
a restart launches `command` instead and the board says the agent came back
without its history. Add a `resume_id_command` too if it can reopen a
conversation by id, which is what makes the agent attachable — though `dma` also
has to know where that agent records its conversations, which today means Claude
Code, Codex and pi.

For images attached to a new session, `image_argument` is repeated once per
image and `{path}` is replaced with the shell-quoted path to its staged PNG.
Codex uses `--image {path}` and pi `@{path}`. Profiles without an
`image_argument`, including Claude Code, receive the image paths in their
opening prompt.

## Everyday controls

| Key | Action |
|---|---|
| `h` `j` `k` `l` | Move between cards and columns; a full column scrolls to keep the cursor in view |
| `i` or `n` | Start composing a new task |
| `t` | Type into the selected agent from the session panel |
| `ctrl-v` | Paste an image or text into a task or live agent |
| `ctrl-u` | Clear the task input while composing |
| `enter` | Start the composed task in the background, leaving the panel where it is |
| `a` | Attach to the selected tmux session |
| `enter` or `d` | Review the selected session's changes |
| `s` | Ask the agent to commit, push, and open a pull request |
| `S` | Ship, then shepherd CI and review feedback until the pull request is ready |
| `o` / `y` | Open or copy the pull-request link |
| `m` | Merge the pull request, enable auto-merge while CI is pending, or add it to the merge queue |
| `x` | Prune one session's worktree and branch, closing its pull request if it is still open |
| `X` | Prune the merged sessions currently shown |
| `c` | Restart the selected session's agent where it left off |
| `C` | Restart every shown session whose terminal is gone |
| `D` | Kill the agent but keep its worktree |
| `A` | Choose the agent used for new sessions |
| `r` | Switch, add, or unregister repositories |
| `p` | Choose a project filter |
| `G` | Move the selected session to a project |
| `R` | Refresh session and PR state |
| `?` | Show the complete in-app help — type in it to search the keymap |
| `q` | Quit; agents continue running |

When typing directly into the session panel or an attached tmux session, every
key—including `esc`—goes to the agent. The panel's frame lights up in the focus
color while it has the keyboard, and its title bar says which key gets you out.
Press `ctrl-q` to return control to the board. While the panel has focus or the
session is attached, the mouse wheel scrolls through the agent's history. In the
panel, drag across text to select and copy it; `dma` holds that highlight steady
while the live agent redraws. In an attached session, hold `shift`—or `option` in
some macOS terminals—to use the terminal's native selection. `dma` restores your
tmux mouse setting when you detach.

If the agent's composer is modal—Codex with `tui.vim_mode_default`, which returns
to normal mode after every message it sends—`dma` puts it back into insert mode
when you hand it the keyboard, so a sentence typed into the panel arrives as text
rather than as vim commands. Nothing is sent to an agent that is not modal, or
while a dialog is open, so answering a prompt with `1` or `y` still works.

## Restarting sessions

Agents survive quitting the board, but they do not survive the machine: tmux
takes every session with it on a restart, so `dma` comes back to a board of cards
whose worktrees are all still there and whose agents are all gone. Those cards
read `⚠ not running`, the session panel says the terminal is gone, and the board
offers the count once on launch.

Press `C` to restart every shown session that is not running, or `c` for the
selected one. Each gets its terminal back at the same name, in the same worktree,
and its agent resumes the conversation it was having there — the branch, the
commits, the uncommitted work and the agent's own history are all still on disk.
Nothing is fetched, rebased or bootstrapped again: a restart runs the agent, and
touches nothing else.

The restarted agent comes up with the floor open rather than mid-task. It knows
what it was doing, so telling it to carry on is a sentence typed with `t`.

Some more detail on what the two keys pick:

- `C` follows the filters, like `X`, so it restarts the board as it is on screen.
- `C` skips merged sessions, whose work has landed. `c` still restarts one on
  request, which is what a merged pull request with review feedback on it needs.
- `c` on an agent that *is* running asks first, then stops it and starts it again
  on the same conversation. That is the way to deal with a wedged agent, and the
  difference between `c` and `D`, which stops one and leaves it stopped.
- A session whose worktree has gone is refused by name, since there is nowhere
  for its agent to be. Prune it with `x`.
- An agent whose profile has no `resume_command` restarts as a fresh agent with
  no memory of the task, and the board says so rather than letting the two look
  alike.
- A session brought on with [`dma attach`](#attaching-a-session-you-already-started)
  restarts by conversation id rather than by directory, because its transcript
  is filed where the conversation began and not in the worktree `dma` gave it.

Outside the board, `dma ls` names the sessions nothing is running in its `TMUX`
column.

## Reviewing changes

`enter` or `d` opens the review view: the files the session touched on the left,
the diff of the one under the cursor on the right. Only the selected row is
rendered, so a session with fifty changed files costs one file's worth of `git`
rather than fifty.

| Key | Action |
|---|---|
| `j` `k` | Move within the focused pane: tree rows, or content lines |
| `h` `l` | Focus the file tree or the pane |
| `n` / `p` | Next or previous file, skipping directory rows |
| `}` / `{` | Next or previous change within the file |
| `c` | Read the file's contents instead of its diff, and back |
| `/` | Find in what the pane is showing |
| `f` | Find a file anywhere in the worktree, by fuzzy name |
| `g` | Search the worktree for a string |
| `enter` | Open or close a directory; on a file, focus the pane |
| `t` | Type `look at <file>:<lines>` to the agent, unsent, and hand over the keyboard |
| `tab` | Switch between the working tree and the whole branch (`base...HEAD`) |
| `v` | Toggle side-by-side columns (requires `delta`) |
| `e` | Hide the file tree and give the pane the whole width |
| `[` / `]` | Previous or next session, without leaving the view |
| `←` `→` | Scroll a wide line sideways — content is never wrapped |
| `esc`, `d`, `q` | Back to the board |

Directory rows show the totals of everything beneath them, and a directory that
holds nothing but one directory is collapsed onto a single row. The top row,
`all changes`, is the whole diff. Files git is not tracking are listed with `?`
and diffed against `/dev/null`, so a brand new file — a coding agent's most
common output — is reviewable like any other.

Every row of the diff carries its line number in each file — the old file, then
the new one — the way an editor does, so a change can be found in the file
without counting from a hunk header. Each hunk opens with a rule carrying the
enclosing function instead.

The header counts the changes in the file (`change 2 of 5`) and follows the
scroll, so `}` and `t` always refer to what is on screen. In a window narrower
than 100 columns the tree gives way to the diff automatically.

### Reading and searching the whole worktree

A diff shows what changed and three lines either side of it, which is often not
enough to answer a review question. `c` swaps the pane between a file's diff and
its contents, syntax-highlighted and numbered the same way.

`f` opens a fuzzy file finder over every path in the worktree — everything git
tracks plus everything untracked and not ignored, so build output and
dependencies stay out of it. Type an abbreviation the way you would say it:
`iur` finds `internal/ui/review.go`. `g` searches those files for a string,
through `ripgrep` when it is installed and `git grep` when it is not; the query
is taken literally, so a function signature with brackets in it is a search
rather than a syntax error. Picking a result opens the file in the pane, at the
line the hit was on.

`/` searches what the pane is already showing, whether that is a diff or a file.
Matches are highlighted in place and `n` and `N` step between them; while a
search has hits, the header says so (`find "needle" · 3 of 12`) and `esc` puts
it down, at which point `n` goes back to meaning next file.

## How the board is organized

| Column | Meaning |
|---|---|
| **idle** | The agent stopped, finished, or needs input |
| **active** | The agent is working |
| **pr open** | The branch has an open pull request |
| **merged** | The pull request merged and the worktree can be pruned |

Cards move automatically as agent and GitHub state changes. Within a column,
sessions needing attention sort first, followed by the longest time in state.

Two things raise a desktop notification, because the board is not meant to be
watched: an agent that starts needing your input, and an open pull request that
becomes mergeable — no conflicts, no failing or unfinished checks, and no
reviewer asking for changes. Each fires once on the transition rather than for
as long as the state lasts, and is not repeated when the board is relaunched.
Drafts and pull requests already set to auto-merge or in a merge queue are not
announced, since none is waiting on you.

Pruning a session whose pull request is still open closes that pull request
first, since the worktree and branch behind it are about to be removed. The
confirmation prompt names the pull request, and the remote branch is left in
place—it holds the only remaining copy of the work, and it is what makes the
pull request reopenable. If GitHub cannot be reached, nothing is removed and
`dma` offers to prune anyway and leave the pull request open.

The columns grow with the cards they hold, up to half the window; past that they
scroll instead, so a busy column never crowds out the session panel below. A
column with cards off screen counts them at that end. Moving the cursor scrolls
its column to follow, and the mouse wheel scrolls whichever column the pointer is
over without changing which session the panel shows.

A card is titled with a few-word summary of the task rather than the description
it was started from, since a card is around thirty characters wide and the
opening of a paragraph is the least distinguishing part of it. The summary is
written by a one-shot `claude -p` call on the cheapest model, made after the card
reaches the board so that nothing waits on it. A description short enough to be a
title is left as it is, and so is one that no model could be reached to summarize.

Projects are optional labels for grouping and filtering sessions. Selecting a
project filters the board and makes new sessions join that project. A project
also remembers the repository used for new work. The configuration retains the
legacy JSON key `groups`, but the interface calls them projects.

## Configuration and state

```text
~/.dma/config.json   repositories, profiles, projects, and polling settings
~/.dma/state.json    sessions
```

Set `DMA_HOME` to relocate both files.

An example configuration, shown as valid JSON:

```json
{
  "repos": [
    {
      "id": "my-project",
      "path": "/Users/you/code/my-project",
      "remote": "you/my-project",
      "base_branch": "main",
      "worktree_root": "/Users/you/.dma/worktrees/my-project",
      "bootstrap": {
        "symlink": [
          ".pnpm-store"
        ],
        "clone": [
          "node_modules",
          ".venv"
        ],
        "copy": [
          ".env"
        ]
      }
    }
  ],
  "default_repo": "my-project",
  "agent_profiles": [
    {
      "name": "claude",
      "command": "claude --permission-mode auto",
      "resume_command": "claude --permission-mode auto --continue",
      "hooks": true
    },
    {
      "name": "codex",
      "command": "codex",
      "image_argument": "--image {path}",
      "resume_command": "codex resume --last",
      "hooks": false
    },
    {
      "name": "pi",
      "command": "pi -a --",
      "image_argument": "@{path}",
      "resume_command": "pi -a -c",
      "fork_command": "pi -a --fork {session} --session-id {new}",
      "hooks": false
    }
  ],
  "default_profile": "claude",
  "groups": [
    {
      "name": "auth work",
      "repo": "my-project"
    }
  ],
  "poll_interval_secs": 45,
  "hook_port": 8787
}
```

## Commands

```text
dma                        Open the board and register the current repository
dma attach <agent>         List that agent's recent conversations
dma attach <agent> <id>    Put one of them on the board, in a new worktree
dma repo add <path>        Register a repository explicitly
dma repo list              List registered repositories
dma repo remove <id>       Unregister a repository
dma ls                     List sessions without opening the board
dma hooks print            Print the Claude hook configuration
dma doctor                 Check runtime tools and GitHub authentication
dma version                Print the commit this binary was built from
```

Unregistering a repository never modifies the repository itself. A repository
with active session records must be pruned before it can be unregistered.

## Troubleshooting

**`dma: command not found`**

Add `$(go env GOPATH)/bin` or the directory containing your manually built
binary to `PATH`.

**The selected agent does not start**

Confirm its command is installed and authenticated, then press `A` to select
the intended profile. Profile commands can be edited in
`~/.dma/config.json`.

**PR status or actions are unavailable**

Run `gh auth status`, confirm the repository has a GitHub `origin`, and inspect
the repository's `remote` value with `dma repo list`.

**Notifications come from Script Editor, or steal focus**

`terminal-notifier` is not installed. Run `brew install terminal-notifier` and
confirm it with `dma doctor`. The launch hint appears only once, recorded as
`notifier_hint_shown` in `~/.dma/config.json`; delete that key to see it again.

**The wrong files are shared, cloned, or copied**

Edit the repository's `bootstrap` lists in `~/.dma/config.json`, or unregister
and add the repository again with explicit `--symlink`, `--clone`, and `--copy`
options.

**A tool offers to reinstall dependencies on every new session**

The dependency tree is being shared rather than cloned. Check that it appears
under `bootstrap.clone` and not `bootstrap.symlink` in `~/.dma/config.json`.

**Every card says `⚠ not running` after a reboot**

tmux does not survive a machine restart, so the agents went with it while their
worktrees stayed. Press `C` to restart them all, or `c` for one. See
[Restarting sessions](#restarting-sessions).

**A restarted agent has forgotten what it was doing**

Its profile has no `resume_command`, so the restart launched `command` instead.
Add one; the built-in profiles use `claude --permission-mode auto --continue`,
`codex resume --last` and `pi -a -c`. A profile whose `command` you edited is never given a
generated resume line, since the two have to name the same program.

**A session is safe to remove**

Press `x`. `dma` asks for additional confirmation before discarding uncommitted
changes, commits on a detached HEAD, or an unmerged branch.
