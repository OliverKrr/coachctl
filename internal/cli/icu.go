package cli

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/OliverKrr/coachctl/internal/apierr"
	"github.com/OliverKrr/coachctl/internal/config"
	"github.com/OliverKrr/coachctl/internal/icu"
	"github.com/OliverKrr/coachctl/internal/store"
	"github.com/spf13/cobra"
)

var defaultActivityFields = []string{
	"id", "start_date_local", "type", "name", "distance", "moving_time",
	"icu_training_load", "icu_intensity", "average_heartrate", "total_elevation_gain",
}

var defaultWellnessFields = []string{
	"id", "ctl", "atl", "rampRate", "restingHR", "hrv", "sleepSecs", "sleepScore",
	"readiness", "weight", "soreness", "fatigue", "stress", "mood", "motivation", "comments",
}

func (a *App) icuClient() (*icu.Client, error) {
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	if !cfg.Has(config.KeyIntervalsKey) || !cfg.Has(config.KeyIntervalsID) {
		return nil, usagef("Intervals.icu is not configured: add %s and %s to %s (`coachctl doctor` shows what is set)", config.KeyIntervalsKey, config.KeyIntervalsID, cfg.Path)
	}
	c := icu.New(cfg.Get(config.KeyIntervalsBase), cfg.Get(config.KeyIntervalsID), cfg.Get(config.KeyIntervalsKey))
	c.UserAgent = "coachctl/" + a.Version
	return c, nil
}

// dateRange resolves --since/--until/--days against today in the coaching
// timezone. days counts back from until, inclusive.
type dateRange struct {
	since, until string
	days         int
}

func (r *dateRange) flags(cmd *cobra.Command, defaultDays int) {
	cmd.Flags().StringVar(&r.since, "since", "", "first date, YYYY-MM-DD")
	cmd.Flags().StringVar(&r.until, "until", "", "last date, YYYY-MM-DD (default today)")
	cmd.Flags().IntVar(&r.days, "days", defaultDays, "number of days ending at --until, when --since is not given")
}

func (a *App) resolveRange(r dateRange) (string, string, error) {
	loc, err := a.location()
	if err != nil {
		return "", "", err
	}
	until := r.until
	if until == "" {
		until = a.Now().In(loc).Format("2006-01-02")
	} else if _, err := store.ParseDate(until); err != nil {
		return "", "", err
	}
	since := r.since
	if since == "" {
		if r.days <= 0 {
			return "", "", usagef("--days must be positive")
		}
		u, _ := time.Parse("2006-01-02", until)
		since = u.AddDate(0, 0, -(r.days - 1)).Format("2006-01-02")
	} else if _, err := store.ParseDate(since); err != nil {
		return "", "", err
	}
	if since > until {
		return "", "", usagef("--since %s is after --until %s", since, until)
	}
	return since, until, nil
}

func (a *App) icuCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "icu",
		Short: "Intervals.icu: profile, activities, wellness, fitness, calendar, workouts, settings",
		Long: `Read and write the athlete's Intervals.icu account with INTERVALS_API_KEY and
INTERVALS_ATHLETE_ID. Reads are safe to run freely; ` + "`icu workout`" + ` writes to the
calendar and ` + "`icu settings set`" + ` to the sport settings, both with --dry-run.
Field meanings, units and the workout text format are in the coachctl skill's
references/intervals.md.`,
	}
	cmd.AddCommand(
		a.icuProfileCmd(), a.icuActivitiesCmd(), a.icuActivityCmd(), a.icuStreamsCmd(),
		a.icuWellnessCmd(), a.icuFitnessCmd(), a.icuEventsCmd(), a.icuWeekCmd(),
		a.icuSummaryCmd(), a.icuWorkoutCmd(), a.icuSettingsCmd(), a.icuGetCmd(),
	)
	return cmd
}

// sportProfile is the curated per-sport view of the athlete's settings.
type sportProfile struct {
	Types         []string  `json:"types"`
	FTP           *float64  `json:"ftp,omitempty"`
	IndoorFTP     *float64  `json:"indoor_ftp,omitempty"`
	LTHR          *float64  `json:"lthr,omitempty"`
	MaxHR         *float64  `json:"max_hr,omitempty"`
	ThresholdPace *float64  `json:"threshold_pace_mps,omitempty"`
	ThresholdText string    `json:"threshold_pace,omitempty"`
	PaceUnits     string    `json:"pace_units,omitempty"`
	WPrime        *float64  `json:"w_prime,omitempty"`
	PMax          *float64  `json:"p_max,omitempty"`
	PowerZones    []float64 `json:"power_zones_pct_ftp,omitempty"`
	HRZones       []float64 `json:"hr_zones_bpm,omitempty"`
	PaceZones     []float64 `json:"pace_zones_pct_threshold,omitempty"`
}

type profileOut struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Timezone         string         `json:"timezone"`
	CurrentDateLocal string         `json:"current_date_local"`
	Weekday          string         `json:"weekday"`
	Sex              string         `json:"sex,omitempty"`
	WeightKg         *float64       `json:"weight_kg,omitempty"`
	RestingHR        *float64       `json:"resting_hr,omitempty"`
	Sports           []sportProfile `json:"sports"`
}

func optNum(o icu.Object, key string) *float64 {
	if v, ok := icu.Num(o, key); ok {
		return &v
	}
	return nil
}

func numList(o icu.Object, key string) []float64 {
	arr, ok := o[key].([]any)
	if !ok {
		return nil
	}
	out := make([]float64, 0, len(arr))
	for _, v := range arr {
		if f, ok := v.(float64); ok {
			out = append(out, f)
		}
	}
	return out
}

func buildProfile(ath icu.Object, now time.Time, fallback *time.Location) profileOut {
	today, loc := icu.TodayIn(now, icu.Str(ath, "timezone"), fallback)
	p := profileOut{
		ID: icu.Str(ath, "id"), Name: icu.Str(ath, "name"), Timezone: loc.String(),
		CurrentDateLocal: today.Format("2006-01-02"), Weekday: today.Weekday().String(),
		Sex: icu.Str(ath, "sex"), WeightKg: optNum(ath, "icu_weight"), RestingHR: optNum(ath, "icu_resting_hr"),
	}
	if p.WeightKg == nil {
		p.WeightKg = optNum(ath, "weight")
	}
	settings, _ := ath["sportSettings"].([]any)
	for _, raw := range settings {
		s, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		sp := sportProfile{
			FTP: optNum(s, "ftp"), IndoorFTP: optNum(s, "indoor_ftp"), LTHR: optNum(s, "lthr"),
			MaxHR: optNum(s, "max_hr"), ThresholdPace: optNum(s, "threshold_pace"), PaceUnits: icu.Str(s, "pace_units"),
			WPrime: optNum(s, "w_prime"), PMax: optNum(s, "p_max"),
			PowerZones: numList(s, "power_zones"), HRZones: numList(s, "hr_zones"), PaceZones: numList(s, "pace_zones"),
		}
		if types, ok := s["types"].([]any); ok {
			for _, t := range types {
				if ts, ok := t.(string); ok {
					sp.Types = append(sp.Types, ts)
				}
			}
		}
		if sp.ThresholdPace != nil {
			sp.ThresholdText = icu.Pace(*sp.ThresholdPace, strings.Contains(sp.PaceUnits, "100"))
		}
		p.Sports = append(p.Sports, sp)
	}
	return p
}

func fmtNums(v []float64) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strings.Join(parts, " ")
}

