# dma

A terminal kanban board for running and monitoring parallel AI coding agent sessions.

You run 3–10 coding agents at once. Each gets its own git worktree and branch. The board answers three questions at a glance:

1. Which session needs my attention right now?
2. What is the state of each session's pull request?
3. Which sessions belong together?

Agents run inside tmux, so they survive the board exiting. The TUI never owns a PTY.

```
 active (2)              review (0)              pr open (1)             merged (0)
▾ auth work · 1 working, 1 needs you
╭──────────────────────╮                        ╭──────────────────────╮
│ token refresh        │                        │ session cookies      │
│ feat/token-refresh   │                        │ #412 ✓ ci ✓ approved │
│ ● working 4m         │                        │ ○ idle 1h20m         │
│ +212 −38             │                        │ +88 −12              │
╰──────────────────────╯                        ╰──────────────────────╯
┏━━━━━━━━━━━━━━━━━━━━━━┓
┃ rate limiter         ┃
┃ feat/rate-limiter    ┃
┃ ◆ needs you 8m       ┃
┃ Bash(rm -rf build)   ┃
┗━━━━━━━━━━━━━━━━━━━━━━┛
```

## Requirements

| Binary | Used for |
|---|---|
| `tmux` | hosting agent sessions |
| `git` | worktrees, branches, diffs |
| `gh` | pull request state (authenticated: `gh auth login`) |

`delta` is used for diff rendering when it is on `PATH`.

Run `dma doctor` to check all of these at once.

## Install

```sh
go install github.com/dma1dma1/dma-cli/cmd/dma@latest
```

Or from a clone:

```sh
go build -o ~/bin/dma ./cmd/dma
```

## Quick start

```sh
cd ~/code/my-project
dma
```

That's it. There is no setup step. The repo you are standing in is registered on first launch and becomes the default for new sessions, so `cd` is how you choose what to work on.

Press `n`, type what you want the agent to do, press enter.

Press `r` at any time to switch repos, add another one, or unregister one.

### What gets set up for you

Registration reads everything it needs from the checkout:

| | |
|---|---|
| `remote` | from `git remote get-url origin` |
| `base_branch` | from `origin/HEAD`, falling back to `main`/`master` |
| `worktree_root` | `~/.dma/worktrees/<id>`, namespaced per repo |
| **bootstrap paths** | **detected — see below** |

**Bootstrap** is the step that decides whether the tool is usable. A fresh worktree with no `node_modules` and no `.env` needs a dependency install and a hand-copied config file before the agent can do anything, and under time pressure you will skip creating the worktree instead.

So it's detected rather than configured. dma looks for dependency trees and env files that **git is ignoring** — anything git tracks already arrives with the worktree — and splits them:

- **symlinked** (shared across worktrees): `node_modules`, `.venv`, `target`, `.gradle`, `vendor`, `Pods`, `.terraform`, package-manager caches — including per-package copies in a monorepo (`packages/*`, `apps/*`, `services/*`, …).
- **copied** (each session needs its own): `.env`, `.env.local`, and friends.

On a pnpm monorepo that typically means ~40 symlinks and one copied `.env`, found in well under a second. The board tells you what it found:

```
registered devops-copilot — shares .pnpm-store, .venv, node_modules +40 more, copies .env
```

Everything is written to `~/.dma/config.json` and can be edited there. Bootstrapped paths are added to the repo's `.git/info/exclude`, so they never make a worktree read as dirty.

## Keys

**Board**

| Key | Action |
|---|---|
| `h` `j` `k` `l` | move selection |
| `enter` | open session detail |
| `n` | new session (compose in bottom bar) |
| `H` `L` | move selected card to previous/next column |
| `g` | collapse/expand current group |
| `G` | change selected session's group |
| `d` | jump to diff for selected session |
| `s` | commit and push branch, open PR |
| `m` | merge PR |
| `x` | prune worktree and branch |
| `D` | kill agent session (worktree kept) |
| `R` | force PR refresh |
| `r` | repositories: switch, add, unregister |
| `f` | filter to one repo, or clear (hidden with one repo) |
| `?` | help |
| `q` | quit — sessions keep running |

**Session detail**

| Key | Action |
|---|---|
| `a` | attach to the live terminal |
| `esc` | back to board |
| `j` `k` | previous/next session |
| `tab` | toggle uncommitted diff / branch diff |
| `d` `s` `m` `x` | same as board |

**Attached** — every keystroke goes to the agent, including `esc`. `ctrl-q` detaches. While attached the tmux status line turns orange and says so.

## How state works

Each session carries two independent state fields.

**`lifecycle`** decides which column a card is in: `active → review → pr_open → merged`. It changes only on a user action or a durable git/PR event — a PR appearing for the branch, or that PR merging.

