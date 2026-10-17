package cli

import (
	"sort"
	"strings"

	"github.com/OliverKrr/coachctl/internal/importer"
	"github.com/spf13/cobra"
)

func (a *App) importCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "import <coaching-mcp snapshot dir>",
		Short: "Import a coaching-mcp snapshot into an empty record",
		Long: `Move a coaching-mcp record into COACH_HOME: SKILL.md becomes main.md,
sections/ and references/ are copied, and records.json fills the journal, open
items and metrics with their original ids, so "#147" still names the same
entry. The journal, items and metrics must be empty. Stored routine prompts are
not imported (list them in the report and keep them as files of your own).`,
		Example: "  coachctl import ~/snapshots/coaching-2026-10-17",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.store()
			if err != nil {
				return err
			}
			rep, err := importer.Run(s, args[0], force, StarterMain())
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(rep)
			}
			a.printf("Imported into %s:\n", s.Root)
			a.printf("  main.md, %d sections, %d references\n", rep.Sections, rep.References)
			a.printf("  %d journal entries (%d archived), %d open items\n", rep.Journal, rep.Archived, rep.Items)
			a.printf("  %d metric series, %d points\n", rep.Series, rep.Points)
			if len(rep.Renamed) > 0 {
				var pairs []string
				for from, to := range rep.Renamed {
					pairs = append(pairs, from+" → "+to)
				}
				sort.Strings(pairs)
				a.printf("  renamed series: %s\n", strings.Join(pairs, ", "))
			}
			if len(rep.Routines) > 0 {
				a.printf("  not imported (stored routine prompts): %s\n", strings.Join(rep.Routines, ", "))
			}
			a.printf("Review metrics/series.toml (units, descriptions), then commit.\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite prose files that already exist with other content")
	return cmd
}