func (a *App) icuProfileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "profile",
		Short: "Athlete profile: today's date in the athlete's zone, thresholds and zones per sport",
		Long: `Today's date in the athlete's timezone plus FTP, LTHR, max HR, threshold pace
and zones per sport, as set in Intervals.icu. Run it before any training
analysis: these values are authoritative, remembered ones drift.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			loc, err := a.location()
			if err != nil {
				return err
			}
			ath, err := c.Athlete(context.Background())
			if err != nil {
				return err
			}
			p := buildProfile(ath, a.Now(), loc)
			if a.JSON {
				return a.printJSON(p)
			}
			a.printf("Athlete: %s (%s), timezone %s\n", p.Name, p.ID, p.Timezone)
			a.printf("Today (athlete timezone): %s %s\n", p.Weekday[:3], p.CurrentDateLocal)
			var body []string
			if p.WeightKg != nil {
				body = append(body, fmt.Sprintf("weight %s kg", fmtValue(*p.WeightKg, "")))
			}
			if p.RestingHR != nil {
				body = append(body, fmt.Sprintf("resting HR %s bpm", fmtValue(*p.RestingHR, "")))
			}
			if len(body) > 0 {
				a.printf("%s\n", strings.Join(body, ", "))
			}
			for _, sp := range p.Sports {
				var parts []string
				add := func(label string, v *float64, unit string) {
					if v != nil && *v > 0 {
						parts = append(parts, fmt.Sprintf("%s %s", label, fmtValue(*v, unit)))
					}
				}
				add("FTP", sp.FTP, "W")
				add("indoor FTP", sp.IndoorFTP, "W")
				if sp.ThresholdText != "" {
					parts = append(parts, "threshold pace "+sp.ThresholdText)
				}
				add("LTHR", sp.LTHR, "bpm")
				add("max HR", sp.MaxHR, "bpm")
				add("W'", sp.WPrime, "J")
				a.printf("\n%s: %s\n", strings.Join(sp.Types, "/"), strings.Join(parts, ", "))
				if len(sp.PowerZones) > 0 {
					a.printf("  power zones, %% of FTP: %s\n", fmtNums(sp.PowerZones))
				}
				if len(sp.HRZones) > 0 {
					a.printf("  HR zones, bpm: %s\n", fmtNums(sp.HRZones))
				}
				if len(sp.PaceZones) > 0 {
					a.printf("  pace zones, %% of threshold: %s\n", fmtNums(sp.PaceZones))
				}
			}
			return nil
		},
	}
}

// csvValue renders one field for CSV; nested values become JSON.
func csvValue(o icu.Object, field string) string {
	v, ok := o[field]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func (a *App) writeCSV(fields []string, rows []icu.Object) error {
	w := csv.NewWriter(a.Stdout)
	if err := w.Write(fields); err != nil {
		return err
	}
	for _, r := range rows {
		rec := make([]string, len(fields))
		for i, f := range fields {
			rec[i] = csvValue(r, f)
		}
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func pick(rows []icu.Object, fields []string) []icu.Object {
	out := make([]icu.Object, len(rows))
	for i, r := range rows {
		o := icu.Object{}
		for _, f := range fields {
			if v, ok := r[f]; ok {
				o[f] = v
			}
		}
		out[i] = o
	}
	return out
}

func filterTypes(rows []icu.Object, types []string) []icu.Object {
	if len(types) == 0 {
		return rows
	}
	want := map[string]bool{}
	for _, t := range types {
		want[strings.ToLower(t)] = true
	}
	var out []icu.Object
	for _, r := range rows {
		if want[strings.ToLower(icu.Str(r, "type"))] {
			out = append(out, r)
		}
	}
	return out
}

// sportTypes expands each sport to every activity type the athlete's sport
// settings group with it: Run becomes Run, VirtualRun, TrailRun when the Run
// settings cover all three.
func sportTypes(ath icu.Object, sports []string) ([]string, error) {
	settings, _ := ath["sportSettings"].([]any)
	var groups [][]string
	for _, raw := range settings {
		if s, ok := raw.(map[string]any); ok {
			groups = append(groups, stringList(s, "types"))
		}
	}
	var out []string
	for _, sport := range sports {
		found := false
		for _, g := range groups {
			if slices.ContainsFunc(g, func(t string) bool { return strings.EqualFold(t, sport) }) {
				found = true
				for _, t := range g {
					if !slices.Contains(out, t) {
						out = append(out, t)
					}
				}
			}
		}
		if !found {
			names := make([]string, len(groups))
			for i, g := range groups {
				names[i] = strings.Join(g, "/")
			}
			return nil, usagef("no sport settings cover %q (the athlete's sports: %s); use --type %s to match that type alone", sport, strings.Join(names, ", "), sport)
		}
	}
	return out, nil
}

func activityLine(o icu.Object) string {
	date := icu.Str(o, "start_date_local")
	if len(date) >= 16 {
		date = strings.Replace(date[:16], "T", " ", 1)
	}
	parts := []string{date, icu.Str(o, "type"), strconv.Quote(icu.Str(o, "name"))}
	if d, ok := icu.Num(o, "distance"); ok && d > 0 {
		parts = append(parts, fmt.Sprintf("%.1f km", d/1000))
	}
	if t, ok := icu.Num(o, "moving_time"); ok && t > 0 {
		parts = append(parts, icu.Duration(t))
	}
	if l, ok := icu.Num(o, "icu_training_load"); ok {
		parts = append(parts, fmt.Sprintf("load %.0f", l))
	}
	if v, ok := icu.Num(o, "icu_intensity"); ok && v > 0 {
		parts = append(parts, fmt.Sprintf("IF %.0f%%", v))
	}
	if hr, ok := icu.Num(o, "average_heartrate"); ok && hr > 0 {
		parts = append(parts, fmt.Sprintf("HR %.0f", hr))
	}
	if e, ok := icu.Num(o, "total_elevation_gain"); ok && e > 0 {
		parts = append(parts, fmt.Sprintf("+%.0f m", e))
	}
	line := strings.Join(parts, "  ") + "  id " + icu.Str(o, "id")
	if isStravaStub(o) {
		line += "  [Strava stub: no data via the API]"
	}
	return line
}

func (a *App) icuActivitiesCmd() *cobra.Command {
	var r dateRange
	var types, sports, fields []string
	var asCSV bool
	cmd := &cobra.Command{
		Use:   "activities",
		Short: "Completed activities in a date range (training history, CSV export)",
		Long: `Completed activities, oldest first. --type matches activity types exactly:
--type Run leaves out treadmill (VirtualRun) and trail runs (TrailRun).
--sport Run takes every type the athlete's Run sport settings cover, as
icu profile lists them (e.g. Run/VirtualRun/TrailRun). --type and --sport
combine: --sport Run --type Walk is all runs plus walks.`,
		Example: `  coachctl icu activities --days 14
  coachctl icu activities --since 2026-06-01 --sport Run --csv > data/runs.csv
  coachctl icu activities --days 28 --type VirtualRide
  coachctl icu activities --days 7 --fields id,name,decoupling,icu_efficiency_factor --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, until, err := a.resolveRange(r)
			if err != nil {
				return err
			}
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			ctx := context.Background()
			if len(sports) > 0 {
				ath, err := c.Athlete(ctx)
				if err != nil {
					return err
				}
				family, err := sportTypes(ath, sports)
				if err != nil {
					return err
				}
				types = withFields(types, family...)
			}
			// The API returns only the named fields: keep the id for --json,
			// and fetch type when --type or --sport filters on it.
			keep := fields
			if len(fields) > 0 {
				keep = withFields(fields, "id")
				if len(types) > 0 {
					keep = withFields(keep, "type")
				}
			}
			rows, err := c.Activities(ctx, since, until, keep)
			if err != nil {
				return err
			}
			rows = filterTypes(rows, types)
			cols := fields
			if len(cols) == 0 {
				cols = defaultActivityFields
			}
			switch {
			case asCSV:
				return a.writeCSV(cols, rows)
			case a.JSON:
				if len(fields) > 0 {
					return a.printJSON(nonNil(pick(rows, keep)))
				}
				return a.printJSON(nonNil(rows))
			}
			// Name the types in the text output: --sport expands to types the
			// caller never typed.
			of := ""
			if len(types) > 0 {
				of = " of type " + strings.Join(types, "/")
			}
			if len(rows) == 0 {
				a.printf("No activities%s %s – %s.\n", of, since, until)
				return nil
			}
			var dist, secs, load float64
			for _, o := range rows {
				a.printf("%s\n", activityLine(o))
				d, _ := icu.Num(o, "distance")
				t, _ := icu.Num(o, "moving_time")
				l, _ := icu.Num(o, "icu_training_load")
				dist, secs, load = dist+d, secs+t, load+l
			}
			a.printf("\n%d activities%s %s – %s: %.1f km, %s, load %.0f\n", len(rows), of, since, until, dist/1000, icu.Duration(secs), load)
			return nil
		},
	}
	r.flags(cmd, 7)
	cmd.Flags().StringSliceVar(&types, "type", nil, "only these activity types, matched exactly, e.g. Run,VirtualRun,WeightTraining")
	cmd.Flags().StringSliceVar(&sports, "sport", nil, "every type the athlete's sport settings group with these, e.g. Run for Run/VirtualRun/TrailRun")
	cmd.Flags().StringSliceVar(&fields, "fields", nil, "fields to return (API names; see references/intervals.md); --json always keeps id")
	cmd.Flags().BoolVar(&asCSV, "csv", false, "RFC 4180 CSV with a header row")
	return cmd
}

