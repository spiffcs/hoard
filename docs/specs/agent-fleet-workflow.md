# Running a fleet: parallel development with cmux worktrees

How multi-agent work is actually run on this repo. Everything here has been
used in anger; the failure modes described are ones that happened, not ones
imagined.

The shape: one **anchor** session coordinates, each **agent** gets its own
cmux workspace, git worktree and branch. Agents never commit. They report
back, the anchor promotes their work onto main, and the repository owner
commits it.

## Why worktrees rather than one repo

Four agents editing one checkout serialise on the filesystem and corrupt each
other's builds — one agent's `go build` sees another's half-written file. Git
worktrees give each agent a real directory at a real commit, so builds and
test runs are independent. They share one object store, so this costs almost
nothing on disk.

## Spawning

```sh
cmux-worktree <branch> [--base <ref>]
```

`~/.config/cmux/bin/cmux-worktree` is what the `ctrl+alt+cmd+w` chord runs
(`actions.worktree-workspace` in `~/.config/cmux/cmux.json`). It creates the
worktree under `~/development/.worktrees/<repo>/<branch>`, then opens a cmux
workspace with the `<repo>-dev` layout: a left pane running `cmux claude-teams`
— which is what makes the agent reachable by `SendMessage` — and a right pane
carrying `test` and `git` surfaces.

Companions: `cmux-worktree ls | rm | gc`, and `cmux-worktree-close`
(`ctrl+alt+cmd+x`).

### The trap: the default base is `origin/main`, not local `main`

The script forks from the remote-tracking ref deliberately, so agents branch
from upstream rather than from whatever the local branch last saw. **When local
main is ahead and unpushed, every agent silently starts N commits behind**, and
their patches then have to be three-way merged onto a moved main.

This has bitten twice. Once with agent-1/2/3 forked at `7cbb408` while main was
three ahead. Again on 2026-08-09, when main carried an unpushed commit deleting
a whole package — agents forking from `origin/main` would have had the deleted
files back, and written patches against code that no longer exists.

Check before every spawn, and pass `--base main` when they differ:

```sh
git rev-parse origin/main main
```

## Briefing

Agents are addressed by name via `SendMessage`. `ListAgents` gives the roster;
**a send may need the ` [ref]` suffix** the listing shows — the bare name is
rejected when it is ambiguous, and the error tells you the exact form to use.

A brief that works has six parts. The first three are what make parallel lanes
safe; the last three are what make the results trustworthy.

1. **Explicit file ownership**, stated as a list, plus who owns the files it
   must not touch. Lanes that share a file will collide, and the collision
   surfaces at promotion time when it is most expensive. If two items in the
   backlog touch one file, they belong in the same lane — split by *file*, not
   by item number.
2. **A stop rule**: "if your work seems to need a file outside your set, STOP
   and report rather than reaching across."
3. **What the other lanes are**, so an agent that spots a cross-lane problem
   can name it instead of fixing it.
4. **A negative control, required.** Prove the new test fails *without* the
   fix. Two rounds of this work have shown that a green suite alone does not
   establish that a guard bites — an assertion can pass because it is vacuous.
   Ask for the failing output, not a claim that it failed.
5. **The gates**, named exactly: `gofmt -l ./internal ./cmd`, `go build ./...`,
   `go vet ./...`, `go test ./...`, plus `swift test --package-path
   scan/hoard-scan` for the SwiftPM package.
6. **Landing rules**: leave everything uncommitted, `SendMessage` the anchor
   when done, do not push, do not open a PR.

Two more things worth putting in a brief when they apply:

- **Facts you already established**, marked as such — with an instruction not
  to redo them but not to take them on faith either. This stops four agents
  re-deriving the same background.
- **Permission to conclude "no fix".** Some items need a decision rather than a
  patch. Say so explicitly, or the agent will force a patch to look successful.

## Landing the work

Agents leave everything **uncommitted** in their worktree; commits are the
owner's alone. The anchor promotes:

```sh
git apply --3way --whitespace=nowarn <patch>
```

`--whitespace=nowarn` is **mandatory** in this repo — `git apply` mangles tabs
here without it, and the corruption is silent.

Then verify against main and hand the owner a commit message. Verify the
*combination*, not the individual reports: the one real defect found in a
previous fleet round came from a width sweep across all four lanes' output that
no single agent could have run. Each agent is correct within its lane and blind
outside it.

## Reclaiming worktrees

`cmux-worktree gc` reclaims worktrees whose workspace has been closed. Two
rules keep it from eating live work:

**A clean `git status` does NOT mean a worktree is idle.** On 2026-08-09 an
agent's workspace was closed and its worktree deleted mid-task. The tree really
was clean when checked — but the agent had been briefed *after* that check and
was still in its read-only investigation phase, so it had nothing on disk yet.
Nothing was lost, by luck. Before removing any worktree, run `ListAgents`,
confirm no session is live in it, and re-check `git status` immediately before
the delete rather than trusting an earlier reading. An open workspace is itself
the signal `gc` uses; overriding a `KEEP … workspace still open` is overriding
exactly that.

**`gc` will not reclaim a worktree whose files differ from `origin/main`**,
even when the work has already landed — a promoted worktree still looks dirty
because it is *missing* main's other commits. Verify the real thing before
deleting: for each changed file, check that every added line is present in
main's HEAD, then

```sh
cmux-worktree rm <branch> --force --prune-branch
```

## Recovering a broken or closed session

**A pane that never started.** If the left pane's `cmux claude-teams` never
ran — a mistyped command leaves it at a shell prompt — the agent will not
appear in `ListAgents` even though the worktree and workspace both exist.
Confirm nothing is on disk, then `cmux-worktree rm <branch> --force
--prune-branch` and spawn it again. Rebuilding is more reliable than repairing
the pane, and costs nothing when the agent had not started.

**A closed session.** Transcripts survive workspace closure at
`~/.claude/projects/<slugified-cwd>/<session-id>.jsonl`. Read it to establish
what the agent actually did — every `tool_use` block, plus the
`file-history-snapshot`, whose `trackedFileBackups` is empty if it never wrote
anything. To restore, recreate the worktree at the same commit, then

```sh
cmux workspace create --cwd <worktree> --name <name> --group workspace_group:1 \
  --layout <hoard-dev layout, claude surface's command replaced by \
            `cmux claude-teams --resume <session-id>`>
```

`cmux claude-teams` is a passthrough wrapper around `claude`, so it accepts
`--resume`. Confirm the resume attached by checking that the original `.jsonl`
grew, rather than a second file appearing.

## What does not go to a fleet

- **Anything needing hardware.** Live pile scans, phone reconnect tests, audio
  behaviour. Agents cannot hold cardboard in front of a camera.
- **Anything needing one coherent judgement across the whole tree.** Ranking
  risk, deciding what blocks a launch, choosing a schema version. Fan-out
  fragments exactly the context those need.
- **Work smaller than its brief.** A one-line change costs more to delegate,
  verify and promote than to make.
