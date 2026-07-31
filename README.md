# dma

A terminal kanban board for running and monitoring parallel AI coding agent sessions.

You run 3–10 coding agents at once. Each gets its own git worktree, cut from a freshly fetched `origin/<base>`. The board answers three questions at a glance:

1. Which session needs my attention right now?
2. What is the state of each session's pull request?
3. Which sessions belong together?

Agents run inside tmux, so they survive the board exiting. The TUI never owns a PTY.

```
┏━ idle 2 · waiting on you ━━━━━┓ ╭─ active 1 · agent working ────╮ ╭─ pr open 1 · pushed ──────────╮ ╭─ merged · done ───────────────╮
┃ ▌ rate limiter                ┃ │ ▌ token refresh               │ │ ▌ session cookies             │ │   —                           │
┃ ▌ feat/rate-limiter           ┃ │ ▌ feat/token-refresh          │ │ ▌ #412 ✓ ci ✓ approved        │ │                               │
┃ ▌ ◆ needs you 8m              ┃ │ ▌ ● working 4m                │ │ ▌ ○ idle 1h20m                │ │                               │
┃ ▌ Bash(rm -rf build)          ┃ │ ▌ +212 −38                    │ │ ▌ +88 −12                     │ │                               │
┃                               ┃ │                               │ │                               │ │                               │
┃ ┃ audit logging               ┃ │                               │ │                               │ │                               │
┃ ┃ ✓ done 2m                   ┃ │                               │ │                               │ │                               │
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛ ╰───────────────────────────────╯ ╰───────────────────────────────╯ ╰───────────────────────────────╯
╭─ audit logging · dma-cli-audit-logging ────────────────────────────────────────────────────────────────────────────────────────────╮
│  ▾ agent claude   ▾ repo dma-cli                                                                          ▾ project all projects  │
│ ────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────── │
│ ● Wrote internal/audit/log.go                                                                                                      │
│   ran tests: 42 passed                                                                                                             │
│ Done in 18s.                                                                                                                       │
│ ❯ press i to describe a task for a new agent session                                                                               │
╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

The panel at the bottom is always there: the selected session's live terminal, the three selectors that define a new session, and an input bar. Press `e` to expand it to the full screen, `a` to attach to it for real.

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

Press `i`, type what you want the agent to do, press enter. The agent, repo and project it uses are whatever the three selectors at the bottom show. While composing, `ctrl-v` pastes an image from the system clipboard (or ordinary text when there is no image); press backspace at the start of the input to remove the last image.

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
| `h` `j` `k` `l` | move between cards and columns |
| `i` | focus the task input at the bottom |
| `ctrl-v` | paste to the selected live agent without attaching |
| `tab` | cycle board → input → agent → repo → project |
| `a` | attach to the selected session's terminal |
| `e` | expand the session panel to full screen |
| `enter` `d` | review the diff |
| `H` `L` | move a card to the previous/next column |
| `G` | set the selected session's project |
| `s` | commit and push the agent's branch, open a PR |
| `o` | open the PR in your browser |
| `y` | copy the PR link to the clipboard |
| `m` | merge the PR — or add it to the merge queue, where the base branch has one |
| `x` | prune the worktree and its branch |
| `X` | prune every merged session the board is showing |
| `D` | kill the agent, keep the worktree |
| `R` | refresh PR and session state now |
| `A` | pick the agent new sessions start with |
| `r` | repositories: switch, add, unregister |
| `p` | pick a project to filter the board |
| `f` | filter to the active repo, or clear |
| `?` | help |
| `q` | quit — agents keep running |

**Task input** — `ctrl-v` adds a clipboard image (or pastes text), and `backspace` at the start removes the last image. `enter` starts an agent using the agent/repo/project shown in the selectors. `esc` returns to the board.

**Selectors** — `←` `→` change a value in place; `enter` opens the full list. Clicking a chip opens it too. From the board, `A` and `p` jump straight to the agent and project lists.

**Diff** — `tab` toggles working tree / branch diff, `j` `k` step between sessions, `esc` returns.

**Attached** — every keystroke goes to the agent, including `esc`. `ctrl-q` detaches. While attached the tmux status line turns orange and says so.

## The four columns

| Column | Owned by | Means |
|---|---|---|
| **idle** | the agent | not working — blocked on you, finished, or sitting there |
| **active** | the agent | working right now; leave it alone |
| **pr open** | git | branch pushed, PR exists |
| **merged** | git | PR merged; the worktree is a prune candidate |

The first two move on their own as agents start and stop, so **idle is the column you act on** — everything waiting for you, whether that is a permission prompt or a finished diff.

The last two are owned by durable git facts, and agent activity can never pull a card out of them: whether a process happens to be mid-tool-call says nothing about whether its PR is merged.

A merge queue is why **merged** is not where `m` puts a card. Where the base branch has one, `m` hands the PR to the queue and the card reads `◌ queued` in **pr open** until the queue lands it — or drops it back out, which polling notices too. Only a PR that actually merged reaches the merged column.

Because cards move by themselves, **selection is anchored to the session, not to a position.** When a card crosses columns the cursor follows it rather than landing on whatever took its place, and a card appearing above the cursor never shifts it.

Within a column, `needs you` sorts first, then longest time in state. Time in state is always shown next to the badge — `needs you 8m` is the actionable signal, `needs you` alone is not. Past 15 minutes a blocked session escalates its color.

### Where agent state comes from

**Claude Code reports it exactly**, through lifecycle hooks. `dma` runs an HTTP listener on `127.0.0.1:<hook_port>` and writes a hook config into each worktree's `.claude/settings.local.json` at creation, so only agents this tool launched report to it and an unrelated Claude Code session elsewhere is untouched. Run `dma hooks print` to see what gets written.

Hook responses are strictly passive — they report state and never return a blocking decision. A `Stop` hook that made the agent act would loop forever.

**Codex and anything else is inferred**, from process liveness plus whether the pane is still changing. A pane quiet for 25 seconds means the turn ended; a pane whose tail looks like an approval request (`[y/n]`, `Allow …?`, a numbered choice list) means it needs you. This is deliberately coarse — pane text is a rendering of a UI, not a state machine, so the heuristic keys on structure rather than on wording that changes between releases.

Entering `needs_you` raises a desktop notification either way. The point of the tool is not to be babysat.

## Projects

A project is an arbitrary label, chosen with the selector when you start a session or with `G` afterwards. Typing a label that does not exist creates it. The project selector filters the board to one project, and new sessions started while filtered join it.

**A project remembers its repo.** Selecting a project moves the repo selector to that repo, so switching what you are working on is one choice rather than two kept in step by hand. A project takes its repo from wherever it was created — the repo selector if you named it from the chip, the card's own repo if you named it from a session — and the picker names each project's repo beside it.

Changing the repo selector while a project is selected re-points that project at the new repo. That is the only way a binding changes, and it is how a project created before it had one, or work that has moved, gets corrected. With no project selected the repo selector is just the repo selector.

A project that names no repo leaves the selector where it is. Sessions already running keep the repo they were started in whatever their project says later.

## Multiple repos

Press `r` for the repo list: `j`/`k` to move, `enter` to make one the default for new sessions, `a` to add another by path, `x` to unregister (which never touches the repo on disk, and unbinds any project that named it). In the compose bar, `tab` to the `repo` field and use `←`/`→` to pick — the base branch follows your choice, and so does the selected project's binding.

One repo is the common case and stays uncluttered — the repo handle is not rendered on cards, and the compose bar hides the repo field entirely.

With more than one repo registered, both appear, along with the `f` repo filter.

**Swimlanes are always groups**, never repos. A group's sessions may sit in several repos — the group's own repo is only the default new ones start in, so a group that has worked in two places still shows both in one lane. A group is free text chosen at creation; typing a label that doesn't exist creates it.

The join key between a worktree and its PR is the pair **`(repo_id, branch)`**, never the branch alone — two repos can each have a `feat/auth`. Worktree roots and tmux session names are namespaced per repo for the same reason. A session has no branch until its agent makes one, so PR polling starts from the moment that name is adopted.

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
      "bootstrap": {
        "symlink": ["node_modules", ".venv"],
        "copy": [".env"]
      },
      "on_pr_open": "/deploy-watch {pr}"    // optional; overrides the profile's
    }
  ],
  "default_repo": "my-project",
  "agent_profiles": [
    { "name": "claude", "command": "claude --permission-mode auto", "hooks": true,
      "on_pr_open": "/pr-shepherd {pr}" },  // optional; see below
    {
      "name": "codex",
      "command": "codex",
      "image_argument": "--image {path}",
      "hooks": false
    }
  ],
  "default_profile": "claude",
  "groups": [                              // known projects
    { "name": "auth work", "repo": "my-project" }   // repo: where new sessions go
  ],
  "poll_interval_secs": 45,
  "hook_port": 8787
}
```

