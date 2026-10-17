# Intervals.icu with coachctl

Sources reviewed up to 2026-10-17. Contents: [Which command answers what](#which-command-answers-what) ·
[Activity types](#activity-types) · [Units and fields](#units-and-fields) ·
[Dates and timezones](#dates-and-timezones) · [Fitness, fatigue, form](#fitness-fatigue-form) ·
[Wellness](#wellness) · [Thresholds and sport settings](#thresholds-and-sport-settings) ·
[Planned workouts](#planned-workouts) · [Workout text format](#workout-text-format) ·
[Limits and errors](#limits-and-errors) · [Pitfalls](#pitfalls) ·
[When this guide runs out](#when-this-guide-runs-out)

## Which command answers what

| Coaching question                                    | Command                                                   |
| ---------------------------------------------------- | --------------------------------------------------------- |
| Today's date for the athlete, thresholds, zones      | `icu profile` (run it first; never use a remembered FTP)  |
| What was trained in a period, volume, load           | `icu activities --since D [--sport Run]`                  |
| One session in depth: intervals, zone times, decoupling | `icu activity <id>`                                    |
| Reps in detail: offsets, GAP, running dynamics per rep | `icu activity <id>` (text), `--json` for every field   |
| Pacing, drift, W' balance, anything per second       | `icu streams <id> > data/<id>.csv`, then a script         |
| Morning readiness: HRV, resting HR, sleep, soreness  | `icu wellness --days 14`                                  |
| Fitness trend, freshness before a race               | `icu fitness --days 42`                                   |
| Taper or load projection from exact numbers          | `icu fitness --since D --until D --json`                  |
| Update thresholds after a test                       | `icu settings set --type Run ... --dry-run`               |
| What is planned, races ahead                         | `icu events --since D --days N [--category RACE_A,...]`   |
| Planned against done for one week                    | `icu week [--date D]`                                     |
| Weekly volume over months                            | `icu summary --days 112 [--type Run] [--csv]`             |
| Power, pace or HR curves, gear, weather, anything else | `icu get <path>` (paths below), read-only               |

Useful `icu get` paths (`{athlete}` expands to the configured id):

- `/athlete/{athlete}/power-curves.json --query type=Ride --query curves=42d` (curve codes: `42d`,
  `1y`, `s0` current season, `s1` the season before, `all`, or `r.2026-01-01.2026-06-30`);
  `pace-curves.json`, `hr-curves.json` likewise (`--query gap=true` on pace curves for
  grade-adjusted bests). `type` stands for the whole sport, so `type=Run` includes treadmill and
  trail runs. Append `-kj0` or `-kj1` to a power-curve code (`90d-kj0`) for the durability curve:
  power produced only after the athlete's first or second work threshold, which the sport
  settings hold in kJ as `after_kj0`/`after_kj1`. Each curve carries the model fitted to it
  (`powerModels[]`: `criticalPower`, `wPrime`, `pMax`, `ftp`; `paceModels[]`: `criticalSpeed`,
  `dPrime`) and, per point, the `activity_id` that set it. Open that activity before quoting a
  best: a GPS or power spike makes a false one.
- `/athlete/{athlete}/activity-pace-curves.json --query oldest=D --query newest=D --query distances=5000,10000`:
  best efforts over distances within a period.
- `/athlete/{athlete}/gear.json`: gear names; activities carry only `gear.id`.
- `/activity/<id>/interval-stats --query start_index=N --query end_index=M`: averages (power,
  speed, GAP, HR, decoupling, running dynamics) for any stretch of a session. The indices are stream
  sample indices; each interval in `icu activity --json` carries `start_index`/`end_index` and
  `start_time`/`end_time` in seconds. The range is half-open: an interval covers samples from
  `start_index` up to, not including, `end_index`. Its `average_watts` is the mean of those `watts`
  samples cut to whole watts, while its `average_speed` is distance over moving time, not a stream
  mean.
- `/activity/<id>/best-efforts --query stream=watts --query duration=1200` (or `distance=` in
  metres; `count`, default 8): the best stretches inside one session, such as the best 20 minutes
  of a race.
- `/athlete/{athlete}/activities/interval-search --query minSecs=240 --query maxSecs=360 --query minIntensity=100 --query maxIntensity=120`:
  earlier sessions with reps of that length and intensity (percent of threshold), newest first.
  This answers "how did the last 5-minute reps compare?". `type` is not the sport but what the
  intensity is a percentage of: `POWER` (FTP), `HR` (LTHR), `PACE` (threshold pace) or `AUTO`;
  a basis whose threshold the sport settings lack matches nothing. `minReps`/`maxReps` bound how
  many intervals of a session must match, and `limit` caps the list (default 30).
- `/athlete/{athlete}/activities-around --query activity_id=<id>`: the sessions before and after one.
- `/activity/<id>/messages`: the activity's comment thread; skip entries marked `deleted`.
- `/activity/<id>/weather-summary`, `/activity/<id>/gap-histogram`.
- `/athlete/{athlete}/training-plan`: the assigned plan (null when none).
- `/athlete/{athlete}/connections`: which platforms feed the account.

## Activity types

An activity's `type` is one exact value: `Run`, `VirtualRun` (treadmill), `TrailRun`, `Ride`,
`VirtualRide`, `GravelRide`, `WeightTraining`, … Volume and load for a sport need the whole family.

- `icu activities --type Run` matches exactly and leaves out treadmill and trail runs.
  `icu activities --sport Run` takes the family from the athlete's sport settings, the groups
  `icu profile` prints (`Run/VirtualRun/TrailRun`). The athlete decides the groups: runs are
  usually `Run`, `VirtualRun`, `TrailRun`; rides `Ride`, `VirtualRide`, `MountainBikeRide`,
  `GravelRide`, `TrackRide`, `Cyclocross`, on some accounts also `EBikeRide`,
  `EMountainBikeRide`, `Velomobile`, `Handcycle`.
- `icu summary --type` picks a category of Intervals' weekly breakdown, and those categories are
  families already: `Run` counts `VirtualRun`, `Ride` counts `VirtualRide` and `GravelRide`,
  `Walk` counts `Hike`, `Workout` counts `WeightTraining`. A type without a category of its own
  (`VirtualRun`, `WeightTraining`) totals zero there.
- The curve endpoints' `type` covers the whole sport as well (see the paths above).

## Units and fields

The API speaks SI everywhere: metres, seconds, kilograms, metres per second. Convert before showing
anything; coachctl's text output already does for distance, time and pace.

- `distance` m (null for strength sessions: skip nulls when summing); `moving_time`,
  `elapsed_time` s; `average_speed` m/s; `total_elevation_gain` m.
- `icu_training_load`: the session's training load, comparable to TSS; for HR-only sessions it is
  heart-rate based. `icu_intensity`: intensity as a percentage of threshold (86 means IF 0.86).
- `decoupling` %: drift of HR against power or pace across the session; `icu_efficiency_factor`:
  output per heartbeat. Both are only meaningful for steady aerobic efforts.
- `icu_ftp`: the FTP stamped on the activity at analysis time. Activities keep their historic FTP
  when settings change later.
- `threshold_pace` is always m/s; `pace_units` (MINS_KM, SECS_100M, …) is only the display
  preference. 4.0 m/s is 4:10/km.
- Power zones and pace zones are stored as percentages of the threshold, so they move with it; HR
  zones are absolute bpm and do not.
- Running cadence may be stored per foot (half of steps per minute); check against the activity
  before judging it.
- `icu_intervals` (in `icu activity`): per interval `type` (WORK, RECOVERY, …), duration, distance,
  average speed, power, HR, `intensity`, `decoupling`, `zone`, `average_gradient` (a fraction), and
  `start_time`/`end_time`, seconds from the start, which the text output shows as `m:ss–m:ss`.
  Intervals come from device laps or from Intervals' own detection, depending on the athlete's
  laps setting, which applies only to activities analysed after it changed; know which you have
  before judging reps. No field records which it was: the text output's header says so by
  comparing the interval count with the device's `icu_lap_count` (lap-based intervals match it
  give or take one, after setting aside 1–2 s slivers where laps meet), and names intervals the
  athlete edited by hand (`icu_intervals_edited`).
- Running-dynamics averages (`average_stance_time`, `average_stance_time_balance`,
  `average_vertical_oscillation`, `average_vertical_ratio`, `average_step_length`, …) sit on the
  activity and, for runs recorded since September 2026, on each interval. They leave out
  standing-still zeros, so prefer them to a plain mean over the stream. Older runs get them only
  after a re-analysis in the web app. Values from a watch and from a foot pod are not comparable
  (vertical oscillation arrives in mm from one and cm from the other; a Garmin watch also stores
  step length in mm). `icu activity` text shows them on the activity and per interval as stored:
  GCT (stance time, ms), GCT balance (% left), VO, VR (%), step, LSS (leg spring stiffness).
- Grade-adjusted pace is native: `gap` (m/s) on the activity and on each interval (both in
  `icu activity` text), `gap_zone_times`;
  the sport's `gap_model` (`NONE` or `STRAVA_RUN`) says whether it is computed. Per-kilometre GAP
  needs the `ga_velocity` stream (grade-adjusted speed, m/s; `grade_smooth` is the gradient in %).
  Intervals computes both, so they are missing from the activity's `stream_types` and `icu streams`
  returns them only when named: `--types time,distance,ga_velocity`.
- `coasting_time` s is on every activity. W' balance: `icu_max_wbal_depletion` on the activity,
  `wbal_start`/`wbal_end` per interval, and the `w_bal` stream.
- Intervals carry more averages when a sensor recorded them: `average_dfa_a1` (DFA α1 from HRV),
  `average_respiration`, `average_smo2` (muscle oxygen), `average_lactate`, and `avg_lr_balance`
  (on the activity too). Weather sits on the activity and on each interval:
  `average_weather_temp`, `average_feels_like`, `headwind_percent`. Read it before calling an
  outdoor session slow.
- `feel` runs 1–5 with 1 = strong and 5 = weak. `perceived_exertion` is the athlete's RPE (1–10);
  `icu_rpe` and `session_rpe` exist too, undocumented, so look at all three before saying no RPE
  was logged.
- `carbs_ingested` is grams over the whole session; a planned event's `carbs_per_hour` is a rate.
- `paired_event_id` links a completed activity to the planned workout it matched; `compliance` is
  the percentage of that plan achieved (80–120 counts as on target).
- `source` says where an activity came from; see the Strava pitfall below.

## Dates and timezones

- **Anchor on the athlete's local date.** `icu profile` prints it with the weekday, from the
  athlete's timezone in Intervals.icu. Plans go wrong when a UTC date or a guessed weekday is used;
  state every planned day as weekday plus date and check the pair.
- Activities carry `start_date_local` (athlete time, no zone) and `start_date` (UTC). Calendar
  events carry local times only. Compare activities and events on the local fields.
- `--since`/`--until` are local calendar dates and inclusive; a date-only end means end of that day.
- An event without a time is stored at `T00:00:00`. Multi-day events (holiday, sick) end at an
  exclusive `end_date_local`: a sickness from Mon to Wed ends Thursday 00:00.
- Wellness records are keyed by local date: the record's `id` is `YYYY-MM-DD`.

## Fitness, fatigue, form

- CTL (fitness, ~42-day load average) and ATL (fatigue, ~7-day) live on the daily wellness
  records, not on a fitness endpoint. `icu fitness` reads them and computes form = CTL − ATL and form
  as a share of CTL. Its `--json` keeps CTL, ATL and form unrounded (the text rounds to 0.1): a
  projection that steps the model forward day by day drifts on rounded inputs.
- The day's load is on the same records: `ctlLoad` and `atlLoad`, the load that day fed into CTL
  and ATL, the sum of its activities' `icu_training_load` (`icu fitness` shows it as `load`). On
  today and later days it also counts planned workouts no activity has matched yet, so a forecast
  row's load is the planned load of that day.
- Only all-sports CTL/ATL exists. A per-sport fitness has to be computed from activity loads.
- `rampRate` on a day is the rolling ramp drawn on the fitness chart (CTL change per week).
- Planned workouts with a load feed the forecast: future CTL/ATL already include them. Days without
  planned load count as zero, so a sparse calendar draws a steady freshening that is no real taper;
  habitual unplanned load such as commuting is missing from it too.
- Read fitness and wellness before prescribing a session, not after.
- `icu summary` reports fitness as of the last activity in each week, which may not be Sunday.
- `icu_ctl`/`icu_atl` on an activity are the values after that activity.

## Wellness

- `restingHR` bpm, `hrv` the overnight HRV value the device reports (usually rMSSD, ms),
  `hrvSDNN` a separate SDNN value from some sources, `sleepSecs` s, `sleepScore` and `readiness`
  as the device scores them (`readiness` 0–100, higher is better), `weight` kg.
- Subjective fields `soreness`, `fatigue`, `stress`, `mood`, `motivation` and `sleepQuality` run
  1–4 with **1 = good, 4 = bad**. Wellness `fatigue` is how the athlete felt, unrelated to ATL.
  `injury` is 0 for none and higher the worse it is.
- Nutrition fields (`kcalConsumed`, `carbohydrates`, `protein`, `fatTotal`) are day totals: a
  write replaces the day's value, it does not add to it. Device-sensed fields (`hrv`,
  `restingHR`, `sleepSecs`, `steps`) come from the watch; never write them by hand.
- A missing night is usually sync delay: the watch syncs to its platform, which syncs on to
  Intervals.icu. Treat absence as "unknown", never as a low value.
- Custom wellness fields appear only when the athlete defined them in their settings.

## Thresholds and sport settings

Each sport's settings (`icu profile` shows them) hold FTP, threshold pace, LTHR, max HR, W' and the
zones, for a group of activity types (Run often covers VirtualRun and TrailRun too).

- `icu settings set --type Run --threshold-pace 3:55 --lthr 168 --dry-run` shows old → new; drop
  `--dry-run` to write (`PUT /athlete/{id}/sport-settings/{type}`). Pace is given as m:ss per km
  (or m:ss/100m) and stored as m/s. Only the fields passed are sent.
- Power and pace zones are percentages of the threshold and move with it. HR zones are bpm and stay
  put when LTHR or max HR change, unless `--recalc-hr-zones` has Intervals recompute them; check the
  zones `icu profile` shows afterwards against the athlete's own zone model.
- Ride settings keep `indoor_ftp` apart from `ftp` (`icu profile` shows both), and a planned
  event carries an `indoor` flag (`icu workout push --indoor`): check which FTP applies before
  writing % FTP targets for the trainer. `w_prime` is in joules.
- Activities keep the thresholds they were analysed with (`icu_ftp` on an activity); a new
  threshold changes later analysis, not history.
- Change a threshold on evidence (a test, a race, a sustained shift in curve models) and log why in
  the journal; the athlete confirms before the write.

## Planned workouts

`icu workout push` creates a calendar event. The rules that matter:

- **The description is the workout.** Intervals.icu parses the description's step lines (format
  below) into structured steps, computes duration, load, intensity and zone times, and syncs the
  workout to a connected watch platform. `--type` is required with a description. Notes and the
  athlete's free text go first, step lines after, in the same description.
- After a push, coachctl reports how many steps were parsed. Zero steps means nothing structured
  reaches the watch: fix the syntax and `icu workout edit <id> --description-stdin`.
- A description without steps (a free-text session) needs `--moving-time` and `--load` (on push or
  edit), or the fitness forecast ignores it.
- `--time HH:MM` on push gives the event a start time, which orders several sessions on one day;
  without it the event sits at midnight.
- **Check the weekday with the date.** Pass the plan's weekday with every `--date` on push and edit
  (`--date 2026-10-21 --weekday Wed`); coachctl refuses a date on another weekday before sending
  anything.
- **Edit in place, don't delete and recreate.** `icu workout edit` keeps the event id, and an edit
  updates the workout already on the watch; delete-and-recreate leaves duplicates and breaks
  pairing. A delete is permanent. The API's DELETE also takes `others=true`, which removes every
  event created in the same request as that one, such as a whole uploaded plan; coachctl never
  sends it. Change the date with `--date`, the text with `--description-stdin`. For a change across
  many days, list the event ids first (`icu events --json`), confirm the whole set once, then edit
  one event at a time.
- **Re-pushing a plan**: give each planned session a stable `--external-id` (e.g.
  `plan-2026w44-tue`). coachctl looks the key up on the calendar (60 days back to 240 days ahead)
  and updates that event instead of adding a second one, even when the date changed. Intervals'
  own upsert works only for OAuth apps, and it replaces any `uid` you send with its own.
- Zone or percentage targets need the sport's thresholds and zones set in Intervals.icu
  (`icu profile` shows them). Pace targets without pace zones resolve to nothing on the watch.
  Check before choosing the target type; do not silently switch to HR.
- Fixed targets reach the watch as ranges (Intervals widens power ±2.5 % for outdoor workouts, pace
  ±2.5 %, HR ±1.5 %). Write explicit ranges when the exact band matters, and keep a range the athlete
  or the plan states as that range; its midpoint puts a different band on the watch.
- Leave the athlete's tags alone; they drive filters in their own views. Provenance belongs in
  `--external-id`.
- `--carbs-per-hour` on push or edit sets `carbs_per_hour` (g/h), the fuelling target the calendar
  shows; `icu events` prints it. Events also carry `load_target`; read it with `icu events --json`.
- A workout reaches the watch only when the athlete's watch connection has planned-workout upload
  switched on, and one dated in the past may not sync at all.
- `icu events --json --resolve` shows planned targets as watts, bpm and m/s at the thresholds of
  that day.
- Strength sessions plan as `--type WeightTraining` with the main lifts in the description.
  Intervals.icu keeps time, heart rate and load for them, never exercises, sets or weights, and the
  watch gets timed steps without exercise names: the strength app stays the record of what was
  lifted. The recording pairs with the planned event like any other session. Marking a planned
  workout done (`POST /athlete/{id}/events/{eventId}/mark-done`) creates an activity, so use it only
  when no device or sync will deliver the session, or it counts twice.
- The `joules` field of a planned workout is not refreshed after API writes; ignore it.

## Workout text format

One step per line, starting with `- `: optional cue text, a duration or distance, a target,
optional cadence. Section headers are plain lines; a repeat block starts with a line `Nx` (or a
header ending in `Nx`) and is separated by blank lines before and after. Repeats do not nest.

```text
Locker einlaufen, Fokus Kadenz.

Warmup
- 15m Z2 Pace

Strides 4x
- 20s 110% Pace
- 40s Z1 Pace

Main set 6x
- 1km 4:00-4:05/km Pace
- 90s Z1 Pace

Cooldown
- 10m Z1-Z2 Pace
```

| Element   | Forms                                                                                     |
| --------- | ----------------------------------------------------------------------------------------- |
| Duration  | `30s`, `10m`, `1h`, `1h20m`, `1m30`                                                       |
| Distance  | `400mtr`, `2km`, `1mi` (`m` means minutes, so metres are `mtr`)                           |
| Power     | `75%` of FTP (bare, not `75% FTP`), `88-94%`, `250w`, `230-250w`, zones `Z2`, `Z3-Z4`     |
| Heart rate | `70% HR` (of max HR), `95% LTHR`, `Z2 HR`                                               |
| Pace      | `90% Pace` (of threshold pace), `Z2 Pace`, `4:30/km Pace`, `1:45/100m Pace`              |
| Ramp      | `- 10m ramp 50%-75%`, downwards too (`ramp 70-45%`); also with watts, HR or Pace          |
| Cadence   | append `90rpm` or `85-95rpm`                                                              |
| Cue text  | words before the duration: `- Erholung 3m 55%`                                            |
| Step role | append `intensity=warmup`, `cooldown`, `interval`, `recovery` or `rest`                   |
| Rest      | `- 30s intensity=rest`: a rest step needs no target                                       |
| Lap button | `- Press lap 30s intensity=rest` waits for the lap button; keep the duration           |

Without `HR` or `Pace`, a percentage or zone means power. A run with power targets therefore needs
`Pace` or `HR` on every step that should not be power; a ride takes no pace targets. A `Press lap`
step with no duration no longer reaches a Garmin watch. Markdown in the description is allowed
and ignored by the parser.

## Limits and errors

- A personal API key allows 5000 requests a day and 2500 per rolling 15 minutes, plus about 10
  requests per second per IP. coachctl retries one 429 after the server's `Retry-After` (up to
  10 s); a further 429 exits with code 3. Loops over many activities belong in a script that paces
  itself (~0.1 s between calls) and reuses one list call instead of many detail calls.
- 401/403 means a bad key or athlete id: the key is the password, the username is literally
  `API_KEY`, and the id usually starts with `i` (very old accounts have digits only). Athlete id
  `0` also works and means the key's own athlete.
- 404 is a wrong path or id; 422 is a validation error (an unknown stream type, a name too long).
- Event ids are integers; activity ids are strings like `i12345678`. Mixing them up gives odd
  errors.

## Pitfalls

- **Strava stubs.** Activities that reached Intervals.icu only through Strava come back as empty
  stubs (Strava's terms): coachctl marks them `[Strava stub]` and says why, and the API refuses to
  update them. The fix is connecting the watch platform directly. Judge by the activity's `source`,
  not by `strava_id`.
- **Description versus comments.** An update to a completed activity's `description` overwrites it.
  Feedback for the athlete belongs in a comment (`POST /activity/{id}/messages` with `content`),
  which the athlete and their connected coaches see: an outward-facing write to confirm first.
  coachctl writes neither; `icu get /activity/<id>/messages` reads the thread.
- **Garmin data access** was under discussion in October 2026 (new Garmin developer terms). As of
  2026-10-17 nothing is blocked. If wellness or activity data from Garmin suddenly stops arriving
  while it is visible in the Intervals.icu web app, suspect an API restriction, check the forum
  thread below, and tell the athlete. The API terms (section 1.1) require a Garmin attribution,
  as Garmin's brand guidelines describe it, wherever Garmin-recorded data is shown to people: a
  report or chart for others carries one.
- **Stream names.** One unknown type fails the whole streams request; `icu streams` without
  `--types` asks only for types the activity has (`stream_types`), which leaves out computed ones
  such as `ga_velocity`: name those. A sensor gap is an empty CSV cell and `null` in `--json`; a
  script skips it rather than reading it as zero.
- **Read after write.** A just-pushed event or a just-synced activity can take a moment to show in
  lists; re-read before concluding it is missing. In a long session, re-read numbers fetched hours
  ago rather than reusing them, and run `icu profile` again after a settings change or once the
  session has crossed midnight.
- **Rewritten activities.** Services that post-process uploads (a strength log converted to FIT, a
  merge of duplicates) can replace an activity under a new id some minutes after it first appears.

## When this guide runs out

Go back to the sources when a question is not covered here, an endpoint behaves unexpectedly, or a
new capability might exist:

- The OpenAPI spec, pinned in the coachctl repo at `docs/refs/intervals-openapi.json`; live at
  https://intervals.icu/api/v1/docs, browsable at https://intervals.icu/api-docs.html.
- "API access to Intervals.icu" (https://forum.intervals.icu/t/api-access-to-intervals-icu/609),
  the long-running developer thread: auth, limits, streams, settings writes.
- The Intervals.icu API terms
  (https://forum.intervals.icu/t/intervals-icu-api-terms-and-conditions/114087): what a client may
  do with the data, including the Garmin attribution.
- "IcuSync – Claude AI MCP connector for Intervals.icu"
  (https://forum.intervals.icu/t/icusync-claude-ai-mcp-connector-for-intervals-icu-no-technical-setup-required/126632):
  its author's feature updates show what an agent can do with the API, its tool list
  (https://icusync.icu/tools) states many field and workout-text rules, and the fixes in its
  changelog (https://icusync.icu/changelog) often reveal API behaviour. IcuSync is the no-setup
  option for claude.ai users.
- The forum's workout-builder guides for the description syntax.

Discourse serves a thread as JSON: append `.json` to the thread URL (`?page=N` for later pages) to
read the newest posts. Folding findings from posts after the "sources reviewed" date above into this
guide is a recurring maintenance step for the package.