func (a *App) icuActivityCmd() *cobra.Command {
	var noIntervals bool
	cmd := &cobra.Command{
		Use:   "activity <id>",
		Short: "One activity in detail: summary metrics, zone times, intervals",
		Example: `  coachctl icu activity i98765432
  coachctl icu activity i98765432 --json > data/act.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			o, err := c.Activity(context.Background(), args[0], !noIntervals)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(o)
			}
			a.printActivity(o)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noIntervals, "no-intervals", false, "skip the interval breakdown")
	return cmd
}

// isStravaStub recognises an activity that reached Intervals.icu through
// Strava: Strava's terms let the API return only a stub for it. The source
// field decides; strava_id is set on any activity Strava ever touched.
func isStravaStub(o icu.Object) bool { return strings.EqualFold(icu.Str(o, "source"), "STRAVA") }

const stravaStubHint = "This activity came in through Strava, and Strava's terms let the Intervals.icu API return only a stub for it. Connect the watch or platform to Intervals.icu directly (Settings → Connections) to analyse such activities."

func (a *App) printActivity(o icu.Object) {
	a.printf("%s\n", activityLine(o))
	if isStravaStub(o) {
		a.printf("%s\n", stravaStubHint)
		return
	}
	type metric struct{ label, key, unit, format string }
	metrics := []metric{
		{"avg power", "icu_average_watts", "W", "%.0f"}, {"weighted power", "icu_weighted_avg_watts", "W", "%.0f"},
		{"FTP at the time", "icu_ftp", "W", "%.0f"}, {"max HR", "max_heartrate", "bpm", "%.0f"},
		{"decoupling", "decoupling", "%", "%.1f"}, {"efficiency factor", "icu_efficiency_factor", "", "%.2f"},
		{"variability index", "icu_variability_index", "", "%.2f"}, {"cadence", "average_cadence", "", "%.0f"},
		{"TRIMP", "trimp", "", "%.0f"}, {"RPE", "icu_rpe", "", "%.0f"}, {"feel", "feel", "", "%.0f"},
		{"calories", "calories", "kcal", "%.0f"}, {"avg temp", "average_temp", "°C", "%.1f"},
	}
	var parts []string
	if v, ok := icu.Num(o, "average_speed"); ok && v > 0 {
		parts = append(parts, "avg pace "+icu.Pace(v, false))
	}
	if v, ok := icu.Num(o, "gap"); ok && v > 0 {
		parts = append(parts, "GAP "+icu.Pace(v, false))
	}
	for _, m := range metrics {
		if v, ok := icu.Num(o, m.key); ok && v != 0 {
			s := fmt.Sprintf(m.format, v)
			if m.unit != "" {
				s += " " + m.unit
			}
			parts = append(parts, m.label+" "+s)
		}
	}
	if len(parts) > 0 {
		a.printf("%s\n", strings.Join(parts, ", "))
	}
	if hz := numList(o, "icu_hr_zone_times"); len(hz) > 0 {
		a.printf("HR zone times (Z1…): %s\n", zoneTimes(hz))
	}
	if pz := numList(o, "pace_zone_times"); len(pz) > 0 {
		a.printf("pace zone times (Z1…): %s\n", zoneTimes(pz))
	}
	if zs, ok := o["icu_zone_times"].([]any); ok && len(zs) > 0 {
		var secs []float64
		for _, z := range zs {
			if m, ok := z.(map[string]any); ok {
				v, _ := icu.Num(m, "secs")
				secs = append(secs, v)
			}
		}
		a.printf("power zone times (Z1…): %s\n", zoneTimes(secs))
	}
	if dyn := runningDynamics(o); len(dyn) > 0 {
		a.printf("running dynamics: %s\n", strings.Join(dyn, ", "))
	}
	if d := strings.TrimSpace(icu.Str(o, "description")); d != "" {
		a.printf("description: %s\n", d)
	}
	raws, _ := o["icu_intervals"].([]any)
	var intervals []icu.Object
	for _, raw := range raws {
		if iv, ok := raw.(map[string]any); ok {
			intervals = append(intervals, iv)
		}
	}
	if len(intervals) == 0 {
		return
	}
	a.printf("\n%s\n", intervalSource(o, intervals))
	for _, iv := range intervals {
		parts := []string{fmt.Sprintf("%-8s", icu.Str(iv, "type"))}
		start, ok1 := icu.Num(iv, "start_time")
		end, ok2 := icu.Num(iv, "end_time")
		if ok1 && ok2 {
			parts = append(parts, clock(start)+"–"+clock(end))
		}
		if l := icu.Str(iv, "label"); l != "" {
			parts = append(parts, l)
		}
		if d, ok := icu.Num(iv, "distance"); ok && d > 0 {
			parts = append(parts, fmt.Sprintf("%.2f km", d/1000))
		}
		if t, ok := icu.Num(iv, "moving_time"); ok && t > 0 {
			parts = append(parts, icu.Duration(t))
		}
		if v, ok := icu.Num(iv, "average_speed"); ok && v > 0 {
			parts = append(parts, icu.Pace(v, false))
		}
		if v, ok := icu.Num(iv, "gap"); ok && v > 0 {
			parts = append(parts, "GAP "+icu.Pace(v, false))
		}
		if w, ok := icu.Num(iv, "average_watts"); ok && w > 0 {
			parts = append(parts, fmt.Sprintf("%.0f W", w))
		}
		if hr, ok := icu.Num(iv, "average_heartrate"); ok && hr > 0 {
			parts = append(parts, fmt.Sprintf("HR %.0f", hr))
		}
		if hr, ok := icu.Num(iv, "max_heartrate"); ok && hr > 0 {
			parts = append(parts, fmt.Sprintf("max %.0f", hr))
		}
		if z, ok := icu.Num(iv, "zone"); ok && z > 0 {
			parts = append(parts, fmt.Sprintf("Z%.0f", z))
		}
		parts = append(parts, runningDynamics(iv)...)
		a.printf("  %s\n", strings.Join(parts, "  "))
	}
}

// runningDynamics renders the running-dynamics averages an activity or
// interval carries. Vertical oscillation and step length are shown as stored:
// their unit depends on the recording device (references/intervals.md).
func runningDynamics(o icu.Object) []string {
	type field struct{ label, key, format string }
	var out []string
	for _, f := range []field{
		{"GCT", "average_stance_time", "%.0f ms"},
		{"GCT balance", "average_stance_time_balance", "%.1f%% L"},
		{"VO", "average_vertical_oscillation", "%.1f"},
		{"VR", "average_vertical_ratio", "%.1f%%"},
		{"step", "average_step_length", "%.0f"},
		{"LSS", "average_leg_spring_stiffness", "%.1f"},
	} {
		if v, ok := icu.Num(o, f.key); ok && v > 0 {
			out = append(out, f.label+" "+fmt.Sprintf(f.format, v))
		}
	}
	return out
}

// intervalSource says whether an activity's intervals follow the device's
// laps or Intervals.icu's own detection. The API has no field for it (the
// sport's laps setting at analysis time decides), so it compares counts:
// lap-based intervals match the lap count give or take one, after dropping
// the 1–2 s slivers that appear where laps meet.
func intervalSource(o icu.Object, intervals []icu.Object) string {
	if b, _ := o["icu_intervals_edited"].(bool); b {
		return "intervals (edited by hand in Intervals.icu):"
	}
	laps, ok := icu.Num(o, "icu_lap_count")
	if !ok {
		return "intervals:"
	}
	n := 0
	for _, iv := range intervals {
		if t, ok := icu.Num(iv, "elapsed_time"); !ok || t > 2 {
			n++
		}
	}
	switch {
	case laps <= 1:
		return "intervals (detected by Intervals.icu; the device recorded no laps):"
	case math.Abs(float64(n)-laps) <= 1:
		return fmt.Sprintf("intervals (from the device's %.0f laps):", laps)
	default:
		return fmt.Sprintf("intervals (detected by Intervals.icu, not the device's %.0f laps):", laps)
	}
}

// clock renders an offset in seconds as m:ss, or h:mm:ss from an hour on.
func clock(secs float64) string {
	s := int(math.Round(secs))
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func zoneTimes(secs []float64) string {
	parts := make([]string, len(secs))
	for i, s := range secs {
		parts[i] = fmt.Sprintf("Z%d %s", i+1, minutes(s))
	}
	return strings.Join(parts, ", ")
}

func minutes(secs float64) string { return fmt.Sprintf("%.0fm", secs/60) }

func (a *App) icuStreamsCmd() *cobra.Command {
	var types []string
	cmd := &cobra.Command{
		Use:   "streams <id>",
		Short: "Per-sample data of an activity as CSV (or --json)",
		Long: `Per-second channels for analysis scripts. Redirect to a file; the output is
large. Common types: time, distance, velocity_smooth, heartrate, watts,
cadence, altitude, grade_smooth, latlng.

Without --types, coachctl asks for the default channels the activity actually
has (its stream_types): the API rejects the whole request when one requested
type does not exist. With --types, every name you pass must exist.

A missing sample (a sensor gap) is an empty cell in the CSV and null in --json.`,
		Example: `  coachctl icu streams i98765432 > data/streams.csv
  coachctl icu streams i98765432 --types time,heartrate,velocity_smooth > data/streams.csv`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			ctx := context.Background()
			if !cmd.Flags().Changed("types") {
				act, err := c.Activity(ctx, args[0], false)
				if err != nil {
					return err
				}
				if isStravaStub(act) {
					return usagef("%s", stravaStubHint)
				}
				have := map[string]bool{}
				if st, ok := act["stream_types"].([]any); ok {
					for _, t := range st {
						if s, ok := t.(string); ok {
							have[s] = true
						}
					}
				}
				var avail []string
				for _, t := range types {
					if have[t] {
						avail = append(avail, t)
					}
				}
				if len(avail) == 0 {
					return usagef("activity %s has none of the default streams (%s); name others with --types", args[0], strings.Join(types, ","))
				}
				types = avail
			}
			streams, err := c.Streams(ctx, args[0], types)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(streams)
			}
			w := csv.NewWriter(a.Stdout)
			header := make([]string, len(streams))
			n := 0
			for i, s := range streams {
				header[i] = s.Type
				n = max(n, len(s.Data))
			}
			if err := w.Write(header); err != nil {
				return err
			}
			rec := make([]string, len(streams))
			for row := 0; row < n; row++ {
				for i, s := range streams {
					rec[i] = ""
					if row < len(s.Data) && !math.IsNaN(s.Data[row]) {
						rec[i] = strconv.FormatFloat(s.Data[row], 'f', -1, 64)
					}
				}
				if err := w.Write(rec); err != nil {
					return err
				}
			}
			w.Flush()
			return w.Error()
		},
	}
	cmd.Flags().StringSliceVar(&types, "types", []string{"time", "distance", "velocity_smooth", "heartrate", "watts", "cadence", "altitude"}, "stream types")
	return cmd
}

func (a *App) printTable(fields []string, rows []icu.Object) {
	// Drop columns that are empty in every row: wellness records carry many
	// fields a given athlete never fills.
	var cols []string
	for _, f := range fields {
		for _, r := range rows {
			if csvValue(r, f) != "" {
				cols = append(cols, f)
				break
			}
		}
	}
	widths := make([]int, len(cols))
	cells := make([][]string, len(rows))
	for i, c := range cols {
		widths[i] = len(c)
	}
	for j, r := range rows {
		cells[j] = make([]string, len(cols))
		for i, c := range cols {
			v := csvValue(r, c)
			if f, err := strconv.ParseFloat(v, 64); err == nil && strings.Contains(v, ".") {
				v = strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64)
			}
			if c == "sleepSecs" && v != "" {
				f, _ := strconv.ParseFloat(v, 64)
				v = fmt.Sprintf("%.1fh", f/3600)
			}
			cells[j][i] = v
			widths[i] = max(widths[i], len([]rune(v)))
		}
	}
	line := func(vals []string) {
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = v + strings.Repeat(" ", widths[i]-len([]rune(v)))
		}
		a.printf("%s\n", strings.TrimRight(strings.Join(parts, "  "), " "))
	}
	line(cols)
	for _, c := range cells {
		line(c)
	}
}

func (a *App) icuWellnessCmd() *cobra.Command {
	var r dateRange
	var fields []string
	var asCSV bool
	cmd := &cobra.Command{
		Use:   "wellness",
		Short: "Daily wellness: HRV, resting HR, sleep, weight, subjective scores, CTL/ATL",
		Example: `  coachctl icu wellness --days 14
  coachctl icu wellness --since 2026-09-01 --csv > data/wellness.csv`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, until, err := a.resolveRange(r)
			if err != nil {
				return err
			}
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			rows, err := c.Wellness(context.Background(), since, until)
			if err != nil {
				return err
			}
			cols := fields
			if len(cols) == 0 {
				cols = defaultWellnessFields
			}
			switch {
			case asCSV:
				return a.writeCSV(cols, rows)
			case a.JSON:
				if len(fields) > 0 {
					return a.printJSON(nonNil(pick(rows, withFields(fields, "id"))))
				}
				return a.printJSON(nonNil(rows))
			}
			if len(rows) == 0 {
				a.printf("No wellness records %s – %s.\n", since, until)
				return nil
			}
			a.printTable(cols, rows)
			return nil
		},
	}
	r.flags(cmd, 7)
	cmd.Flags().StringSliceVar(&fields, "fields", nil, "fields to show (API names)")
	cmd.Flags().BoolVar(&asCSV, "csv", false, "RFC 4180 CSV with a header row")
	return cmd
}

// withFields puts each of extra in front of fields unless fields names it.
func withFields(fields []string, extra ...string) []string {
	var out []string
	for _, f := range extra {
		if !slices.Contains(fields, f) {
			out = append(out, f)
		}
	}
	return append(out, fields...)
}

// fitnessDay carries the model at full precision: projections that compound
// CTL and ATL day by day drift when fed values rounded to 0.1. Text output
// rounds.
type fitnessDay struct {
	Date     string   `json:"date"`
	CTL      float64  `json:"ctl"`
	ATL      float64  `json:"atl"`
	Form     float64  `json:"form"`
	FormPct  *float64 `json:"form_pct,omitempty"`
	RampRate *float64 `json:"ramp_rate,omitempty"`
	Load     *float64 `json:"load,omitempty"`
}

func (a *App) icuFitnessCmd() *cobra.Command {
	var r dateRange
	cmd := &cobra.Command{
		Use:   "fitness",
		Short: "Fitness model per day: CTL (fitness), ATL (fatigue), form = CTL − ATL, the day's load",
		Long: `One row per day from the wellness records: CTL, ATL, form = CTL − ATL, form as
a share of CTL, ramp rate, and load, the day's training load as the model
counted it (the wellness field ctlLoad). On today and later days that load
includes planned workouts no activity has matched yet, which is what the
forecast rows are built from. --json keeps CTL, ATL and form unrounded for
projections; text rounds to 0.1.`,
		Example: `  coachctl icu fitness
  coachctl icu fitness --since 2026-10-01 --until 2026-11-15 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, until, err := a.resolveRange(r)
			if err != nil {
				return err
			}
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			rows, err := c.Wellness(context.Background(), since, until)
			if err != nil {
				return err
			}
			var days []fitnessDay
			for _, w := range rows {
				ctl, ok1 := icu.Num(w, "ctl")
				atl, ok2 := icu.Num(w, "atl")
				if !ok1 || !ok2 {
					continue
				}
				d := fitnessDay{Date: icu.Str(w, "id"), CTL: ctl, ATL: atl, Form: ctl - atl, RampRate: optNum(w, "rampRate"), Load: optNum(w, "ctlLoad")}
				if ctl > 0 {
					pct := (ctl - atl) / ctl * 100
					d.FormPct = &pct
				}
				days = append(days, d)
			}
			if a.JSON {
				return a.printJSON(nonNil(days))
			}
			if len(days) == 0 {
				a.printf("No fitness data %s – %s.\n", since, until)
				return nil
			}
			r1 := func(v float64) string { return fmtValue(round1(v), "") }
			last := days[len(days)-1]
			a.printf("%s: fitness (CTL) %s, fatigue (ATL) %s, form %s", last.Date, r1(last.CTL), r1(last.ATL), r1(last.Form))
			if last.FormPct != nil {
				a.printf(" (%s%%)", r1(*last.FormPct))
			}
			if last.RampRate != nil {
				a.printf(", ramp rate %s", r1(*last.RampRate))
			}
			a.printf("\n\ndate        CTL    ATL    form   load\n")
			for _, d := range days {
				load := ""
				if d.Load != nil {
					load = fmt.Sprintf("%.0f", *d.Load)
				}
				a.printf("%s\n", strings.TrimRight(fmt.Sprintf("%s  %-5s  %-5s  %-5s  %s", d.Date, r1(d.CTL), r1(d.ATL), r1(d.Form), load), " "))
			}
			return nil
		},
	}
	r.flags(cmd, 28)
	return cmd
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func eventLine(o icu.Object, withDescription bool) string {
	date := icu.Str(o, "start_date_local")
	if t, err := time.Parse("2006-01-02T15:04:05", date); err == nil {
		date = t.Format("Mon 2006-01-02")
		if t.Hour() != 0 || t.Minute() != 0 {
			date += t.Format(" 15:04")
		}
	}
	parts := []string{date, icu.Str(o, "category")}
	if t := icu.Str(o, "type"); t != "" {
		parts = append(parts, t)
	}
	parts = append(parts, strconv.Quote(icu.Str(o, "name")))
	if t, ok := icu.Num(o, "moving_time"); ok && t > 0 {
		parts = append(parts, icu.Duration(t))
	}
	if d, ok := icu.Num(o, "distance"); ok && d > 0 {
		parts = append(parts, fmt.Sprintf("%.1f km", d/1000))
	}
	if l, ok := icu.Num(o, "icu_training_load"); ok && l > 0 {
		parts = append(parts, fmt.Sprintf("load %.0f", l))
	}
	if c, ok := icu.Num(o, "carbs_per_hour"); ok && c > 0 {
		parts = append(parts, fmt.Sprintf("carbs %.0f g/h", c))
	}
	line := strings.Join(parts, "  ") + "  id " + icu.Str(o, "id")
	if withDescription {
		if d := strings.TrimSpace(icu.Str(o, "description")); d != "" {
			line += "\n    " + strings.ReplaceAll(d, "\n", "\n    ")
		}
	}
	return line
}