`image_argument` is repeated for every image attached to a new session; `{path}` becomes the shell-quoted path of the staged PNG. The built-in Codex profile uses `--image {path}`. Profiles without `image_argument` receive the image paths in their opening prompt, which lets agents such as Claude Code read them directly.

`hooks: false` puts a profile on the inferred-state path described above. Add any agent you like — the command is run inside the worktree's tmux session.

### On PR open

`on_pr_open` is a line typed into the agent the first time a pull request appears for its session — a slash command, or plain instructions. `{pr}` and `{url}` are substituted. Unset, the default, sends nothing.

```jsonc
"on_pr_open": "/pr-shepherd {pr}"          // a command your agent knows
"on_pr_open": "Watch PR {url} until CI is green and every review thread is resolved."
```

It can be set in two places, and the more specific one wins:

| Where | Means |
|---|---|
| `agent_profiles[].on_pr_open` | the default for that agent, in every repo |
| `repos[].on_pr_open` | this repo instead — **absent** inherits the profile, **`""`** disables shepherding here |

So the profile is how you say "always", and the repo is how you say "except here". A repo needing its own line sets one; a repo with nothing worth shepherding sets `""`; a repo that says nothing follows the agent.

```sh
dma repo add --on-pr-open '/deploy-watch {pr}' ~/code/service   # this repo's own line
dma repo add --on-pr-open '' ~/code/scratch                     # never shepherd here
```

