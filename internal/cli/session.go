package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/OliverKrr/coachctl/internal/store"
	"github.com/spf13/cobra"
)

// Per-entry and per-item caps for session start. Session start is paid by
// every session and every routine, so no single long entry may set its size;
// the markers name the command that returns the rest.
const (
	sessionEntryMaxChars = 1200
	sessionItemMaxChars  = 500
)

type sessionOut struct {
	Today      string                `json:"today"`
	Weekday    string                `json:"weekday"`
	Now        string                `json:"now"`
	Timezone   string                `json:"timezone"`
	Items      []store.Item          `json:"open_items,omitempty"`
	Overdue    int                   `json:"overdue,omitempty"`
	Journal    []store.Entry         `json:"journal,omitempty"`
	Stale      []store.SeriesSummary `json:"stale_metrics,omitempty"`
	Main       *store.DocFile        `json:"main,omitempty"`
	Sections   []store.DocFile       `json:"sections,omitempty"`
	References []store.DocFile       `json:"references,omitempty"`
}

func (a *App) sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Session start: date, open items, journal, stale metrics, record files",
	}
	var scopes []string
	var full, headlines int
	start := &cobra.Command{
		Use:   "start",
		Short: "Everything a coaching session needs first, in one call",
		Long: `Prints today's date from the system clock in the coaching timezone, the open
items (overdue first), the latest journal entries (newest in full, older as
headlines), state metrics past their staleness window, and the list of prose
files. main.md is not inlined: read it with your file tool.

--scope narrows the output for runs that need only a slice.`,
		Example: `  coachctl session start
  coachctl session start --scope items --scope journal
  coachctl session start --scope context --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			want := map[string]bool{}
			for _, sc := range scopes {
				switch sc {
				case "context", "items", "journal":
					want[sc] = true
				default:
					return usagef("--scope must be context, items or journal")
				}
			}
			if len(want) == 0 {
				want = map[string]bool{"context": true, "items": true, "journal": true}
			}
			s, err := a.store()
			if err != nil {
				return err
			}
			now := s.Now()
			out := sessionOut{
				Today: s.Today(), Weekday: now.Weekday().String(),
				Now: now.Format(time.RFC3339), Timezone: s.Loc.String(),
			}
			// Stale metrics ride along on every scope: they are the mechanical
			// signal a narrowed start must not hide.
			if out.Stale, err = s.Stale(); err != nil {
				return err
			}
			if want["items"] {
				items, err := s.Items()
				if err != nil {
					return err
				}
				for _, it := range items {
					if it.Status == store.StatusOpen {
						out.Items = append(out.Items, it)
						if it.Overdue(out.Today) {
							out.Overdue++
						}
					}
				}
				sortOpenItems(out.Items, out.Today)
			}
			if want["journal"] {
				all, err := s.Journal()
				if err != nil {
					return err
				}
				for i := len(all) - 1; i >= 0 && len(out.Journal) < full+headlines; i-- {
					if !all[i].Archived {
						out.Journal = append(out.Journal, all[i])
					}
				}
			}
			if want["context"] {
				if out.Main, out.Sections, out.References, err = s.Docs(); err != nil {
					return err
				}
			}
			if a.JSON {
				return a.printJSON(out)
			}
			a.printSession(out, s.Loc, want, full)
			return nil
		},
	}
	start.Flags().StringSliceVar(&scopes, "scope", nil, "context, items or journal (repeatable; default all)")
	start.Flags().IntVar(&full, "journal-full", 2, "newest journal entries shown in full")
	start.Flags().IntVar(&headlines, "journal-headlines", 10, "further entries shown as one-line headlines")
	cmd.AddCommand(start)
	return cmd
}

func (a *App) printSession(out sessionOut, loc *time.Location, want map[string]bool, full int) {
	now, _ := time.Parse(time.RFC3339, out.Now)
	a.printf("Today: %s %s, %s (%s)\n", now.Weekday().String()[:3], out.Today, now.Format("15:04"), out.Timezone)
	if len(out.Stale) > 0 {
		parts := make([]string, 0, len(out.Stale))
		for _, r := range out.Stale {
			parts = append(parts, fmt.Sprintf("%s = %s (measured %s, stale after %d d)", r.Name, fmtValue(r.Latest.Value, r.Latest.Unit), r.Latest.MeasuredAt[:10], r.StaleAfterDays))
		}
		a.printf("\n⚠ Stale state metrics: %s. Remeasure or re-record.\n", strings.Join(parts, "; "))
	}
	if want["items"] {
		header := fmt.Sprintf("## Open items (%d open", len(out.Items))
		if out.Overdue > 0 {
			header += fmt.Sprintf(", %d OVERDUE", out.Overdue)
		}
		a.printf("\n%s)\n\n", header)
		if len(out.Items) == 0 {
			a.printf("No open items.\n")
		}
		for _, it := range out.Items {
			a.printf("%s\n", itemLine(it, out.Today, false, sessionItemMaxChars))
		}
	}
	if want["journal"] {
		if len(out.Journal) == 0 {
			a.printf("\n## Journal\n\nNo journal entries yet.\n")
		}
		n := min(full, len(out.Journal))
		if n > 0 {
			a.printf("\n## Journal: latest %d in full\n\n", n)
			for i, e := range out.Journal[:n] {
				if i > 0 {
					a.printf("\n---\n\n")
				}
				a.printf("%s\n", journalFull(e, loc, sessionEntryMaxChars))
			}
		}
		if len(out.Journal) > n {
			a.printf("\n## Journal: earlier headlines (coachctl journal show <id> for the full text)\n\n")
			for _, e := range out.Journal[n:] {
				a.printf("%s\n", journalHeadline(e, loc))
			}
		}
	}
	if want["context"] {
		a.printf("\n## Record files (read with your file tool, main.md first)\n\n")
		if out.Main == nil {
			a.printf("main.md is missing: create it, or import a record with `coachctl import`.\n")
		} else {
			a.printf("main.md (%s)\n", humanBytes(out.Main.Bytes))
		}
		for _, group := range []struct {
			name  string
			files []store.DocFile
		}{{"sections", out.Sections}, {"references", out.References}} {
			if len(group.files) == 0 {
				continue
			}
			names := make([]string, 0, len(group.files))
			for _, f := range group.files {
				names = append(names, strings.TrimSuffix(strings.TrimPrefix(f.Path, group.name+"/"), ".md"))
			}
			a.printf("%s/: %s\n", group.name, strings.Join(names, ", "))
		}
	}
}

func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}
