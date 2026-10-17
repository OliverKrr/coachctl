---
name: coachctl
description: Runs coaching from a plain-file record with the coachctl CLI - session start, journal, open items, metrics, Intervals.icu training data and planned workouts, Hevy strength logs and routines, Telegram pushes. Use when a coaching session or scheduled routine works in a workspace with a coaching/ record, or when a coachctl command, its output or an error from it is in question.
---

# coachctl

`coachctl` is the athlete's coaching record and data access in one binary. The record is plain
files under `COACH_HOME` (usually `<workspace>/coaching/`); the integrations use the athlete's own
keys from `~/.config/coachctl/env`. `coachctl <command> --help` has examples for every command.

## Session start (every coaching session)

A turn counts as coaching as soon as it touches training, planning, athlete facts or a record
write; treat an unclear case as coaching. Repo or tooling work skips this.

1. `coachctl session start`. It prints today's date from the system clock in the coaching
   timezone, open items (overdue first), the newest journal entries in full and older ones as
   headlines, stale state metrics, and the prose files. Done when you have read all of it.
2. Read `main.md` with your file tool, then follow the procedure it describes. Open a reference file
   when the task touches its topic.
3. Work the open items, overdue first: resolve (`coachctl item resolve <id> --note …`) or
   renegotiate each with the athlete. Done when no overdue item is left unaddressed.
4. Before any training analysis or plan, `coachctl icu profile`: its date and thresholds are
   authoritative over remembered ones. Name every day of a plan with weekday and date.

A routine that needs only a slice narrows step 1: `--scope items`, `--scope journal`,
`--scope context` (repeatable).

## The record: who writes what

| Part                                              | Writer                         |
| ------------------------------------------------- | ------------------------------ |
| `main.md`, `sections/*.md`, `references/*.md`     | You, with Edit, passage by passage |
| `journal/YYYY-MM.md`, `journal/archive/`          | `coachctl journal` only        |
| `open-items.jsonl`                                | `coachctl item` only           |
| `metrics/<series>.jsonl`                          | `coachctl metric` only         |
| `metrics/series.toml` (series definitions)        | `coachctl metric define`, or Edit to change one |

Edit only the passage that changes; a whole-file rewrite loses what you did not reread. After a
record change, commit it (and push, if the workspace has a remote). Search is `rg -i '<words>'
coaching/`, history is `git log -p -- coaching/<file>`. The formats, the journal entry shape and
the metric kinds are in [references/record.md](references/record.md): read it before your first
record write in a session.

## Which command answers what

| Question                                         | Command                                                     |
| ------------------------------------------------ | ----------------------------------------------------------- |
| What was decided last time? Find an old entry    | `journal list [--since D] [--grep RE] [--headlines]`, `journal show <id>` |
| Log the session                                  | `journal add` (stdin)                                       |
| An entry was wrong                               | `journal correct <id>` (stdin: what is actually true)       |
| Shrink session start after condensing a period   | `journal archive <id>...`                                   |
| Commitments and flags                            | `item add --kind commitment\|flag [--date D] [--dedup-key K]`, `item list [--overdue]` |
| A number measured again and again                | `metric record <series> <value> [--at D]`, `metric get <series>`, `metric list` |
| Athlete thresholds, zones, today's date          | `icu profile`                                               |
| What was trained                                 | `icu activities [--days N \| --since D] [--type Run]`, `icu activity <id>` |
| Readiness inputs (HRV, resting HR, sleep)        | `icu wellness`, `icu fitness`                               |
| What is planned                                  | `icu events --since D --days N`, `icu week [--date D]`      |
| Weekly volume trend                              | `icu summary --days 84 [--type Run]`                        |
| Per-second data for a script                     | `icu streams <id> > data/<file>.csv`                        |
| An endpoint without a command                    | `icu get <path> [--query k=v]` (read-only)                  |
| Strength log and progression                     | `hevy workouts [--since D]`, `hevy history "<exact title>"` |
| Find an exercise template id                     | `hevy templates search <words>`                             |
| Push to the athlete's phone                      | `notify` (stdin, max 4096 characters)                       |
| Something is misconfigured                       | `doctor [--online]`                                         |

Intervals.icu field meanings, units, dates and the workout text format are in
[references/intervals.md](references/intervals.md); open it before analysing activities or
writing a workout description. Hevy's routine format is in
[references/hevy.md](references/hevy.md); open it before `hevy routine create`.

## Output, errors, scripts

- Text output is for reading; add `--json` after the subcommand for scripts and `jq`
  (`coachctl item list --json`). Data for analysis goes straight to a file with `--csv > data/…`,
  never retyped: every number shown to the athlete comes from command or script output.
- Exit codes: 0 ok, 1 failure, 2 your call was wrong (the message says how to fix it), 3 the
  external service failed (retry later, or report it).
- A multi-step query asked more than once becomes a recipe: a script in `scripts/recipes/` that
  composes `coachctl … --json`, with a row in `scripts/recipes/README.md`. Check that catalog
  before composing raw calls.

## External writes

`icu workout push|edit|delete`, `hevy routine create` and `notify` reach outside the record.

1. Run the command with `--dry-run` first and read what it would send.
2. In an interactive session, ask the athlete before the real call unless the record's tier table
   already allows it; a scheduled routine writes only when its prompt says so.
3. Make the real call directly, never inside a recipe script.
4. Check the result: a pushed workout must report parsed steps, or its description syntax is wrong.
   Give repeated pushes of a plan a stable `--uid` so a re-push updates instead of duplicating.

Done when the result line confirms the write (event id, routine id, `Sent`).
