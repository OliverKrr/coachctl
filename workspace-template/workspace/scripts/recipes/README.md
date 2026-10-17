# Recipes

Scripts that answer a recurring multi-step question by composing `coachctl … --json` calls. Check
this catalog before composing raw calls; add a row when you add a recipe, and update "last verified"
when you run one and check its output.

Rules: a recipe only reads (no `coachctl icu workout`, no `coachctl hevy routine create`, no
`coachctl notify`); it takes its thresholds as parameters; it prints a compact answer. A recipe that
a routine depends on, or that stayed stable for weeks, is a candidate for a `coachctl` subcommand.

| Recipe | Question it answers | Call | Last verified |
| ------ | ------------------- | ---- | ------------- |
