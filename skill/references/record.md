# The coaching record

Everything under `COACH_HOME`. Prose is the agent's; the journal, open items and metrics are
written only by `coachctl`, which validates input, stamps times and ids, takes a file lock and
writes atomically.

## Prose

- `main.md`: the primary coaching document, read at every session start. Keep it skimmable; push
  detail into `references/`.
- `sections/<name>.md`: further sections of the main document, when it is split.
- `references/<name>.md`: one file per topic or concern (zones, injuries, season plan, equipment).
  Lowercase kebab-case names.

Change prose with Edit, one passage at a time. A lost passage comes back from `git log -p`.

## Journal

Session history: decisions, data changes, observations. One file per month,
`journal/YYYY-MM.md` (by the entry's local date); archived entries move to
`journal/archive/YYYY-MM.md`.

- **Ids are global and permanent.** `#147` names the same entry forever, across months and the
  archive, and a migrated record keeps its old ids. Inside entries and items, ids are fine; in
  replies to the athlete, name the content instead.
- **The timestamp is stamped by coachctl.** Start the text with its substance, not a date.
- **Entry shape**: a headline first line (headlines are what older entries collapse to), then the
  parts that apply: Decided / Learned / Committed / Watch for. One entry per session or routine
  run, not one per fact.
- **Corrections**: `journal correct <id>` attaches what is actually true; the original stays and
  every read shows the correction with it. A second correction adds to the first.
- **Archiving** is growth control: once a period is condensed into a reference document, archive
  its entries. They stay readable (`journal show`, `journal list --archived`), and session start
  stops showing them.

On disk an entry is a `## #<id> · <YYYY-MM-DD HH:MM +zz:zz>` header, the text, and optional
`> ⚠ Correction <time>: …` lines. Read the files with Read or rg freely; write only through
coachctl.

## Open items

`open-items.jsonl`, one JSON object per line. Two kinds:

- `commitment`: the athlete's if-then next action, agreed at a session close. Set `--date` when it
  has one.
- `flag`: something a routine noticed for the coach to raise. Give a recurring condition a
  `--dedup-key` (e.g. `hrv-low-2026-10-17`): while an open item carries that key, a second add is
  a no-op.

An open item whose date has passed is OVERDUE. Items are resolved (`done`, or `--dismiss`ed),
never deleted; the resolution note sits next to the original text.

## Metrics

Repeated numbers, kept as data so trends are a query.

- `metrics/series.toml` defines each series: `kind`, optional `unit`, `stale_after_days`,
  `description`. Define before the first record: `coachctl metric define <name> --kind …`.
- **`state`** (FTP, thresholds, HRV baseline): a new value supersedes the previous one. Reads
  return the value valid now; `--as-of D` answers "what was it then"; a backdated value becomes
  history behind a newer one. `stale_after_days` flags the current value at session start.
- **`event`** (weekly volume, adherence, body weight readings): values accumulate; reads list
  points with min/avg/max.
- A series keeps its kind; to change meaning, start a new series. Points of a series share the
  series' unit, so convert before recording.
- Points live in `metrics/<series>.jsonl` with global ids; `metric delete <id>` removes a wrong one.

## Commit discipline

Record changes are commits like any other: stage the files you changed (`git add coaching/…`),
commit with a message saying what changed, push when the workspace has a remote. Several sessions
and routines may share the checkout, so stage explicit paths.