func (a *App) icuEventsCmd() *cobra.Command {
	var r dateRange
	var categories []string
	var full, resolve bool
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Calendar: planned workouts, races, notes",
		Long:  "Calendar events in a date range. Categories include WORKOUT, RACE_A, RACE_B, RACE_C, NOTE, PLAN, HOLIDAY, SICK, INJURED.",
		Example: `  coachctl icu events --since 2026-10-19 --days 7
  coachctl icu events --days 120 --until 2027-02-15 --category RACE_A,RACE_B
  coachctl icu events --since 2026-10-20 --days 1 --full`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if r.since != "" && r.until == "" && !cmd.Flags().Changed("days") {
				r.days = 7
			}
			since, until, err := a.resolveEventRange(r)
			if err != nil {
				return err
			}
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			rows, err := c.Events(context.Background(), since, until, categories, resolve)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(nonNil(rows))
			}
			if len(rows) == 0 {
				a.printf("No events %s – %s.\n", since, until)
				return nil
			}
			for _, o := range rows {
				a.printf("%s\n", eventLine(o, full))
			}
			return nil
		},
	}
	r.flags(cmd, 7)
	cmd.Flags().StringSliceVar(&categories, "category", nil, "only these categories")
	cmd.Flags().BoolVar(&full, "full", false, "include descriptions")
	cmd.Flags().BoolVar(&resolve, "resolve", false, "with --json: workout_doc targets as watts, bpm and m/s at the thresholds of the day")
	return cmd
}

