package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/OliverKrr/coachctl/internal/config"
	"github.com/OliverKrr/coachctl/internal/store"
	"github.com/spf13/cobra"
)

// Check statuses.
const (
	checkOK   = "ok"
	checkWarn = "warn"
	checkFail = "FAIL"
	checkSkip = "off"
)

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func (a *App) doctorCmd() *cobra.Command {
	var online bool
	var skillDir string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check config, record files and integrations (never prints secrets)",
		Long: `Check the env file (exists, mode 0600, known keys), COACH_HOME and every
record file, which integrations are configured, and whether the installed
skill matches this binary. --online also calls each configured service once.
Exit code 1 when any check fails.`,
		Example: "  coachctl doctor\n  coachctl doctor --online",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := a.runDoctor(online, skillDir)
			failed := 0
			for _, c := range checks {
				if c.Status == checkFail {
					failed++
				}
			}
			if a.JSON {
				if err := a.printJSON(map[string]any{"ok": failed == 0, "checks": checks}); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					a.printf("%-4s  %-12s %s\n", c.Status, c.Name, c.Detail)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d check%s failed", failed, plural(failed))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&online, "online", false, "also call each configured service")
	cmd.Flags().StringVar(&skillDir, "skill-dir", "", "where the skill is installed (default ~/.claude/skills/coachctl)")
	return cmd
}

func (a *App) runDoctor(online bool, skillDir string) []check {
	var out []check
	add := func(name, status, format string, args ...any) {
		out = append(out, check{Name: name, Status: status, Detail: fmt.Sprintf(format, args...)})
	}
	add("version", checkOK, "coachctl %s", a.Version)

	cfg, err := config.Load()
	if err != nil {
		add("config", checkFail, "%v", err)
		return out
	}
	a.cfg = cfg
	switch {
	case cfg.FileMode == 0:
		add("config", checkWarn, "%s does not exist; settings come from the environment only", cfg.Path)
	case cfg.FileMode&0o077 != 0:
		add("config", checkFail, "%s has mode %04o; it holds API keys, run: chmod 600 %s", cfg.Path, cfg.FileMode, cfg.Path)
	default:
		add("config", checkOK, "%s (mode %04o)", cfg.Path, cfg.FileMode)
	}
	var unknown []string
	for k := range cfg.File {
		if !slices.Contains(config.Known, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		add("config", checkWarn, "unknown keys in %s: %s", cfg.Path, strings.Join(unknown, ", "))
	}

	loc, err := cfg.Location()
	if err != nil {
		add("timezone", checkFail, "%v", err)
		loc = time.Local
	} else {
		add("timezone", checkOK, "%s, today %s", loc, a.Now().In(loc).Format("Mon 2006-01-02"))
	}

	home, err := cfg.Home()
	if err != nil {
		add("record", checkFail, "%v", err)
	} else if s, err := store.Open(home, loc, a.Now); err != nil {
		add("record", checkFail, "%v", err)
	} else {
		out = append(out, recordChecks(s)...)
		out = append(out, workspaceChecks(filepath.Dir(home))...)
	}

	integrations := []struct {
		name string
		keys []string
	}{
		{"intervals", []string{config.KeyIntervalsKey, config.KeyIntervalsID}},
		{"hevy", []string{config.KeyHevyKey}},
		{"telegram", []string{config.KeyTelegramToken, config.KeyTelegramChat}},
	}
	configured := map[string]bool{}
	for _, in := range integrations {
		var missing []string
		for _, k := range in.keys {
			if !cfg.Has(k) {
				missing = append(missing, k)
			}
		}
		switch {
		case len(missing) == len(in.keys):
			add(in.name, checkSkip, "not configured (%s)", strings.Join(in.keys, ", "))
		case len(missing) > 0:
			add(in.name, checkFail, "partly configured: %s missing", strings.Join(missing, ", "))
		default:
			configured[in.name] = true
			add(in.name, checkOK, "configured (%s set)", strings.Join(in.keys, ", "))
		}
	}
	if online {
		out = append(out, a.onlineChecks(configured)...)
	}
	out = append(out, a.skillCheck(skillDir))
	return out
}

func recordChecks(s *store.Store) []check {
	var out []check
	add := func(status, format string, args ...any) {
		out = append(out, check{Name: "record", Status: status, Detail: fmt.Sprintf(format, args...)})
	}
	add(checkOK, "COACH_HOME %s", s.Root)
	main, sections, refs, err := s.Docs()
	switch {
	case err != nil:
		add(checkFail, "prose files: %v", err)
	case main == nil:
		add(checkWarn, "main.md is missing")
	default:
		add(checkOK, "main.md %s, %d sections, %d references", humanBytes(main.Bytes), len(sections), len(refs))
	}
	if entries, err := s.Journal(); err != nil {
		add(checkFail, "journal: %v", err)
	} else {
		add(checkOK, "journal: %d entries", len(entries))
	}
	if items, err := s.Items(); err != nil {
		add(checkFail, "open items: %v", err)
	} else {
		open := 0
		for _, it := range items {
			if it.Status == store.StatusOpen {
				open++
			}
		}
		add(checkOK, "open items: %d open of %d", open, len(items))
	}
	rows, err := s.Summaries()
	if err != nil {
		add(checkFail, "metrics: %v", err)
		return out
	}
	var undefined []string
	points := 0
	for _, r := range rows {
		points += r.Count
		if !r.Defined {
			undefined = append(undefined, r.Name)
		}
	}
	add(checkOK, "metrics: %d series, %d points", len(rows), points)
	if len(undefined) > 0 {
		add(checkWarn, "series with data but no definition in %s: %s", store.SeriesFile, strings.Join(undefined, ", "))
	}
	return out
}

// workspaceChecks looks for the template's permission guard next to the
// record: without it an agent may edit the CLI-owned files directly.
func workspaceChecks(workspace string) []check {
	data, err := os.ReadFile(filepath.Join(workspace, ".claude", "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return []check{{Name: "workspace", Status: checkWarn, Detail: fmt.Sprintf("%s/.claude/settings.json is missing: nothing stops a session editing the CLI-owned files (see `coachctl init`)", workspace)}}
	}
	if err != nil {
		return []check{{Name: "workspace", Status: checkWarn, Detail: err.Error()}}
	}
	if !strings.Contains(string(data), "coaching/**/*.jsonl") || !strings.Contains(string(data), "coaching/journal/**") {
		return []check{{Name: "workspace", Status: checkWarn, Detail: ".claude/settings.json does not deny Edit/Write on coaching/**/*.jsonl and coaching/journal/**"}}
	}
	return []check{{Name: "workspace", Status: checkOK, Detail: "permission guard on the CLI-owned files is in place"}}
}

func (a *App) onlineChecks(configured map[string]bool) []check {
	var out []check
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if configured["intervals"] {
		c, _ := a.icuClient()
		if ath, err := c.Athlete(ctx); err != nil {
			out = append(out, check{"intervals", checkFail, err.Error()})
		} else {
			out = append(out, check{"intervals", checkOK, fmt.Sprintf("reached athlete %v", ath["id"])})
		}
	}
	if configured["hevy"] {
		c, _ := a.hevyClient()
		if p, err := c.Workouts(ctx, 1, 1); err != nil {
			out = append(out, check{"hevy", checkFail, err.Error()})
		} else {
			out = append(out, check{"hevy", checkOK, fmt.Sprintf("reached the workout log (%d pages)", p.PageCount)})
		}
	}
	if configured["telegram"] {
		t, _ := a.telegram()
		if name, err := t.Me(ctx); err != nil {
			out = append(out, check{"telegram", checkFail, err.Error()})
		} else {
			out = append(out, check{"telegram", checkOK, "bot @" + name + " reachable (no message sent)"})
		}
	}
	return out
}

func (a *App) skillCheck(dir string) check {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return check{"skill", checkWarn, err.Error()}
		}
		dir = filepath.Join(home, ".claude", "skills", "coachctl")
	}
	data, err := os.ReadFile(filepath.Join(dir, SkillVersionFile))
	if errors.Is(err, os.ErrNotExist) {
		return check{"skill", checkWarn, fmt.Sprintf("not installed in %s: run `coachctl skill install`", dir)}
	}
	if err != nil {
		return check{"skill", checkWarn, err.Error()}
	}
	if v := strings.TrimSpace(string(data)); v != a.Version {
		return check{"skill", checkWarn, fmt.Sprintf("installed skill is %s, binary is %s: run `coachctl skill install`", v, a.Version)}
	}
	return check{"skill", checkOK, fmt.Sprintf("%s matches the binary", dir)}
}