Both are plain fields in `~/.dma/config.json` and can be edited there directly.

It fires on the pull request existing, not on anything the agent was told at launch. Asking for shepherding in the opening prompt only works when you remember to ask and the agent is still holding the instruction an hour later; a PR appearing is a durable fact the board already computes, so it covers the sessions you forgot to ask. Both ways one can appear — pressing `s`, and the poll finding a PR the agent opened itself — go through the same path.

Sent once per pull request number, recorded in `state.json`, so restarting the board does not start a second turn, and a PR closed and reopened under a new number is picked up again. A send is only recorded once it lands: a session whose terminal is gone stays armed and is served when the agent comes back.

The line is typed into the agent's composer, so it inherits that composer's quirks — notably the vim-keymap mangling described under `LaunchCommand`, which is why the built-in `codex` profile leaves it unset.

The card stays in **pr open** while the line runs. Its badge goes to `● working`, but PR-driven columns are never given up to agent activity.

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

You normally need none of these — `cd` into a repo and run `dma`. `dma repo add` exists for scripting, for overriding detection with explicit `--symlink` / `--copy` lists, and for setting a repo's `--on-pr-open` line.

## Design notes

- **Exactly four columns.** At 100 characters wide, four columns leaves ~22 characters of card content. Additional axes become filters, never a fifth column.
- **The input is always on screen**, so starting work is one key away and the board stays visible while you type — it should be obvious if a session for that work already exists.
- **Cards are rows with a colored accent bar**, not nested boxes. Borders inside borders read as noise, and the bar carries the state signal more cheaply.
- **There was a fifth idea, `review`, and it was cut.** It meant "diff ready, not pushed" but nothing filled it automatically, so it was manual bookkeeping in a tool whose premise is not doing bookkeeping. `idle` says the same thing from real evidence.
- **A single click only selects.** Opening takes `enter` or a double click; misclicks on a dense board are frequent.
- **`needs_you` cards sort first** within each column, then by time in state descending.
- **A collapsed group keeps its rollup**, so collapsing can never hide the fact that something needs attention.
- **PR polling lists open PRs only**, once per repo that has a live session. Asking GitHub for the full PR history with check rollups times out with a 502 on large repos; a tracked PR that leaves the open set is resolved individually instead.
- **Only the selected session's pane is captured** for the preview. Capturing every pane every second would spawn a process per session per tick for output nobody is reading.
- **Diffs are not parsed.** `git diff --color=always`, optionally piped through `delta`, rendered into a viewport. Untracked files are included explicitly — a new file is an agent's most common output and plain `git diff` omits it.
- **Registration is a side effect, not a step.** Standing in a repo is a complete statement of intent; making someone declare it first is ceremony. Detection reads the checkout instead of asking.
- **dma creates no branches.** A worktree starts detached at the tip of `origin/<base>`, fetched at that moment — a local base branch is whatever the last direct visit to the repo left behind, which on a repo driven through this tool is nothing. The agent names its own branch once it knows what the work turned out to be, and the board adopts whatever it finds there; a name derived from the task title would be a guess made at the moment of least information. Until then the card reads `no branch`, and `s` refuses to invent one.
- **`on_pr_open` defaults on the agent profile and is overridden on the repo.** The line is written in one agent's vocabulary — `/pr-shepherd 412` means something to Claude Code and nothing to anything else — so the profile is where it belongs by default, and setting it in one place is what lets "always" mean every repo that agent works in. Putting it *only* on repos would make unconditional shepherding a list to keep in step with the repo list. Putting it only on profiles would make one repo with a different review flow, or none at all, unrepresentable. The repo field is a pointer for that second case: absent inherits, present-and-empty disables, and with a plain string those two would be the same value.
- **Teardown never forces.** A dirty worktree, commits sitting on no branch, or an unmerged branch each require a second, explicit confirmation.

## Not in scope

Remote/SSH access, PTY ownership, sandboxing, an agent-facing control API, test-gated merges, plugins or themes, multi-user, Windows.
