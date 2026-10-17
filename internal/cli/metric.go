package cli

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/OliverKrr/coachctl/internal/store"
	"github.com/spf13/cobra"
)

func fmtValue(v float64, unit string) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if unit != "" {
		s += " " + unit
	}
	return s
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func (a *App) metricCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metric",
		Short: "Record and read repeated measurements",
		Long: `Metrics are repeated numbers (body weight, resting HR, FTP, adherence %) kept
as data instead of markdown tables. Each series is defined in
metrics/series.toml with a kind:

  state  a new value supersedes the previous one (FTP, thresholds, baselines);
         reads return what is valid now; stale_after_days flags old values
  event  values accumulate (weekly volume, adherence); nothing supersedes

Points live in metrics/<series>.jsonl, written only through coachctl.`,
	}
	cmd.AddCommand(a.metricRecordCmd(), a.metricGetCmd(), a.metricListCmd(), a.metricStaleCmd(), a.metricDefineCmd(), a.metricDeleteCmd())
	return cmd
}

func (a *App) metricRecordCmd() *cobra.Command {
	var in store.NewPoint
	cmd := &cobra.Command{
		Use:   "record <series> <value>",
		Short: "Record one measurement",
		Example: `  coachctl metric record body-weight 71.4
  coachctl metric record ftp 268 --at 2026-10-12 --note "ramp test"
  coachctl metric record plan-adherence 80 --at 2026-10-12`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := strconv.ParseFloat(strings.ReplaceAll(args[1], ",", "."), 64)
			if err != nil {
				return usagef("value %q is not a number", args[1])
			}
			in.Series, in.Value = args[0], v
			s, err := a.store()
			if err != nil {
				return err
			}
			r, err := s.RecordMetric(in)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(r)
			}
			msg := fmt.Sprintf("Metric #%d recorded: %s = %s at %s.", r.Point.ID, r.Series.Name, fmtValue(r.Point.Value, r.Point.Unit), r.Point.MeasuredAt)
			switch {
			case r.Historical != nil:
				msg += fmt.Sprintf(" Recorded as history: #%d (%s, %s) is newer and stays current.", r.Historical.ID, fmtValue(r.Historical.Value, r.Historical.Unit), r.Historical.MeasuredAt)
			case r.Supersedes != nil:
				msg += fmt.Sprintf(" Supersedes #%d (%s, since %s).", r.Supersedes.ID, fmtValue(r.Supersedes.Value, r.Supersedes.Unit), r.Supersedes.MeasuredAt)
			}
			a.printf("%s\n", msg)
			return nil
		},
	}
	cmd.Flags().StringVar(&in.Unit, "unit", "", "unit; must match the series' unit when it has one")
	cmd.Flags().StringVar(&in.Note, "note", "", "context for this point")
	cmd.Flags().StringVar(&in.MeasuredAt, "at", "", "when it was measured: YYYY-MM-DD or \"YYYY-MM-DD HH:MM\" (default now)")
	return cmd
}

