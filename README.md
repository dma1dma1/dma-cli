# dma

`dma` runs and monitors multiple AI coding-agent sessions from one terminal.
Each task gets its own git worktree and persistent tmux session, while the board
shows which agents need attention and the state of their GitHub pull requests.

Use it when you want to run several Claude Code, Codex, or other command-line
coding agents in parallel without manually creating worktrees, switching
terminals, or checking every session for progress.

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
| A coding-agent CLI | `claude`, `codex`, or another configured command |

Install and authenticate at least one coding-agent CLI before starting a
session. `dma` includes profiles for Claude Code and Codex and uses Claude Code
by default. If you only have Codex installed, select it with `A` before starting
your first task.

For GitHub pull-request features, also install `gh` and authenticate it:

```sh
gh auth login
```

The board can run without `gh`, but PR status, push-and-open-PR, link, and merge
features will not be available. `dma doctor` treats missing GitHub integration
as an incomplete setup.

Optional tools:

| Tool | Purpose |
|---|---|
| `delta` | Improved diff rendering |
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
command -v claude || command -v codex
dma doctor
```

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
4. Press `enter`.

In the task input, `ctrl-v` attaches a clipboard image or pastes ordinary text.
Press `backspace` at the start of the input to remove the last attached image.

Describe the task in as much detail as the agent needs. The whole description is
sent to the agent. The card shows that text at first and renames itself to a
short summary of it a few seconds later.

`dma` fetches the configured base branch, creates a detached worktree under
`~/.dma/worktrees`, prepares it, starts a tmux session, and launches the agent
with your task. The worktree is named from the opening of the description, and
keeps that name after the card is renamed.

The agent is responsible for creating and naming its branch. Until it does, the
card displays `no branch` and `s` cannot push or open a pull request.

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

It handles detected paths in two ways:

- **Symlinked and shared:** dependency trees and caches such as `node_modules`,
  `.venv`, `target`, `.gradle`, `vendor`, `Pods`, and `.terraform`.
- **Copied per worktree:** local configuration such as `.env`, `.env.local`,
  and related files.

Detection also covers common monorepo directories such as `packages/*`,
`apps/*`, and `services/*`. Tracked files are not bootstrapped because git
already places them in every worktree.

The registration notice summarizes what was detected:

```text
registered devops-copilot — shares .pnpm-store, .venv, node_modules +40 more, copies .env
```

Review this behavior before starting agents in repositories with sensitive
configuration:

- Copied `.env` files may contain secrets that the selected agent can access.
- Symlinked dependency directories are shared, so changes made by one worktree
  are visible to the others.

Detected paths are stored in `~/.dma/config.json`. You can edit the repository's
`bootstrap.symlink` and `bootstrap.copy` lists, or register a repository
explicitly:

```sh
dma repo add --symlink node_modules,.venv --copy .env ~/code/my-project
```

Bootstrap paths and the Claude hook settings installed by `dma` are added to
the repository's local git exclude file so they do not make worktrees appear
dirty.

## Agent permissions and profiles

The built-in profiles are:

```json
[
  {
    "name": "claude",
    "command": "claude --permission-mode auto",
    "hooks": true
  },
  {
    "name": "codex",
    "command": "codex",
    "image_argument": "--image {path}",
    "hooks": false
  }
]
```

Claude Code is the default and starts with `--permission-mode auto` so parallel
sessions can make progress without stopping at every ordinary permission
prompt. Review whether that permission mode is appropriate for your environment
before using it. You can change the command in `~/.dma/config.json`.

Claude Code reports its state through hooks installed only in worktrees created
by `dma`. Codex and custom profiles are read off their terminal instead, in this
order of trust: the agent's own interrupt hint (`esc to interrupt`) says a turn
is running, a menu with a selection marker on one row or a line naming a key to
press means `needs you`, and for an agent that shows neither, a pane quiet for 25
seconds means the turn ended. It is a good signal rather than an exact one — a
turn shorter than one poll can pass unnoticed.

You can add another agent by adding an entry to `agent_profiles`. The command
runs inside the new worktree. The task is appended as a positional argument;
use `{prompt}` in the command if it needs to appear somewhere else.

For images attached to a new session, `image_argument` is repeated once per
image and `{path}` is replaced with the shell-quoted path to its staged PNG.
The built-in Codex profile uses `--image {path}`. Profiles without an
`image_argument`, including Claude Code, receive the image paths in their
opening prompt.

## Everyday controls

| Key | Action |
|---|---|
| `h` `j` `k` `l` | Move between cards and columns; a full column scrolls to keep the cursor in view |
| `i` or `n` | Start composing a new task |
| `t` | Type into the selected agent from the session panel |
| `ctrl-v` | Paste an image or text into a task or live agent |
| `a` | Attach to the selected tmux session |
| `e` | Expand the session panel |
| `enter` or `d` | Review the selected session's diff |
| `s` | Commit, push, and open a pull request |
| `o` / `y` | Open or copy the pull-request link |
| `m` | Merge the pull request or add it to the merge queue |
| `x` | Prune one session's worktree and branch |
| `X` | Prune the merged sessions currently shown |
| `D` | Kill the agent but keep its worktree |
| `A` | Choose the agent used for new sessions |
| `r` | Switch, add, or unregister repositories |
| `p` | Choose a project filter |
| `G` | Move the selected session to a project |
| `f` | Filter to the active repository |
| `R` | Refresh session and PR state |
| `?` | Show the complete in-app help |
| `q` | Quit; agents continue running |

When typing directly into the session panel or an attached tmux session, every
key—including `esc`—goes to the agent. Press `ctrl-q` to return control to the
board. While attached, the mouse wheel scrolls through the agent's history.
Hold `shift`—or `option` in some macOS terminals—to select text. `dma` restores
your tmux mouse setting when you detach.

If the agent's composer is modal—Codex with `tui.vim_mode_default`, which returns
to normal mode after every message it sends—`dma` puts it back into insert mode
when you hand it the keyboard, so a sentence typed into the panel arrives as text
rather than as vim commands. Nothing is sent to an agent that is not modal, or
while a dialog is open, so answering a prompt with `1` or `y` still works.

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
Drafts and pull requests already in a merge queue are not announced, since
neither is waiting on you.

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
      "hooks": true
    },
    {
      "name": "codex",
      "command": "codex",
      "image_argument": "--image {path}",
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
dma                    Open the board and register the current repository
dma repo add <path>    Register a repository explicitly
dma repo list          List registered repositories
dma repo remove <id>   Unregister a repository
dma ls                 List sessions without opening the board
dma hooks print        Print the Claude hook configuration
dma doctor             Check runtime tools and GitHub authentication
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

**The wrong files are shared or copied**

Edit the repository's `bootstrap` lists in `~/.dma/config.json`, or unregister
and add the repository again with explicit `--symlink` and `--copy` options.

**A session is safe to remove**

Press `x`. `dma` asks for additional confirmation before discarding uncommitted
changes, commits on a detached HEAD, or an unmerged branch.
