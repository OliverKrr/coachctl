package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/OliverKrr/coachctl/internal/config"
	"github.com/OliverKrr/coachctl/internal/hevy"
	"github.com/OliverKrr/coachctl/internal/store"
	"github.com/spf13/cobra"
)

func (a *App) hevyClient() (*hevy.Client, error) {
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	if !cfg.Has(config.KeyHevyKey) {
		return nil, usagef("Hevy is not configured: add %s to %s (needs Hevy Pro; `coachctl doctor` shows what is set)", config.KeyHevyKey, cfg.Path)
	}
	c := hevy.New(cfg.Get(config.KeyHevyBase), cfg.Get(config.KeyHevyKey))
	c.UserAgent = "coachctl/" + a.Version
	return c, nil
}

func (a *App) hevyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hevy",
		Short: "Hevy: workouts, exercise history, templates, routines",
		Long: `Read the athlete's Hevy log and create routines with HEVY_API_KEY (the Hevy
API needs Hevy Pro). The routine format is in the coachctl skill,
references/hevy.md.`,
	}
	routine := &cobra.Command{Use: "routine", Short: "Show, create and update routines"}
	routine.AddCommand(a.hevyRoutineShowCmd(), a.hevyRoutineCreateCmd(), a.hevyRoutineUpdateCmd())
	templates := &cobra.Command{Use: "templates", Short: "Search the exercise-template catalog"}
	templates.AddCommand(a.hevyTemplatesSearchCmd())
	cmd.AddCommand(a.hevyWorkoutsCmd(), a.hevyWorkoutCmd(), a.hevyHistoryCmd(), templates, a.hevyRoutinesCmd(), a.hevyFoldersCmd(), routine)
	return cmd
}

func setLine(s map[string]any) string {
	var parts []string
	if w, ok := s["weight_kg"].(float64); ok && w > 0 {
		parts = append(parts, fmtValue(w, "kg"))
	}
	if r, ok := s["reps"].(float64); ok && r > 0 {
		if len(parts) > 0 {
			parts[len(parts)-1] += fmt.Sprintf(" × %.0f", r)
		} else {
			parts = append(parts, fmt.Sprintf("%.0f reps", r))
		}
	}
	if d, ok := s["distance_meters"].(float64); ok && d > 0 {
		parts = append(parts, fmt.Sprintf("%.0f m", d))
	}
	if d, ok := s["duration_seconds"].(float64); ok && d > 0 {
		parts = append(parts, fmt.Sprintf("%.0f s", d))
	}
	if rpe, ok := s["rpe"].(float64); ok && rpe > 0 {
		parts = append(parts, fmt.Sprintf("RPE %s", fmtValue(rpe, "")))
	}
	line := strings.Join(parts, " ")
	if t, _ := s["type"].(string); t != "" && t != "normal" {
		line += " (" + t + ")"
	}
	return line
}

func workoutText(w hevy.Object) string {
	var b strings.Builder
	start := hevy.Str(w, "start_time")
	if len(start) >= 16 {
		start = strings.Replace(start[:16], "T", " ", 1)
	}
	fmt.Fprintf(&b, "%s  %s  id %s\n", start, strings.TrimSpace(hevy.Str(w, "title")), hevy.Str(w, "id"))
	exercises, _ := w["exercises"].([]any)
	for _, raw := range exercises {
		e, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		sets, _ := e["sets"].([]any)
		lines := make([]string, 0, len(sets))
		for _, rs := range sets {
			if s, ok := rs.(map[string]any); ok {
				lines = append(lines, setLine(s))
			}
		}
		fmt.Fprintf(&b, "  %s: %s\n", hevy.Str(e, "title"), strings.Join(lines, ", "))
	}
	return b.String()
}