func (a *App) metricGetCmd() *cobra.Command {
	var since, until, asOf string
	var all bool
	var limit int
	cmd := &cobra.Command{
		Use:   "get <series>",
		Short: "Read a series",
		Long: `State series: the current value (--as-of for a past moment, --all for the
history with validity windows). Event series: the points, newest first, with
min/avg/max.`,
		Example: `  coachctl metric get ftp
  coachctl metric get ftp --as-of 2026-06-30
  coachctl metric get body-weight --since 2026-09-01 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, d := range []string{since, until} {
				if d != "" {
					if _, err := store.ParseDate(d); err != nil {
						return err
					}
				}
			}
			s, err := a.store()
			if err != nil {
				return err
			}
			defs, err := s.SeriesDefs()
			if err != nil {
				return err
			}
			name := args[0]
			def, ok := defs[name]
			if !ok {
				def = store.Series{Name: name, Kind: store.KindEvent}
			}
			points, err := s.Points(name)
			if err != nil {
				return err
			}
			if len(points) == 0 {
				known := make([]string, 0, len(defs))
				for n := range defs {
					known = append(known, n)
				}
				return usagef("no points for series %q (defined series: %s)", name, strings.Join(sortedStrings(known), ", "))
			}
			if def.Kind == store.KindState && asOf != "" {
				p := store.ValueAsOf(points, asOf)
				if a.JSON {
					return a.printJSON(map[string]any{"series": def, "as_of": asOf, "point": p})
				}
				if p == nil {
					a.printf("No value of %s was valid at %s; the series starts %s.\n", name, asOf, points[0].MeasuredAt)
					return nil
				}
				a.printf("%s as of %s: %s (#%d, measured %s)%s\n", name, asOf, fmtValue(p.Value, p.Unit), p.ID, p.MeasuredAt, noteSuffix(p.Note))
				return nil
			}
			if def.Kind == store.KindState && !all {
				cur := points[len(points)-1]
				if a.JSON {
					return a.printJSON(map[string]any{"series": def, "current": cur, "superseded": len(points) - 1})
				}
				a.printf("%s (state): %s, current since %s (#%d)%s\n", name, fmtValue(cur.Value, cur.Unit), cur.MeasuredAt, cur.ID, noteSuffix(cur.Note))
				if n := len(points) - 1; n > 0 {
					a.printf("%d superseded value%s: --all for the history, --as-of for a point in time.\n", n, plural(n))
				}
				return nil
			}
			if def.Kind == store.KindState {
				windows := store.StateHistory(points)
				if a.JSON {
					return a.printJSON(map[string]any{"series": def, "history": windows})
				}
				a.printf("%s (state, full history), %d values\n", name, len(windows))
				for i := len(windows) - 1; i >= 0; i-- {
					w := windows[i]
					to := w.ValidTo
					if to == "" {
						to = "current"
					}
					a.printf("#%d %s → %s · %s%s\n", w.ID, w.MeasuredAt, to, fmtValue(w.Value, w.Unit), noteSuffix(w.Note))
				}
				return nil
			}
			var sel []store.Point
			for _, p := range points {
				day := p.MeasuredAt[:10]
				if (since == "" || day >= since) && (until == "" || day <= until) {
					sel = append(sel, p)
				}
			}
			if len(sel) == 0 {
				return usagef("no points for %q in that range", name)
			}
			minV, maxV, sum := sel[0].Value, sel[0].Value, 0.0
			for _, p := range sel {
				minV, maxV, sum = math.Min(minV, p.Value), math.Max(maxV, p.Value), sum+p.Value
			}
			avg := sum / float64(len(sel))
			shown := sel
			if limit > 0 && len(shown) > limit {
				shown = shown[len(shown)-limit:]
			}
			if a.JSON {
				return a.printJSON(map[string]any{"series": def, "count": len(sel), "min": minV, "avg": round2(avg), "max": maxV, "points": reversed(shown)})
			}
			unit := sel[len(sel)-1].Unit
			a.printf("%s, %d point%s", name, len(sel), plural(len(sel)))
			if len(sel) > 1 {
				a.printf(", min %s / avg %s / max %s", fmtValue(round2(minV), ""), fmtValue(round2(avg), ""), fmtValue(round2(maxV), unit))
			}
			if len(shown) < len(sel) {
				a.printf(" (newest %d shown)", len(shown))
			}
			a.printf("\n")
			for i := len(shown) - 1; i >= 0; i-- {
				p := shown[i]
				a.printf("#%d %s · %s%s\n", p.ID, p.MeasuredAt, fmtValue(p.Value, p.Unit), noteSuffix(p.Note))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "event series: points on or after this date")
	cmd.Flags().StringVar(&until, "until", "", "event series: points on or before this date")
	cmd.Flags().StringVar(&asOf, "as-of", "", "state series: the value valid at this date or \"date HH:MM\"")
	cmd.Flags().BoolVar(&all, "all", false, "state series: full history with validity windows")
	cmd.Flags().IntVar(&limit, "limit", 50, "event series: max points shown (stats cover all)")
	return cmd
}

func sortedStrings(s []string) []string {
	sort.Strings(s)
	return s
}

func reversed[T any](s []T) []T {
	out := make([]T, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return " — " + note
}

func (a *App) metricListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Overview of every series: kind, count, span, latest value",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.store()
			if err != nil {
				return err
			}
			rows, err := s.Summaries()
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(nonNil(rows))
			}
			if len(rows) == 0 {
				a.printf("No series defined. Define one with `coachctl metric define <name> --kind state|event`.\n")
				return nil
			}
			for _, r := range rows {
				a.printf("%s\n", summaryLine(r))
			}
			return nil
		},
	}
}

func summaryLine(r store.SeriesSummary) string {
	line := fmt.Sprintf("%s [%s]", r.Name, r.Kind)
	if !r.Defined {
		line += " (not in series.toml)"
	}
	if r.Count == 0 {
		return line + ": no points yet"
	}
	label := "latest"
	if r.Kind == store.KindState {
		label = "current"
	}
	line += fmt.Sprintf(": %d point%s, %s → %s, %s %s", r.Count, plural(r.Count), r.First[:10], r.Last[:10], label, fmtValue(r.Latest.Value, r.Latest.Unit))
	if r.Stale {
		line += fmt.Sprintf(" ⚠ stale (measured %s, stale after %d d)", r.Latest.MeasuredAt[:10], r.StaleAfterDays)
	}
	return line
}

func (a *App) metricStaleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stale",
		Short: "State series whose current value is past stale_after_days",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.store()
			if err != nil {
				return err
			}
			rows, err := s.Stale()
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(nonNil(rows))
			}
			if len(rows) == 0 {
				a.printf("No stale state metrics.\n")
				return nil
			}
			for _, r := range rows {
				a.printf("%s\n", summaryLine(r))
			}
			return nil
		},
	}
}

func (a *App) metricDefineCmd() *cobra.Command {
	var def store.Series
	cmd := &cobra.Command{
		Use:   "define <series>",
		Short: "Add a series to metrics/series.toml",
		Long: `Add a series definition. Names are lowercase and stable (trends only work
when the name repeats). To change an existing definition, edit
metrics/series.toml; ` + "`coachctl doctor`" + ` validates it.`,
		Example: `  coachctl metric define ftp --kind state --unit W --stale-after 120 --description "Cycling FTP"
  coachctl metric define weekly-run-km --kind event --unit km`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			def.Name = args[0]
			s, err := a.store()
			if err != nil {
				return err
			}
			if err := s.DefineSeries(def); err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(def)
			}
			a.printf("Series %s defined (%s) in %s.\n", def.Name, def.Kind, store.SeriesFile)
			return nil
		},
	}
	cmd.Flags().StringVar(&def.Kind, "kind", "", "state or event (required)")
	cmd.Flags().StringVar(&def.Unit, "unit", "", "unit every point uses, e.g. kg, bpm, W, s/km")
	cmd.Flags().IntVar(&def.StaleAfterDays, "stale-after", 0, "state series: flag the current value after this many days")
	cmd.Flags().StringVar(&def.Description, "description", "", "what the series measures")
	_ = cmd.MarkFlagRequired("kind")
	return cmd
}

func (a *App) metricDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete one point (a typo, a wrong unit); git keeps the old file",
		Example: "  coachctl metric delete 412",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "metric point")
			if err != nil {
				return err
			}
			s, err := a.store()
			if err != nil {
				return err
			}
			series, p, err := s.DeletePoint(id)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(map[string]any{"series": series, "deleted": p})
			}
			a.printf("Metric #%d deleted from %s (%s at %s).\n", p.ID, series, fmtValue(p.Value, p.Unit), p.MeasuredAt)
			return nil
		},
	}
}