**`agent_state`** decides the card's badge: `idle`, `working`, `needs_you`, `done`. It is driven by Claude Code lifecycle hooks and changes every few seconds.

**Agent state never moves a card between columns.** If it did, the selection would jump out from under the cursor mid-keystroke and card positions would never become memorable.

Time in state is always rendered next to the badge. `needs you 8m` is the actionable signal; `needs you` alone is not. Past 15 minutes a blocked session escalates its color.

### Agent state comes from hooks, not from scraping

`dma` runs an HTTP listener on `127.0.0.1:<hook_port>` and writes a hook config into each worktree's `.claude/settings.local.json` when the session is created. Only agents this tool launched report to it; an unrelated Claude Code session elsewhere is untouched.

Run `dma hooks print` to see exactly what gets written.

Hook responses are strictly passive — they report state and never return a blocking decision. A `Stop` hook that made the agent act would loop forever.

Entering `needs_you` raises a desktop notification. The point of the tool is not to be babysat.

Agents without hook support degrade to process liveness plus recent pane output.

## Multiple repos

Press `r` for the repo list: `j`/`k` to move, `enter` to make one the default for new sessions, `a` to add another by path, `x` to unregister (which never touches the repo on disk). In the compose bar, `tab` to the `repo` field and use `←`/`→` to pick — the base branch follows your choice.

One repo is the common case and stays uncluttered — the repo handle is not rendered on cards, and the compose bar hides the repo field entirely.

With more than one repo registered, both appear, along with the `f` repo filter.

**Groups and repos are orthogonal.** Swimlanes are always groups. A group may span several repos, and one repo's sessions may sit in several groups. A group is free text chosen at creation; typing a label that doesn't exist creates it.

The join key between a worktree and its PR is the pair **`(repo_id, branch)`**, never the branch alone — two repos can each have a `feat/auth`. Worktree roots and tmux session names are namespaced per repo for the same reason.

## Files

```
~/.dma/config.json   registered repos, agent profiles, groups, poll interval
~/.dma/state.json    sessions (written atomically: temp file, then rename)
```

Set `DMA_HOME` to relocate both.

### Config

```jsonc
{
  "repos": [
    {
      "id": "my-project",
      "path": "/Users/you/code/my-project",
      "remote": "you/my-project",          // read from origin at registration
      "base_branch": "main",
      "worktree_root": "/Users/you/.dma/worktrees/my-project",
      "branch_prefix": "feat/",
      "bootstrap": {
        "symlink": ["node_modules", ".venv"],
        "copy": [".env"]
      }
    }
  ],
  "default_repo": "my-project",
  "agent_profiles": [{ "name": "claude", "command": "claude" }],
  "default_profile": "claude",
  "groups": ["auth work"],                 // display order of swimlanes
  "poll_interval_secs": 45,
  "hook_port": 8787,
  "auto_advance_on_stop": false            // Stop hook moves active → review
}
```

`auto_advance_on_stop` is off by default: an agent that resumes work would otherwise flap its card between columns.

## Commands

```
dma                    open the board (registers the repo you are in)
dma repo add <path>    register a repository explicitly
dma repo list          list registered repositories
dma repo remove <id>   unregister a repository
dma ls                 list sessions without opening the TUI
dma hooks print        print the hook config the board installs
dma doctor             check required external tools
```

You normally need none of these — `cd` into a repo and run `dma`. `dma repo add` exists for scripting and for overriding detection with explicit `--symlink` / `--copy` lists.

## Design notes

- **Exactly four columns.** At 100 characters wide, four columns leaves ~22 characters of card content. Additional axes become swimlanes, never a fifth column.
- **Compose is inline**, not a modal, so the board stays visible while you type — it should be obvious if a session for that work already exists.
- **A single click only selects.** Opening takes `enter` or a double click; misclicks on a dense board are frequent.
- **`needs_you` cards sort first** within each column, then by time in state descending.
- **A collapsed group keeps its rollup**, so collapsing can never hide the fact that something needs attention.
- **PR polling lists open PRs only**, once per repo that has a live session. Asking GitHub for the full PR history with check rollups times out with a 502 on large repos; a tracked PR that leaves the open set is resolved individually instead.
- **Diffs are not parsed.** `git diff --color=always`, optionally piped through `delta`, rendered into a viewport. Untracked files are included explicitly — a new file is an agent's most common output and plain `git diff` omits it.
- **Registration is a side effect, not a step.** Standing in a repo is a complete statement of intent; making someone declare it first is ceremony. Detection reads the checkout instead of asking.
- **Teardown never forces.** A dirty worktree or an unmerged branch requires a second, explicit confirmation.

## Not in scope

Remote/SSH access, PTY ownership, sandboxing, an agent-facing control API, test-gated merges, plugins or themes, multi-user, Windows.
