# Coaching workspace

A Claude Code coaching workspace built on `coachctl`. The coaching record lives in `coaching/` as
plain files; `CLAUDE.md` is the operating manual for the sessions that work here, and the
`coachctl` skill (`coachctl skill install`) explains the record and the commands.

```sh
coachctl session start                   # what a coaching session reads first
coachctl doctor --online                 # check config, record and integrations
uv run scripts/<name>.py                 # run an analysis
scripts/run_routine.sh <routine-name>    # run a scheduled routine once, headless
```

Layout: `coaching/` the record · `scripts/` analysis code and the routine runner ·
`scripts/routines/` routine prompts and tool grants · `scripts/recipes/` multi-step queries ·
`TODO.md` work handed between sessions · `handoff/` open defects · `data/`, `out/` gitignored
exports and charts.
