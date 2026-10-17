# Hevy with coachctl

Built from the Hevy public API documentation (https://api.hevyapp.com/docs). The API needs a
**Hevy Pro** subscription; the key comes from hevy.com → Settings → Developer
(`HEVY_API_KEY`).

## Reads

- `hevy workouts [--since D]`: logged workouts, newest first, each exercise with its sets. A page
  holds at most 10 workouts (an API limit); `--since` pages for you.
- `hevy workout <id>`: one workout. `--json` gives every field: `start_time`/`end_time` (UTC,
  ISO 8601), exercises with `exercise_template_id`, sets with `type`, `weight_kg`, `reps`,
  `distance_meters`, `duration_seconds`, `rpe`.
- `hevy history "<exact title>"` or `hevy history <template-id>`: every logged set of one exercise,
  grouped by workout, for progression. An inexact title lists the candidates.
- `hevy templates search <words> [--muscle M]`: template ids by title substring, built-in and
  custom. The catalog is cached for a day; `--refresh` after the athlete adds a custom exercise.
- `hevy routines`, `hevy folders`: existing routines and folder ids; `hevy routine show <id>`: one
  routine with every set.

Weights are kilograms; set `type` is `normal`, `warmup`, `failure` or `dropset`. RPE is 6–10 in
half steps, on logged workout sets only.

## Creating a routine

`hevy routine create` reads one routine object on stdin in the API's wire format and validates it
before sending:

```json
{
  "title": "Kraft A (KW43)",
  "folder_id": null,
  "notes": "Fokus: einbeinig, langsame Exzentrik",
  "exercises": [
    {
      "exercise_template_id": "<from templates search>",
      "superset_id": null,
      "rest_seconds": 120,
      "notes": "3 s exzentrisch",
      "sets": [
        { "type": "warmup", "weight_kg": 12, "reps": 10 },
        { "type": "normal", "weight_kg": 20, "rep_range": { "start": 8, "end": 10 } }
      ]
    }
  ]
}
```

- Routine sets take `reps` or a `rep_range`, never `rpe`.
- `folder_id: null` puts the routine in the default folder; take ids from `hevy folders`.
- Find every `exercise_template_id` with `templates search` first; a made-up id fails.
- Base weights on `hevy history` of that exercise, not on memory.
- `--dry-run` validates and prints the request; the real call prints the new routine's id.

## Updating a routine

Adjusting an existing routine is usually better than creating a new one each week: the athlete
keeps one routine per session type. `hevy routine update <id>` takes the same object as create and
**replaces** the exercise list, so start from `hevy routine show <id> --json > data/r.json`, edit
the file, and send it whole (`--dry-run` first). Send `folder_id` only to move the routine. coachctl
strips the fields the API returns but does not accept back and drops a null `rep_range`. The Hevy
API cannot delete a routine; retire one by renaming it.

## Logging a workout

`hevy workout create` logs a session that already happened (title, `start_time`, `end_time` in ISO
8601, exercises with sets that may carry `rpe`). Use it only for something actually done that is
missing from the log, with the athlete's OK: it may sync on to other platforms.

The routine appears in the athlete's app at once. Finished workouts may also reach other platforms
(a sync to Garmin or Intervals.icu), so a strength session can show up there as an activity of type
`WeightTraining` some time after it was logged.