// resolveEventRange is resolveRange, except that a --since without --until
// counts --days forward: the calendar is mostly read ahead.
func (a *App) resolveEventRange(r dateRange) (string, string, error) {
	if r.since != "" && r.until == "" {
		if _, err := store.ParseDate(r.since); err != nil {
			return "", "", err
		}
		s, _ := time.Parse("2006-01-02", r.since)
		r.until = s.AddDate(0, 0, max(r.days, 1)-1).Format("2006-01-02")
	}
	return a.resolveRange(r)
}

func (a *App) icuWeekCmd() *cobra.Command {
	var date string
	cmd := &cobra.Command{
		Use:   "week",
		Short: "One Mon–Sun week: planned workouts next to completed activities, with totals",
		Example: `  coachctl icu week
  coachctl icu week --date 2026-10-26 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := a.location()
			if err != nil {
				return err
			}
			day := a.Now().In(loc)
			if date != "" {
				if _, err := store.ParseDate(date); err != nil {
					return err
				}
				day, _ = time.ParseInLocation("2006-01-02", date, loc)
			}
			offset := (int(day.Weekday()) + 6) % 7
			monday := time.Date(day.Year(), day.Month(), day.Day()-offset, 0, 0, 0, 0, loc)
			sunday := monday.AddDate(0, 0, 6)
			mon, sun := monday.Format("2006-01-02"), sunday.Format("2006-01-02")
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			ctx := context.Background()
			events, err := c.Events(ctx, mon, sun, []string{"WORKOUT", "RACE_A", "RACE_B", "RACE_C", "NOTE"}, false)
			if err != nil {
				return err
			}
			acts, err := c.Activities(ctx, mon, sun, nil)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(map[string]any{"monday": mon, "sunday": sun, "planned": nonNil(events), "done": nonNil(acts)})
			}
			a.printf("Week %s – %s\n", monday.Format("Mon 2006-01-02"), sunday.Format("Sun 2006-01-02"))
			byDay := func(rows []icu.Object, d string) []icu.Object {
				var out []icu.Object
				for _, r := range rows {
					if strings.HasPrefix(icu.Str(r, "start_date_local"), d) {
						out = append(out, r)
					}
				}
				return out
			}
			var plannedLoad float64
			for i := 0; i < 7; i++ {
				d := monday.AddDate(0, 0, i)
				ds := d.Format("2006-01-02")
				a.printf("\n%s\n", d.Format("Mon 2006-01-02"))
				for _, e := range byDay(events, ds) {
					if l, ok := icu.Num(e, "icu_training_load"); ok && icu.Str(e, "category") == "WORKOUT" {
						plannedLoad += l
					}
					a.printf("  planned  %s\n", eventLine(e, false))
				}
				for _, act := range byDay(acts, ds) {
					a.printf("  done     %s\n", activityLine(act))
				}
			}
			type total struct {
				n                int
				dist, secs, load float64
			}
			totals := map[string]*total{}
			var all total
			for _, act := range acts {
				t := icu.Str(act, "type")
				if totals[t] == nil {
					totals[t] = &total{}
				}
				d, _ := icu.Num(act, "distance")
				s, _ := icu.Num(act, "moving_time")
				l, _ := icu.Num(act, "icu_training_load")
				for _, tt := range []*total{totals[t], &all} {
					tt.n++
					tt.dist += d
					tt.secs += s
					tt.load += l
				}
			}
			keys := make([]string, 0, len(totals))
			for k := range totals {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			a.printf("\nDone: %d activities, %s, load %.0f (planned workout load %.0f)\n", all.n, icu.Duration(all.secs), all.load, plannedLoad)
			for _, k := range keys {
				t := totals[k]
				a.printf("  %s: %d×, %.1f km, %s, load %.0f\n", k, t.n, t.dist/1000, icu.Duration(t.secs), t.load)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "any day in the week (default today)")
	return cmd
}

var summaryFields = []string{"date", "count", "distance", "moving_time", "training_load", "fitness", "fatigue", "form", "rampRate", "eftp", "weight"}

func (a *App) icuSummaryCmd() *cobra.Command {
	var r dateRange
	var asCSV bool
	var sport string
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Weekly totals: count, distance, time, load, fitness at week end",
		Long: `One row per week (date = the week's Monday) with totals and the fitness model.
--type limits the totals to one category of Intervals' weekly breakdown, and
the categories already group related types: Run counts VirtualRun too, Ride
counts VirtualRide and GravelRide, Workout counts WeightTraining. A type that
is not a category of its own (WeightTraining) totals zero.`,
		Example: `  coachctl icu summary --days 84
  coachctl icu summary --since 2026-01-01 --type Run --csv > data/run_weeks.csv`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, until, err := a.resolveRange(r)
			if err != nil {
				return err
			}
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			rows, err := c.Summary(context.Background(), since, until)
			if err != nil {
				return err
			}
			if sport != "" {
				rows = summaryForType(rows, sport)
			}
			switch {
			case asCSV:
				return a.writeCSV(summaryFields, rows)
			case a.JSON:
				return a.printJSON(nonNil(rows))
			}
			if len(rows) == 0 {
				a.printf("No weeks %s – %s.\n", since, until)
				return nil
			}
			for _, w := range rows {
				d, _ := icu.Num(w, "distance")
				t, _ := icu.Num(w, "moving_time")
				l, _ := icu.Num(w, "training_load")
				line := fmt.Sprintf("%s  %2s×  %6.1f km  %9s  load %4.0f", icu.Str(w, "date"), icu.Str(w, "count"), d/1000, icu.Duration(t), l)
				if f, ok := icu.Num(w, "fitness"); ok {
					fa, _ := icu.Num(w, "fatigue")
					line += fmt.Sprintf("  CTL %.0f ATL %.0f form %.0f", f, fa, f-fa)
				}
				a.printf("%s\n", line)
			}
			return nil
		},
	}
	r.flags(cmd, 56)
	cmd.Flags().BoolVar(&asCSV, "csv", false, "RFC 4180 CSV with a header row")
	cmd.Flags().StringVar(&sport, "type", "", "totals for one category, e.g. Run (VirtualRun included)")
	return cmd
}

// summaryForType replaces each week's totals with one category's totals,
// keeping the fitness model fields.
func summaryForType(rows []icu.Object, sport string) []icu.Object {
	out := make([]icu.Object, 0, len(rows))
	for _, w := range rows {
		o := icu.Object{"date": w["date"], "fitness": w["fitness"], "fatigue": w["fatigue"], "form": w["form"], "rampRate": w["rampRate"], "count": 0.0, "distance": 0.0, "moving_time": 0.0, "training_load": 0.0}
		cats, _ := w["byCategory"].([]any)
		for _, raw := range cats {
			c, ok := raw.(map[string]any)
			if !ok || !strings.EqualFold(icu.Str(c, "category"), sport) {
				continue
			}
			for _, k := range []string{"count", "distance", "moving_time", "training_load", "eftp"} {
				if v, ok := c[k]; ok {
					o[k] = v
				}
			}
		}
		out = append(out, o)
	}
	return out
}

func (a *App) icuWorkoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workout",
		Short: "Write planned workouts to the calendar (push, edit, delete)",
		Long: `Writes to the athlete's calendar. Every subcommand supports --dry-run, which
prints the request and sends nothing. The description uses Intervals.icu's
workout text format (see the coachctl skill, references/intervals.md);
Intervals parses it into structured steps that sync to the watch.`,
	}
	cmd.AddCommand(a.icuWorkoutPushCmd(), a.icuWorkoutEditCmd(), a.icuWorkoutDeleteCmd())
	return cmd
}

// parseWeekday reads Mon…Sun, or the full English name, in any case.
func parseWeekday(s string) (time.Weekday, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for d := time.Sunday; d <= time.Saturday; d++ {
		name := strings.ToLower(d.String())
		if s == name || s == name[:3] {
			return d, true
		}
	}
	return 0, false
}

// checkWeekday refuses a date that does not fall on the weekday the plan
// names, so an off-by-one date fails before anything is written.
func checkWeekday(date, weekday string) error {
	if weekday == "" {
		return nil
	}
	want, ok := parseWeekday(weekday)
	if !ok {
		return usagef("--weekday %q must be one of Mon, Tue, Wed, Thu, Fri, Sat, Sun", weekday)
	}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return usagef("--date %q must be YYYY-MM-DD", date)
	}
	if d.Weekday() != want {
		return usagef("--date %s is a %s, not a %s (--weekday %s): nothing was sent; check which day the plan means", date, d.Weekday(), want, weekday)
	}
	return nil
}

// noNegatives rejects negative values of the named integer flags.
func noNegatives(cmd *cobra.Command, names ...string) error {
	for _, n := range names {
		if v, err := cmd.Flags().GetInt(n); err == nil && v < 0 {
			return usagef("--%s must not be negative", n)
		}
	}
	return nil
}

func (a *App) icuWorkoutPushCmd() *cobra.Command {
	var date, at, sport, name, externalID, category, weekday string
	var movingTime, load, carbs int
	var indoor, dryRun bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Create a planned workout (description on stdin)",
		Example: `  coachctl icu workout push --date 2026-10-21 --weekday Wed --type Run --name "6x1k" <<'EOF'
Warmup
- 15m Z2 Pace

6x
- 1km 95-100% Pace
- 2m Z1 Pace

Cooldown
- 10m Z2 Pace
EOF
  coachctl icu workout push --date 2026-10-22 --weekday Thu --type Ride --name "Z2" --carbs-per-hour 60 --external-id plan-2026w43-thu --dry-run <<'EOF'
- 2h 65-75%
EOF`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := store.ParseDate(date); err != nil {
				return err
			}
			if err := checkWeekday(date, weekday); err != nil {
				return err
			}
			if name == "" || sport == "" {
				return usagef("--name and --type are required")
			}
			if err := noNegatives(cmd, "moving-time", "load", "carbs-per-hour"); err != nil {
				return err
			}
			startTime := "00:00:00"
			if at != "" {
				t, err := time.Parse("15:04", at)
				if err != nil {
					return usagef("--time %q must be HH:MM", at)
				}
				startTime = t.Format("15:04:05")
			}
			desc, err := a.readStdin("the workout description")
			if err != nil {
				return err
			}
			event := icu.Object{
				"category": category, "start_date_local": date + "T" + startTime,
				"type": sport, "name": name, "description": strings.TrimSpace(desc),
			}
			if movingTime > 0 {
				event["moving_time"] = movingTime
			}
			if load > 0 {
				event["icu_training_load"] = load
			}
			if carbs > 0 {
				event["carbs_per_hour"] = carbs
			}
			if indoor {
				event["indoor"] = true
			}
			if externalID != "" {
				event["external_id"] = externalID
			}
			if dryRun && externalID == "" {
				return a.dryRun("POST", "/athlete/{athlete}/events", event)
			}
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			ctx := context.Background()
			if externalID != "" {
				existing, err := a.findPlanned(ctx, c, externalID, date)
				if err != nil {
					return err
				}
				if existing != "" {
					path := "/athlete/{athlete}/events/" + existing
					if dryRun {
						return a.dryRun("PUT", path, event)
					}
					out, err := c.UpdateEvent(ctx, existing, event)
					if err != nil {
						return err
					}
					return a.reportEvent("Updated", out)
				}
				if dryRun {
					return a.dryRun("POST", "/athlete/{athlete}/events", event)
				}
			}
			out, err := c.CreateEvent(ctx, event)
			if err != nil {
				return err
			}
			return a.reportEvent("Created", out)
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "day of the workout, YYYY-MM-DD (required)")
	cmd.Flags().StringVar(&weekday, "weekday", "", weekdayHelp)
	cmd.Flags().StringVar(&at, "time", "", "start time HH:MM (default: no time)")
	cmd.Flags().StringVar(&sport, "type", "", "activity type: Run, Ride, Swim, WeightTraining, … (required)")
	cmd.Flags().StringVar(&name, "name", "", "workout name (required)")
	cmd.Flags().StringVar(&category, "category", "WORKOUT", "event category: WORKOUT, NOTE, RACE_A, …")
	cmd.Flags().IntVar(&movingTime, "moving-time", 0, "planned duration in seconds, when the description has no steps")
	cmd.Flags().IntVar(&load, "load", 0, "planned training load, when Intervals cannot compute it")
	cmd.Flags().IntVar(&carbs, "carbs-per-hour", 0, carbsHelp)
	cmd.Flags().BoolVar(&indoor, "indoor", false, "mark as an indoor workout")
	cmd.Flags().StringVar(&externalID, "external-id", "", "your stable key for this session: pushing a key already on the calendar updates that event instead of adding one")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the request, send nothing")
	_ = cmd.MarkFlagRequired("date")
	return cmd
}

// findPlanned returns the id of the calendar event carrying externalID, or "".
// It searches from 60 days before the earlier of today and date to 240 days
// after the later, so a session moved to another day is still found.
func (a *App) findPlanned(ctx context.Context, c *icu.Client, externalID, date string) (string, error) {
	loc, err := a.location()
	if err != nil {
		return "", err
	}
	today := a.Now().In(loc).Format("2006-01-02")
	lo, hi := min(date, today), max(date, today)
	l, _ := time.Parse("2006-01-02", lo)
	h, _ := time.Parse("2006-01-02", hi)
	ev, n, err := c.FindByExternalID(ctx, externalID, l.AddDate(0, 0, -60).Format("2006-01-02"), h.AddDate(0, 0, 240).Format("2006-01-02"))
	if err != nil || ev == nil {
		return "", err
	}
	if n > 1 {
		_, _ = fmt.Fprintf(a.Stderr, "coachctl: %d events carry external id %q; updating %s, delete the others\n", n, externalID, icu.Str(ev, "id"))
	}
	return icu.Str(ev, "id"), nil
}

func (a *App) dryRun(method, path string, body any) error {
	if a.JSON {
		return a.printJSON(map[string]any{"dry_run": true, "method": method, "path": path, "body": body})
	}
	a.printf("Dry run, nothing sent: %s %s\n", method, path)
	if body != nil {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(body)
	}
	return nil
}

func (a *App) reportEvent(verb string, out icu.Object) error {
	if a.JSON {
		return a.printJSON(out)
	}
	a.printf("%s event %s: %s\n", verb, icu.Str(out, "id"), eventLine(out, false))
	if icu.Str(out, "category") == "WORKOUT" {
		steps := 0
		if doc, ok := out["workout_doc"].(map[string]any); ok {
			if s, ok := doc["steps"].([]any); ok {
				steps = len(s)
			}
		}
		if steps == 0 {
			a.printf("⚠ Intervals parsed no workout steps from the description, so nothing structured syncs to the watch. Check the syntax (references/intervals.md) and edit the event.\n")
		} else {
			a.printf("Parsed %d top-level step%s.\n", steps, plural(steps))
		}
	}
	return nil
}

const (
	weekdayHelp = "refuse the call unless --date falls on this weekday (Mon…Sun): catches an off-by-one date before anything is sent"
	carbsHelp   = "fuelling target in grams of carbohydrate per hour, shown on the calendar"
)

func (a *App) icuWorkoutEditCmd() *cobra.Command {
	var date, name, sport, weekday string
	var movingTime, load, carbs int
	var fromStdin, dryRun bool
	cmd := &cobra.Command{
		Use:   "edit <event-id>",
		Short: "Change a planned workout's date, name, type, description, load, duration or fuelling",
		Example: `  coachctl icu workout edit 12345678 --date 2026-10-23 --weekday Fri
  coachctl icu workout edit 12345678 --load 45 --moving-time 3600 --carbs-per-hour 60
  coachctl icu workout edit 12345678 --description-stdin <<'EOF'
- 45m Z2 Pace
EOF`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fields := icu.Object{}
			if weekday != "" && date == "" {
				return usagef("--weekday checks --date: pass the new --date with it")
			}
			if date != "" {
				if _, err := store.ParseDate(date); err != nil {
					return err
				}
				if err := checkWeekday(date, weekday); err != nil {
					return err
				}
				fields["start_date_local"] = date + "T00:00:00"
			}
			if err := noNegatives(cmd, "moving-time", "load", "carbs-per-hour"); err != nil {
				return err
			}
			if name != "" {
				fields["name"] = name
			}
			if sport != "" {
				fields["type"] = sport
			}
			if cmd.Flags().Changed("moving-time") {
				fields["moving_time"] = movingTime
			}
			if cmd.Flags().Changed("load") {
				fields["icu_training_load"] = load
			}
			if cmd.Flags().Changed("carbs-per-hour") {
				fields["carbs_per_hour"] = carbs
			}
			if fromStdin {
				desc, err := a.readStdin("the new description")
				if err != nil {
					return err
				}
				fields["description"] = strings.TrimSpace(desc)
			}
			if len(fields) == 0 {
				return usagef("nothing to change: pass --date, --name, --type, --load, --moving-time, --carbs-per-hour or --description-stdin")
			}
			path := "/athlete/{athlete}/events/" + url.PathEscape(args[0])
			if dryRun {
				return a.dryRun("PUT", path, fields)
			}
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			out, err := c.UpdateEvent(context.Background(), args[0], fields)
			if err != nil {
				return err
			}
			return a.reportEvent("Updated", out)
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "move to this day, YYYY-MM-DD")
	cmd.Flags().StringVar(&weekday, "weekday", "", weekdayHelp)
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&sport, "type", "", "new activity type")
	cmd.Flags().IntVar(&movingTime, "moving-time", 0, "planned duration in seconds, when the description has no steps")
	cmd.Flags().IntVar(&load, "load", 0, "planned training load, when Intervals cannot compute it")
	cmd.Flags().IntVar(&carbs, "carbs-per-hour", 0, carbsHelp)
	cmd.Flags().BoolVar(&fromStdin, "description-stdin", false, "replace the description with stdin")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the request, send nothing")
	return cmd
}

func (a *App) icuWorkoutDeleteCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:     "delete <event-id>",
		Short:   "Delete a calendar event (shows what it deletes)",
		Example: "  coachctl icu workout delete 12345678 --dry-run",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			ctx := context.Background()
			ev, err := c.Event(ctx, args[0])
			if err != nil {
				return err
			}
			if dryRun {
				if a.JSON {
					return a.printJSON(map[string]any{"dry_run": true, "would_delete": ev})
				}
				a.printf("Dry run, nothing deleted. Would delete: %s\n", eventLine(ev, false))
				return nil
			}
			if err := c.DeleteEvent(ctx, args[0]); err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(map[string]any{"deleted": ev})
			}
			a.printf("Deleted: %s\n", eventLine(ev, false))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the event, delete nothing")
	return cmd
}

// settingField is one threshold `icu settings set` can change.
type settingField struct{ flag, key, label, unit string }

var settingFields = []settingField{
	{"ftp", "ftp", "FTP", "W"},
	{"threshold-pace", "threshold_pace", "threshold pace", ""},
	{"lthr", "lthr", "LTHR", "bpm"},
	{"max-hr", "max_hr", "max HR", "bpm"},
	{"w-prime", "w_prime", "W'", "J"},
}

// guardedSettings are the fields a settings write must not touch unless asked.
var guardedSettings = []string{"ftp", "indoor_ftp", "threshold_pace", "lthr", "max_hr", "w_prime", "power_zones", "pace_zones", "hr_zones"}

type settingChange struct {
	Field string   `json:"field"`
	Old   *float64 `json:"old"`
	New   float64  `json:"new"`
}

func (f settingField) show(v *float64, per100m bool) string {
	switch {
	case v == nil:
		return "unset"
	case f.key == "threshold_pace":
		return icu.Pace(*v, per100m)
	}
	return fmtValue(*v, f.unit)
}

// parsePace reads m:ss per km (optionally with /km), or m:ss/100m, as m/s.
func parsePace(s string) (float64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	dist := 1000.0
	if strings.HasSuffix(t, "/100m") {
		dist, t = 100, strings.TrimSuffix(t, "/100m")
	}
	t = strings.TrimSuffix(t, "/km")
	m, sec, ok := strings.Cut(t, ":")
	mi, err1 := strconv.Atoi(m)
	se, err2 := strconv.Atoi(sec)
	if !ok || err1 != nil || err2 != nil || len(sec) != 2 || mi < 0 || se < 0 || se > 59 || mi*60+se == 0 {
		return 0, usagef("--threshold-pace %q must be m:ss per km (4:10 or 4:10/km), or m:ss/100m for swimming", s)
	}
	return dist / float64(mi*60+se), nil
}

func (a *App) icuSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Change a sport's thresholds: FTP, threshold pace, LTHR, max HR, W'",
		Long:  "`coachctl icu profile` reads the settings; `icu settings set` changes them and\nsupports --dry-run.",
	}
	cmd.AddCommand(a.icuSettingsSetCmd())
	return cmd
}

func (a *App) icuSettingsSetCmd() *cobra.Command {
	var sport, pace string
	var ftp, lthr, maxHR, wPrime int
	var recalcHR, dryRun bool
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set one sport's thresholds, showing old → new (--dry-run sends nothing)",
		Long: `Changes the thresholds of the sport settings that cover --type, e.g. after a
test. Only the fields you pass are sent. Power and pace zones are stored as
percentages, so they move with FTP and threshold pace; HR zones are absolute
bpm and keep their values unless --recalc-hr-zones has Intervals.icu recompute
them from LTHR and max HR. Activities keep the thresholds they were analysed
with.

coachctl reads the settings before the write and compares them with what the
write returns: if a threshold or zone it was not asked to change moved, it
exits 1 and names it.`,
		Example: `  coachctl icu settings set --type Run --threshold-pace 3:55 --lthr 168 --dry-run
  coachctl icu settings set --type Ride --ftp 265
  coachctl icu settings set --type Run --max-hr 189 --recalc-hr-zones`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sport == "" {
				return usagef("--type is required: the sport whose settings change, e.g. Run or Ride")
			}
			body := icu.Object{}
			want := map[string]float64{}
			for _, f := range []struct {
				flag, key string
				v         int
			}{{"ftp", "ftp", ftp}, {"lthr", "lthr", lthr}, {"max-hr", "max_hr", maxHR}, {"w-prime", "w_prime", wPrime}} {
				if !cmd.Flags().Changed(f.flag) {
					continue
				}
				if f.v <= 0 {
					return usagef("--%s must be positive", f.flag)
				}
				body[f.key], want[f.key] = f.v, float64(f.v)
			}
			if cmd.Flags().Changed("threshold-pace") {
				mps, err := parsePace(pace)
				if err != nil {
					return err
				}
				body["threshold_pace"], want["threshold_pace"] = mps, mps
			}
			if len(body) == 0 && !recalcHR {
				return usagef("nothing to change: pass --ftp, --threshold-pace, --lthr, --max-hr, --w-prime or --recalc-hr-zones")
			}
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			ctx := context.Background()
			old, err := c.SportSettings(ctx, sport)
			var upstream *apierr.Error
			if errors.As(err, &upstream) && upstream.Status == http.StatusNotFound {
				return usagef("Intervals.icu has no sport settings covering --type %s; `coachctl icu profile` lists the athlete's sports", sport)
			} else if err != nil {
				return err
			}
			value := func(key string) float64 {
				if v, ok := want[key]; ok {
					return v
				}
				v, _ := icu.Num(old, key)
				return v
			}
			if l, m := value("lthr"), value("max_hr"); l > 0 && m > 0 && l >= m {
				return usagef("LTHR %.0f would not be below max HR %.0f; check the values", l, m)
			}
			per100m := strings.Contains(icu.Str(old, "pace_units"), "100")
			label := strings.Join(stringList(old, "types"), "/")
			if label == "" {
				label = sport
			}
			var changes []settingChange
			var shown []string
			for _, f := range settingFields {
				if v, ok := want[f.key]; ok {
					o := optNum(old, f.key)
					changes = append(changes, settingChange{Field: f.key, Old: o, New: v})
					shown = append(shown, fmt.Sprintf("%s %s → %s", f.label, f.show(o, per100m), f.show(&v, per100m)))
				}
			}
			path := fmt.Sprintf("/athlete/{athlete}/sport-settings/%s?recalcHrZones=%t", url.PathEscape(sport), recalcHR)
			hrNote := func(zones []float64) string {
				switch {
				case recalcHR && dryRun:
					return "HR zones will be recomputed from LTHR and max HR."
				case recalcHR:
					return "HR zones recomputed, bpm: " + fmtNums(zones)
				case len(zones) > 0 && (want["lthr"] > 0 || want["max_hr"] > 0):
					return "HR zones keep their bpm values (" + fmtNums(zones) + "); --recalc-hr-zones recomputes them from LTHR and max HR."
				}
				return ""
			}
			if dryRun {
				if a.JSON {
					return a.printJSON(map[string]any{"dry_run": true, "method": "PUT", "path": path, "body": body, "sport": label, "changes": nonNil(changes)})
				}
				a.printf("Dry run, nothing sent: PUT %s\n", path)
				if len(shown) > 0 {
					a.printf("%s: %s\n", label, strings.Join(shown, ", "))
				}
				if n := hrNote(numList(old, "hr_zones")); n != "" {
					a.printf("%s\n", n)
				}
				return nil
			}
			out, err := c.UpdateSportSettings(ctx, sport, body, recalcHR)
			if err != nil {
				return err
			}
			if len(out) == 0 {
				if out, err = c.SportSettings(ctx, sport); err != nil {
					return err
				}
			}
			if bad := settingsDrift(old, out, want, recalcHR); len(bad) > 0 {
				return fmt.Errorf("the write to %s went through, but %s; compare `coachctl icu profile` with the values above and set them back", label, strings.Join(bad, "; "))
			}
			if a.JSON {
				return a.printJSON(map[string]any{"updated": label, "changes": nonNil(changes), "hr_zones_bpm": numList(out, "hr_zones")})
			}
			if len(shown) > 0 {
				a.printf("Updated %s: %s\n", label, strings.Join(shown, ", "))
			} else {
				a.printf("Updated %s.\n", label)
			}
			if n := hrNote(numList(out, "hr_zones")); n != "" {
				a.printf("%s\n", n)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sport, "type", "", "the sport whose settings change: an activity type such as Run or Ride (required)")
	cmd.Flags().IntVar(&ftp, "ftp", 0, "FTP in watts")
	cmd.Flags().StringVar(&pace, "threshold-pace", "", "threshold pace as m:ss per km (4:10 or 4:10/km), or m:ss/100m; sent as m/s")
	cmd.Flags().IntVar(&lthr, "lthr", 0, "lactate threshold heart rate, bpm")
	cmd.Flags().IntVar(&maxHR, "max-hr", 0, "maximum heart rate, bpm")
	cmd.Flags().IntVar(&wPrime, "w-prime", 0, "W' (anaerobic work capacity), joules")
	cmd.Flags().BoolVar(&recalcHR, "recalc-hr-zones", false, "have Intervals.icu recompute the HR zones from LTHR and max HR")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show old → new and the request, send nothing")
	return cmd
}

// settingsDrift lists what a settings write did that it was not asked to:
// a guarded field that moved, or a requested value that did not arrive.
func settingsDrift(before, after icu.Object, want map[string]float64, recalcHR bool) []string {
	var bad []string
	for _, key := range guardedSettings {
		if v, ok := want[key]; ok {
			got, _ := icu.Num(after, key)
			if math.Abs(got-v) > 0.001 {
				bad = append(bad, fmt.Sprintf("%s reads %v instead of %v", key, after[key], v))
			}
			continue
		}
		if key == "hr_zones" && recalcHR {
			continue
		}
		if _, present := after[key]; !present {
			continue
		}
		was, _ := json.Marshal(before[key])
		now, _ := json.Marshal(after[key])
		if string(was) != string(now) {
			bad = append(bad, fmt.Sprintf("%s also changed from %s to %s", key, was, now))
		}
	}
	return bad
}

func stringList(o icu.Object, key string) []string {
	arr, _ := o[key].([]any)
	var out []string
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) icuGetCmd() *cobra.Command {
	var query []string
	cmd := &cobra.Command{
		Use:   "get <path>",
		Short: "Read-only escape hatch: GET any API path, print JSON (secrets redacted)",
		Long: `GET an Intervals.icu API path that has no command yet, relative to
https://intervals.icu/api/v1. {athlete} expands to INTERVALS_ATHLETE_ID.
Read-only by construction. Fields named like keys or tokens are redacted.`,
		Example: `  coachctl icu get /athlete/{athlete}/gear.json
  coachctl icu get /athlete/{athlete}/power-curves.json --query type=Ride --query curves=42d
  coachctl icu get /activity/i98765432/weather-summary`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			for _, kv := range query {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || k == "" {
					return usagef("--query %q must be key=value", kv)
				}
				q.Add(k, v)
			}
			c, err := a.icuClient()
			if err != nil {
				return err
			}
			raw, err := c.GetRaw(context.Background(), args[0], q)
			if err != nil {
				return err
			}
			clean, err := icu.Redact(raw)
			if err != nil {
				return err
			}
			var v any
			dec := json.NewDecoder(strings.NewReader(string(clean)))
			dec.UseNumber()
			if err := dec.Decode(&v); err != nil {
				return err
			}
			return a.printJSON(v)
		},
	}
	cmd.Flags().StringArrayVar(&query, "query", nil, "query parameter key=value (repeatable)")
	return cmd
}
