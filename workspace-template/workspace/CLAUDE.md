# CLAUDE.md — coaching workspace

The user's coaching workspace. Claude Code sessions arrive here interactively, over Remote Control
from a phone, or headless from cron, and they own this repo end to end.

## The record lives in coaching/

The coaching record is plain files under `coaching/` (the `COACH_HOME` of `coachctl`), versioned in
this repo. The `coachctl` skill describes it in full; in short:

- `coaching/main.md`, `sections/`, `references/`: prose. Read with Read, change with Edit, one
  passage at a time; never regenerate a whole document to change part of it.
- The journal, open items and metrics are owned by `coachctl` (`journal`, `item`, `metric`). Write
  them only through it; `.claude/settings.json` denies direct edits.
- `git log -p coaching/` is the change history, `rg` the search.

## Session start

Work that touches no coaching data (repo, tooling, git, routine plumbing) may skip this. As soon as
a turn touches training, planning, athlete facts or a record write, it counts as coaching; treat an
unclear case as coaching. For coaching:

1. `coachctl session start`, then read `coaching/main.md` and follow its procedure. Read `TODO.md`
   (work handed on by earlier sessions) and `handoff/README.md` (open infrastructure defects).
2. Work the open items before coaching, overdue ones first: resolve or renegotiate each.
3. `coachctl icu profile` before any training analysis: its date and thresholds are authoritative.
   Name every day of a plan with its date.
4. Answer coaching questions from the record, never from memory.

## Git: commit and push, no human in the loop

- Commit after every coherent unit of work, record writes included.
- Push after each commit (`git pull --rebase` first). Work that is not pushed does not exist once
  the machine dies, so pushing is your job here.
- Stage explicit paths. Other sessions and routines share this checkout, so never `git add -A`,
  and never stash, reset or check out files you did not change.
- Never force-push, rewrite history or skip hooks.

## Data and numbers

- Pull data into files with the command itself, never by retyping it:
  `coachctl icu activities --since 2026-06-01 --csv > data/runs.csv`.
- Every number shown to the user (a reply, a chart label, a stat tile) is computed by a script or a
  `coachctl` call and copied from its output.

## Scripts and recipes

- `scripts/recipes/` holds multi-step queries as scripts that compose `coachctl … --json`. Check
  its `README.md` catalog before composing raw calls, and add a recipe when you repeat a multi-step
  query. Recipes never perform external writes: pushes and routine creation stay direct `coachctl`
  calls, so their permission rules apply.
- `scripts/` holds analysis code; run it with `uv run scripts/<name>.py`. Keep scripts
  parameterized: thresholds and baselines are coaching decisions that live in the record.
- Charts go to `out/` as PNGs. Read the PNG back before delivering it.

## Skills

`capture` (in `.claude/skills/`) routes a durable finding to the right place in the record, or a
repo-technical gotcha into the Gotchas below. Use it when a session produced one, and when the
PreCompact hook reminds you.

## Keep the session-start path short

`CLAUDE.md`, `TODO.md` and `handoff/README.md` load before any work, so every stale line costs every
session. Finishing a piece of work removes what it made obsolete in the same commit.

## Routines

Scheduled check-ins run as headless `claude -p` turns through `scripts/run_routine.sh <name>`, started
by cron. Read `scripts/README.md` before editing the runner, a routine prompt or a `.tools` file.

## Replies

- Reply in the language the user writes in.
- Name content, not item or journal numbers.
- Be decisive on data and safety, collaborative on direction, and give the reason.

## Credentials and limits

- API keys live in `~/.config/coachctl/env` (0600), outside this repo. Keys stay out of commits,
  transcripts, charts and replies.
- Stop and ask the user before an external write (`coachctl icu workout …`, `coachctl hevy routine
  create`), before changing a document whose tier says to ask first, and before anything that needs
  a credential you do not have. If the user is not there, leave the work in `TODO.md`.

## Finishing a session

A session is done when its work is committed (and pushed, with a remote), and `TODO.md` and
`handoff/` reflect what is still open.

## Gotchas (repo-technical)

Maintained by `capture`: one tight bullet per mechanism, newest last.

- A headless `claude -p` silently denies any tool missing from `--allowedTools`; the run continues
  and does less.
- Permission rules match the command's start: put `--json` after the subcommand
  (`coachctl session start --json`), or the allow rule does not match.
