# coachctl design

Why coachctl is shaped the way it is. One section per decision; read the matching one before
changing it.

## Single-user coaching needs no server

An athlete coaching in Claude Code has a shell, a git repo and their own API keys. An OAuth server,
per-user databases and a connector are plumbing they would pay for without using. A CLI plus a skill
is the current practice for agents with a shell: it composes with pipes, `jq` and scripts, its
permissions are scoped per subcommand, and it is extended without a protocol layer. MCP stays the
right shape for shell-less clients (claude.ai web and mobile) and for multi-user setups; that is
[coaching-mcp](https://github.com/OliverKrr/coaching-mcp). The two share no code; coachctl ports
the domain ideas (journal with corrections and archiving, open items with dedup keys, state and
event metrics) and `coachctl import` moves a record across.

Token cost is not the argument: Claude Code defers MCP tool schemas, so a CLI saves little context.
The gains are flexibility, plain files, and a much smaller codebase.

## Three layers

1. **The binary: stable primitives.** Validated record writes, typed API access, the agent-CLI
   conventions. Every integration is optional; `coachctl doctor` reports which are configured.
2. **Recipes: agent-written scripts.** Multi-step queries live as scripts in the user's workspace
   (`scripts/recipes/`) that compose `coachctl … --json`, with a catalog the skill points sessions
   at. A recipe used by a routine or stable for weeks is a candidate for a subcommand. Recipes
   never perform external writes: permission rules match the outer command, so a write inside a
   script would slip past them.
3. **The record: plain files** in the user's workspace repo, with git as history and backup.

The core is domain-neutral (journal, items, metrics, prose); Intervals.icu and Hevy are optional
command groups, and further sources attach the same way.

## The record is plain files

```
main.md, sections/<name>.md, references/<name>.md   prose        the agent edits
journal/YYYY-MM.md, journal/archive/YYYY-MM.md      entries      coachctl journal
open-items.jsonl                                    items        coachctl item
metrics/series.toml                                 definitions  coachctl metric define, or by hand
metrics/<series>.jsonl                              points       coachctl metric record
```

- **Not SQLite.** Volumes are small (hundreds of points, a few hundred entries). Text diffs in git
  beat a binary file, and the build stays pure Go. If search ever needs an index, it is a derived,
  gitignored cache; the files stay the truth. `rg` and `git log -p` replace full-text search and a
  change table.
- **The CLI owns what needs validation, a real clock or ids,** and is the only writer of those
  files: it validates, stamps, takes an exclusive `flock` on `.coachctl.lock` (a cron routine and a
  session may write at once) and replaces the file with a rename. The workspace template's
  `.claude/settings.json` denies `Edit`/`Write` on them.
- **Journal month files are readable markdown.** An entry is a `## #<id> · <time>` header, its
  text, and `> ⚠ Correction <time>: …` lines. A body line that would parse as either shape is
  escaped with one backslash on write and unescaped on read, so any text round-trips.
- **Corrections attach, never rewrite.** The entry text stays; every read shows the corrections.
  Git keeps the file history anyway, but a correction is visible to a reader who never runs git.
- **Archiving moves an entry** to `journal/archive/` under its id. Session start stops showing it;
  `journal show` and `journal list --archived` still do. Growth control, not deletion.
- **Ids are global and permanent** for entries, items and metric points, and import keeps the
  source's ids, so `#147` written in older text keeps meaning the same entry.
- **Metric kinds.** `state` (a new value supersedes, `stale_after_days` flags old ones) or `event`
  (values accumulate), fixed per series in `series.toml`. Validity windows are computed at read
  time from measurement order, so a backdated value lands in history without rewriting anything.
  Series must be defined before the first record, which keeps names stable.
- **Times.** The journal stamps minute precision with the UTC offset; metrics store a local date or
  wall-clock minute; `COACH_TIMEZONE` (IANA name, tzdata embedded) defines "today".

## Session start

`coachctl session start` prints, in one call: today's date from the system clock in the coaching
timezone, open items (overdue first), the newest journal entries in full and older ones as
headlines, stale state metrics, and the list of prose files. It does not inline `main.md`: the
session reads it with its file tool, so no output cap applies. Entry and item bodies are capped
(1200 and 500 characters) with a marker naming the command that returns the rest, because session
start is paid by every session and routine. `--scope` narrows it for routines.

## Command conventions

Concise text by default, `--json` for scripts; errors on stderr that say how to fix the call; exit
codes 0 ok, 1 failure, 2 usage, 3 upstream API error; no interactive prompts (text input comes on
stdin, a terminal on stdin is refused); `--dry-run` on every external write; `--help` with
examples. Tabular data also comes as `--csv`, so analysis input reaches a file without being
retyped.

## Permissions

Shipped in the workspace template's `.claude/settings.json`:

- **allow** the record commands (`session`, `journal`, `item`, `metric`), the Intervals.icu and Hevy
  reads, `icu get`, `doctor`;
- **ask** before `icu workout *`, `icu settings *` and the Hevy writes: an `ask` rule prompts in an
  interactive or Remote Control session and is denied in a headless run;
- **deny** `Edit`/`Write` on the CLI-owned files.

A routine that must push headlessly gets an explicit grant in its `.tools` file.

## Intervals.icu: direct REST plus our own guide

coachctl calls the Intervals.icu REST API directly with the athlete's key (Basic auth, user
`API_KEY`). An MCP connector carries curated knowledge in its tool descriptions: which endpoint
answers which question, units, the workout text format, timezone rules. Calling the API directly
loses that unless coachctl rebuilds it, so `skill/references/intervals.md` is that rebuild, in
original text, from the official API docs and OpenAPI spec (pinned in `docs/refs/`) and the two
forum threads it names. It carries a "sources reviewed up to" date; re-reading the threads since
then and folding new findings in is a recurring maintenance step. Logic the guide describes moves
into code where it can (computed form, the athlete's local date, unit rendering), with tests.

`icu get` is a read-only escape hatch for endpoints without a command; it redacts secret-looking
fields, since the athlete endpoint returns the account's own API key. Pushed workouts may carry an
`--external-id`: coachctl looks the key up on the calendar and updates that event, so a re-pushed
plan does not duplicate. Intervals keeps `external_id` on events an API key creates, but its
server-side upsert is for OAuth apps only and it replaces a client `uid` with its own (both
verified against the live API), so the lookup happens client-side.

Hevy gets the same treatment in `skill/references/hevy.md`, lighter, from the official API docs.
User-specific rules (language of workout descriptions, which markers to push, thresholds) belong in
the user's own workspace, not the package.

## Delivery of the skill and template

`skill/` and `workspace-template/` are embedded with `go:embed`. `coachctl skill install` writes the
skill into `~/.claude/skills/coachctl/` with a version marker, `coachctl doctor` warns when it does
not match the binary, `coachctl setup` prints the agent-facing guide, `coachctl init` writes the
workspace. The guide and skill therefore always match the installed commands, without a checkout to
symlink from.

The template derives from coaching-mcp v3.9's workspace setup. The always-on host files
(`host/`: the Remote Control systemd unit, `autosave.sh`, the crontab example) are copied from it,
each with a header naming that version. The rest diverges on purpose: no server connection check or
output-cap step, keys in `~/.config/coachctl/env`, local routine prompt files only, and a runner
that sends its own failure notice with `coachctl notify` instead of an agent turn.

## Import from coaching-mcp

`coachctl import <dir>` reads a coaching-mcp snapshot: `SKILL.md` becomes `main.md`, `sections/`
and `references/` are copied, and `records.json` (the snapshot's lossless export of journal, open
items, metric series and points, with ids) fills the CLI-owned files. SQLite UTC timestamps become
local ones; a `measured_at` with seconds was the server's UTC default and converts, a date or a
minute was the user's local input and is kept. Series names that are not valid file names are
slugged and reported. Stored routine prompts are reported, not imported. Import refuses a record
that already has journal, item or metric data, and refuses to overwrite a prose file with other
content unless `--force` (the starter `main.md` from `init` is always replaceable).

## Considered and rejected

- **Rust.** Slower builds on small ARM boards (minutes against seconds), more compile-error turns
  for agents, and its stronger type system matters little at this size.
- **TypeScript.** It would reuse coaching-mcp's domain code but carry its shape and a Node runtime;
  the file store replaces most of the database and history code anyway.
- **An Intervals.icu MCP connector.** Less code, but an MCP dependency per user, and recipes could
  not compose its calls.
- **SQLite as the store.** Binary diffs, cgo or a large pure-Go driver, while git already gives
  history and backup.
