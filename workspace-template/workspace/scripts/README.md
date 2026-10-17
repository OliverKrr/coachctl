# scripts/

Analysis scripts (run with `uv run scripts/<name>.py`), recipes, and the routine runner.

## Recipes

`recipes/` holds scripts that compose `coachctl … --json` calls for questions asked more than once.
`recipes/README.md` is the catalog: check it before composing raw calls, and add a row when you add
a recipe. Recipes only read; external writes stay direct `coachctl` calls.

## Routines

`run_routine.sh <name> [extra context]` runs one routine as a headless `claude -p` turn from `$HOME`,
with this repo added through `--add-dir`. The prompt is `routines/<name>.md`. Each run appends to
`data/routines.log`.

- **Tool grants.** A headless run silently denies every tool not granted. `routines/<name>.tools`
  lists them, space separated (`Bash Read Write Edit Grep Glob`, `WebSearch`, an MCP server).
  `coachctl` runs through `Bash`; without a `.tools` file the runner grants `Bash` only.
- **External writes.** A routine that pushes workouts or creates routines does it with a direct
  `coachctl` call, never inside a recipe script, so the grant stays visible.
- **Verdict.** The run must end on a `DELIVERED`, `SILENT` or `FAILED` line. Anything else is a
  failure: logged, pinged to healthchecks as `/fail`, and sent to the athlete as a short
  `coachctl notify` notice when Telegram is configured.
- **Delivery.** A routine reaches the athlete through `coachctl notify` or, failing that, a journal
  entry the next session sees.
- **Watchdog.** Optional: one healthchecks.io check per routine, its ping URL in `healthchecks.env`
  as `<name>=<url>`. A dead machine stops every routine silently; this is how you notice.
- **Scheduling.** One crontab line per routine, e.g.
  `45 7 * * * /path/to/workspace/scripts/run_routine.sh readiness`. cron uses the system timezone.
- **Login.** All runs share the interactive claude.ai login, so the runner spaces starts at least
  20 s apart (needs `flock`). When the login dies, every run fails within seconds with an auth
  error in the log; the fix is an interactive `claude` then `/login`.

Test a routine by running it by hand and reading the last lines of `data/routines.log`.