func (a *App) hevyWorkoutsCmd() *cobra.Command {
	var since string
	var page, size int
	cmd := &cobra.Command{
		Use:   "workouts",
		Short: "Logged workouts, newest first",
		Example: `  coachctl hevy workouts
  coachctl hevy workouts --since 2026-10-01 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.hevyClient()
			if err != nil {
				return err
			}
			ctx := context.Background()
			var rows []hevy.Object
			if since != "" {
				if _, err := store.ParseDate(since); err != nil {
					return err
				}
				if rows, err = c.WorkoutsSince(ctx, since, 20); err != nil {
					return err
				}
			} else {
				if size < 1 || size > 10 {
					return usagef("--page-size must be 1–10 (a Hevy API limit)")
				}
				p, err := c.Workouts(ctx, page, size)
				if err != nil {
					return err
				}
				rows = p.Items
			}
			if a.JSON {
				return a.printJSON(nonNil(rows))
			}
			if len(rows) == 0 {
				a.printf("No workouts.\n")
				return nil
			}
			for _, w := range rows {
				a.printf("%s", workoutText(w))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "all workouts on or after this date (YYYY-MM-DD)")
	cmd.Flags().IntVar(&page, "page", 1, "page number")
	cmd.Flags().IntVar(&size, "page-size", 5, "workouts per page (max 10)")
	return cmd
}

func (a *App) hevyWorkoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workout <id>",
		Short: "One workout with every set (or `workout create` to log one)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.hevyClient()
			if err != nil {
				return err
			}
			w, err := c.Workout(context.Background(), args[0])
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(w)
			}
			a.printf("%s", workoutText(w))
			return nil
		},
	}
	cmd.AddCommand(a.hevyWorkoutCreateCmd())
	return cmd
}

var templateIDRe = regexp.MustCompile(`^[0-9A-Fa-f]{8}$|^[0-9a-f-]{36}$`)

// resolveTemplate accepts a template id or an exact (case-insensitive)
// title, and lists candidates when a title is ambiguous or unknown.
func resolveTemplate(ctx context.Context, c *hevy.Client, arg string) (string, string, error) {
	if templateIDRe.MatchString(arg) {
		return arg, arg, nil
	}
	all, err := c.Templates(ctx, false)
	if err != nil {
		return "", "", err
	}
	var exact, partial []hevy.Object
	q := strings.ToLower(arg)
	for _, t := range all {
		title := strings.ToLower(hevy.Str(t, "title"))
		switch {
		case title == q:
			exact = append(exact, t)
		case strings.Contains(title, q):
			partial = append(partial, t)
		}
	}
	if len(exact) == 1 {
		return hevy.Str(exact[0], "id"), hevy.Str(exact[0], "title"), nil
	}
	cands := append(exact, partial...)
	names := make([]string, 0, min(len(cands), 10))
	for _, t := range cands[:min(len(cands), 10)] {
		names = append(names, fmt.Sprintf("%s (%s)", hevy.Str(t, "title"), hevy.Str(t, "id")))
	}
	if len(names) == 0 {
		return "", "", usagef("no exercise template matches %q; try `coachctl hevy templates search <word>`", arg)
	}
	return "", "", usagef("%q is not one exact exercise title; pass a template id: %s", arg, strings.Join(names, "; "))
}

func (a *App) hevyHistoryCmd() *cobra.Command {
	var since, until string
	cmd := &cobra.Command{
		Use:   "history <template-id|exact title>",
		Short: "Every logged set of one exercise (progression)",
		Example: `  coachctl hevy history "Bulgarian Split Squat (Dumbbell)" --since 2026-07-01
  coachctl hevy history 07B38369 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, d := range []string{since, until} {
				if d != "" {
					if _, err := store.ParseDate(d); err != nil {
						return err
					}
				}
			}
			c, err := a.hevyClient()
			if err != nil {
				return err
			}
			ctx := context.Background()
			id, title, err := resolveTemplate(ctx, c, args[0])
			if err != nil {
				return err
			}
			rows, err := c.ExerciseHistory(ctx, id, since, until)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(nonNil(rows))
			}
			if len(rows) == 0 {
				a.printf("No logged sets of %s.\n", title)
				return nil
			}
			// Group the flat set list by workout, in the API's order.
			type session struct {
				date, title string
				sets        []string
			}
			var sessions []*session
			byWorkout := map[string]*session{}
			for _, s := range rows {
				wid := hevy.Str(s, "workout_id")
				sess, ok := byWorkout[wid]
				if !ok {
					start := hevy.Str(s, "workout_start_time")
					if len(start) >= 10 {
						start = start[:10]
					}
					sess = &session{date: start, title: hevy.Str(s, "workout_title")}
					byWorkout[wid] = sess
					sessions = append(sessions, sess)
				}
				sess.sets = append(sess.sets, setLine(s))
			}
			a.printf("%s\n", title)
			for _, sess := range sessions {
				a.printf("%s  %s: %s\n", sess.date, sess.title, strings.Join(sess.sets, ", "))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "first date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&until, "until", "", "last date (YYYY-MM-DD)")
	return cmd
}

func (a *App) hevyTemplatesSearchCmd() *cobra.Command {
	var muscle string
	var refresh bool
	cmd := &cobra.Command{
		Use:   "search <words>",
		Short: "Find exercise templates by title (built-in and custom)",
		Long: `Search template titles (case-insensitive substring). The catalog is cached
for a day; --refresh re-reads it after creating a custom exercise in the app.`,
		Example: `  coachctl hevy templates search split squat
  coachctl hevy templates search row --muscle upper_back --json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.hevyClient()
			if err != nil {
				return err
			}
			all, err := c.Templates(context.Background(), refresh)
			if err != nil {
				return err
			}
			q := strings.ToLower(strings.Join(args, " "))
			var out []hevy.Object
			for _, t := range all {
				if strings.Contains(strings.ToLower(hevy.Str(t, "title")), q) && (muscle == "" || hevy.Str(t, "primary_muscle_group") == muscle) {
					out = append(out, t)
				}
			}
			if a.JSON {
				return a.printJSON(nonNil(out))
			}
			if len(out) == 0 {
				a.printf("No template title contains %q.\n", q)
				return nil
			}
			for _, t := range out {
				custom := ""
				if v, _ := t["is_custom"].(bool); v {
					custom = " (custom)"
				}
				a.printf("%s  %s%s  [%s, %s, %s]\n", hevy.Str(t, "id"), hevy.Str(t, "title"), custom, hevy.Str(t, "type"), hevy.Str(t, "primary_muscle_group"), hevy.Str(t, "equipment"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&muscle, "muscle", "", "only this primary muscle group, e.g. quadriceps, upper_back")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "re-read the catalog instead of the day-old cache")
	return cmd
}

func (a *App) hevyRoutinesCmd() *cobra.Command {
	var page, size int
	cmd := &cobra.Command{
		Use:   "routines",
		Short: "Saved routines",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.hevyClient()
			if err != nil {
				return err
			}
			p, err := c.Routines(context.Background(), page, size)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(map[string]any{"page": p.Page, "page_count": p.PageCount, "routines": nonNil(p.Items)})
			}
			for _, r := range p.Items {
				ex, _ := r["exercises"].([]any)
				a.printf("%s  %s  folder %s  (%d exercises, updated %s)\n", hevy.Str(r, "id"), hevy.Str(r, "title"), hevy.Str(r, "folder_id"), len(ex), hevy.Str(r, "updated_at"))
			}
			a.printf("page %d of %d\n", p.Page, p.PageCount)
			return nil
		},
	}
	cmd.Flags().IntVar(&page, "page", 1, "page number")
	cmd.Flags().IntVar(&size, "page-size", 10, "routines per page (max 10)")
	return cmd
}

func (a *App) hevyFoldersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "folders",
		Short: "Routine folders (ids for routine create's folder_id)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.hevyClient()
			if err != nil {
				return err
			}
			p, err := c.Folders(context.Background(), 1, 10)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(nonNil(p.Items))
			}
			for _, f := range p.Items {
				a.printf("%s  %s\n", hevy.Str(f, "id"), hevy.Str(f, "title"))
			}
			return nil
		},
	}
}

var setTypes = map[string]bool{"normal": true, "warmup": true, "failure": true, "dropset": true}

// validateRoutine checks the parts of the wire format the API rejects with an
// unhelpful message.
func validateRoutine(r hevy.Object) error {
	if strings.TrimSpace(hevy.Str(r, "title")) == "" {
		return usagef("routine.title is required")
	}
	exercises, ok := r["exercises"].([]any)
	if !ok || len(exercises) == 0 {
		return usagef("routine.exercises needs at least one exercise")
	}
	for i, raw := range exercises {
		e, ok := raw.(map[string]any)
		if !ok {
			return usagef("exercises[%d] is not an object", i)
		}
		if hevy.Str(e, "exercise_template_id") == "" {
			return usagef("exercises[%d].exercise_template_id is required (find it with `coachctl hevy templates search`)", i)
		}
		sets, ok := e["sets"].([]any)
		if !ok || len(sets) == 0 {
			return usagef("exercises[%d].sets needs at least one set", i)
		}
		for j, rs := range sets {
			s, ok := rs.(map[string]any)
			if !ok {
				return usagef("exercises[%d].sets[%d] is not an object", i, j)
			}
			if t, ok := s["type"].(string); ok && !setTypes[t] {
				return usagef("exercises[%d].sets[%d].type %q must be normal, warmup, failure or dropset", i, j, t)
			}
			if _, ok := s["rpe"]; ok {
				return usagef("exercises[%d].sets[%d]: routine sets take rep_range, not rpe", i, j)
			}
		}
	}
	return nil
}

func (a *App) hevyRoutineCreateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a routine from JSON on stdin (Hevy wire format)",
		Long: `Create a routine. Stdin is one routine object in the Hevy API format:
title, optional folder_id and notes, and exercises with exercise_template_id,
rest_seconds, notes and sets (type, weight_kg, reps or rep_range, ...).`,
		Example: `  coachctl hevy routine create --dry-run <<'EOF'
{"title": "Kraft A (KW43)", "folder_id": null, "notes": "",
 "exercises": [{"exercise_template_id": "07B38369", "rest_seconds": 120,
   "sets": [{"type": "normal", "weight_kg": 24, "rep_range": {"start": 8, "end": 10}}]}]}
EOF`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := a.readStdin("the routine JSON")
			if err != nil {
				return err
			}
			var routine hevy.Object
			if err := json.Unmarshal([]byte(raw), &routine); err != nil {
				return usagef("stdin is not one JSON object: %v", err)
			}
			if inner, ok := routine["routine"].(map[string]any); ok {
				routine = inner
			}
			if err := validateRoutine(routine); err != nil {
				return err
			}
			if dryRun {
				return a.dryRun("POST", "/routines", hevy.Object{"routine": routine})
			}
			c, err := a.hevyClient()
			if err != nil {
				return err
			}
			out, err := c.CreateRoutine(context.Background(), routine)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(out)
			}
			a.printf("Created routine %s: %s\n", hevy.Str(out, "id"), hevy.Str(out, "title"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate and print the request, send nothing")
	return cmd
}

// readJSONObject reads one JSON object from stdin, unwrapping {"<key>": {...}}.
func (a *App) readJSONObject(what, key string) (hevy.Object, error) {
	raw, err := a.readStdin(what)
	if err != nil {
		return nil, err
	}
	var obj hevy.Object
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, usagef("stdin is not one JSON object: %v", err)
	}
	if inner, ok := obj[key].(map[string]any); ok {
		obj = inner
	}
	return obj, nil
}

func (a *App) hevyRoutineShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "One routine with its exercises and sets (the starting point for an update)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.hevyClient()
			if err != nil {
				return err
			}
			r, err := c.Routine(context.Background(), args[0])
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(r)
			}
			a.printf("%s  %s  folder %s\n", hevy.Str(r, "id"), hevy.Str(r, "title"), hevy.Str(r, "folder_id"))
			exercises, _ := r["exercises"].([]any)
			for _, raw := range exercises {
				e, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				sets, _ := e["sets"].([]any)
				lines := make([]string, 0, len(sets))
				for _, rs := range sets {
					if s, ok := rs.(map[string]any); ok {
						line := setLine(s)
						if rr, ok := s["rep_range"].(map[string]any); ok {
							line += fmt.Sprintf(" (%s-%s reps)", hevy.Str(rr, "start"), hevy.Str(rr, "end"))
						}
						lines = append(lines, line)
					}
				}
				a.printf("  %s [%s]: %s\n", hevy.Str(e, "title"), hevy.Str(e, "exercise_template_id"), strings.Join(lines, ", "))
			}
			return nil
		},
	}
}

func (a *App) hevyRoutineUpdateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Replace a routine's title, notes and exercises (JSON on stdin)",
		Long: `Update a routine. The exercise list is REPLACED, not merged: start from
` + "`coachctl hevy routine show <id> --json`" + `, change it, and send the whole routine. Include
folder_id only to move the routine (null means the default folder). A null rep_range is
dropped, because the API rejects it on update.`,
		Example: `  coachctl hevy routine show 3f1c… --json > data/routine.json   # edit, then:
  coachctl hevy routine update 3f1c… --dry-run < data/routine.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			routine, err := a.readJSONObject("the routine JSON", "routine")
			if err != nil {
				return err
			}
			// Fields the API returns but does not accept back.
			for _, k := range []string{"id", "created_at", "updated_at", "index"} {
				delete(routine, k)
			}
			if exercises, ok := routine["exercises"].([]any); ok {
				for _, raw := range exercises {
					e, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					delete(e, "index")
					delete(e, "title")
					sets, _ := e["sets"].([]any)
					for _, rs := range sets {
						if s, ok := rs.(map[string]any); ok {
							delete(s, "index")
							if v, has := s["rep_range"]; has && v == nil {
								delete(s, "rep_range")
							}
						}
					}
				}
			}
			if err := validateRoutine(routine); err != nil {
				return err
			}
			if dryRun {
				return a.dryRun("PUT", "/routines/"+args[0], hevy.Object{"routine": routine})
			}
			c, err := a.hevyClient()
			if err != nil {
				return err
			}
			out, err := c.UpdateRoutine(context.Background(), args[0], routine)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(out)
			}
			a.printf("Updated routine %s: %s\n", hevy.Str(out, "id"), hevy.Str(out, "title"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate and print the request, send nothing")
	return cmd
}

func (a *App) hevyWorkoutCreateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Log a completed workout (JSON on stdin)",
		Long: `Log a workout that already happened: title, start_time and end_time (ISO 8601),
optional description, and exercises with exercise_template_id and sets (type, weight_kg,
reps, rpe, distance_meters, duration_seconds). It appears in the athlete's log and may sync
on to other platforms, so log only what was actually done.`,
		Example: `  coachctl hevy workout create --dry-run <<'EOF'
{"title": "Kraft A", "start_time": "2026-10-17T17:00:00Z", "end_time": "2026-10-17T17:50:00Z",
 "exercises": [{"exercise_template_id": "07B38369",
   "sets": [{"type": "normal", "weight_kg": 20, "reps": 10, "rpe": 8}]}]}
EOF`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := a.readJSONObject("the workout JSON", "workout")
			if err != nil {
				return err
			}
			if strings.TrimSpace(hevy.Str(w, "title")) == "" || hevy.Str(w, "start_time") == "" || hevy.Str(w, "end_time") == "" {
				return usagef("workout.title, start_time and end_time are required")
			}
			exercises, ok := w["exercises"].([]any)
			if !ok || len(exercises) == 0 {
				return usagef("workout.exercises needs at least one exercise")
			}
			for i, raw := range exercises {
				e, ok := raw.(map[string]any)
				if !ok || hevy.Str(e, "exercise_template_id") == "" {
					return usagef("exercises[%d].exercise_template_id is required", i)
				}
				if sets, ok := e["sets"].([]any); !ok || len(sets) == 0 {
					return usagef("exercises[%d].sets needs at least one set", i)
				}
			}
			if dryRun {
				return a.dryRun("POST", "/workouts", hevy.Object{"workout": w})
			}
			c, err := a.hevyClient()
			if err != nil {
				return err
			}
			out, err := c.CreateWorkout(context.Background(), w)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(out)
			}
			a.printf("Logged workout %s: %s\n", hevy.Str(out, "id"), hevy.Str(out, "title"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate and print the request, send nothing")
	return cmd
}
