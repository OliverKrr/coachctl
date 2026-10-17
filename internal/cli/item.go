package cli

import (
	"fmt"
	"sort"

	"github.com/OliverKrr/coachctl/internal/store"
	"github.com/spf13/cobra"
)

// itemLine renders one item; maxChars > 0 caps the body and names the
// command that shows the rest.
func itemLine(it store.Item, today string, withStatus bool, maxChars int) string {
	label := it.Kind
	if withStatus {
		label += ", " + it.Status
	}
	dates := "opened " + it.CreatedAt.Format("2006-01-02")
	if it.RelevantDate != "" {
		dates += ", for " + it.RelevantDate
	}
	if it.Overdue(today) {
		dates += " — OVERDUE"
	}
	content := it.Content
	if r := []rune(content); maxChars > 0 && len(r) > maxChars {
		content = fmt.Sprintf("%s… (+%d chars — coachctl item list --id %d)", string(r[:maxChars]), len(r)-maxChars, it.ID)
	}
	line := fmt.Sprintf("#%d [%s] (%s) %s", it.ID, label, dates, content)
	if it.ResolvedNote != "" {
		line += "  — resolved: " + it.ResolvedNote
	}
	if it.Source != "" {
		line += "  — src: " + it.Source
	}
	return line
}

// sortOpenItems puts overdue items first, then newest first.
func sortOpenItems(items []store.Item, today string) {
	sort.SliceStable(items, func(i, j int) bool {
		oi, oj := items[i].Overdue(today), items[j].Overdue(today)
		if oi != oj {
			return oi
		}
		return items[i].ID > items[j].ID
	})
}

func (a *App) itemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "item",
		Short: "Open items: commitments and flags",
		Long: `An open item is a commitment the athlete made (an if-then next action) or a
flag a routine raised for the coach to surface. Items are resolved, never
deleted, and live in open-items.jsonl, written only through coachctl.`,
	}
	cmd.AddCommand(a.itemAddCmd(), a.itemListCmd(), a.itemResolveCmd())
	return cmd
}

func (a *App) itemAddCmd() *cobra.Command {
	var in store.NewItem
	cmd := &cobra.Command{
		Use:   "add [text]",
		Short: "Record a commitment or flag (text as argument or on stdin)",
		Long: `Record a commitment or flag. With --dedup-key, an OPEN item with the same key
is kept and nothing new is added, so a recurring condition is raised once.`,
		Example: `  coachctl item add --kind commitment --date 2026-10-21 "If the calf is quiet on Tuesday, run the 6x1k"
  coachctl item add --kind flag --source readiness --dedup-key hrv-low-2026-10-17 --date 2026-10-17 "HRV 18% under baseline"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := a.textArg(args, "the item text")
			if err != nil {
				return err
			}
			in.Content = text
			s, err := a.store()
			if err != nil {
				return err
			}
			it, created, err := s.AddItem(in)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(map[string]any{"item": it, "created": created})
			}
			if !created {
				a.printf("Open item #%d already open (dedup on %q); nothing added.\n", it.ID, in.DedupKey)
				return nil
			}
			a.printf("Open item #%d added (%s).\n", it.ID, it.Kind)
			return nil
		},
	}
	cmd.Flags().StringVar(&in.Kind, "kind", "", "commitment or flag (required)")
	cmd.Flags().StringVar(&in.Source, "source", "", "provenance, e.g. session, weekly-review, readiness")
	cmd.Flags().StringVar(&in.DedupKey, "dedup-key", "", "stable key of a recurring condition")
	cmd.Flags().StringVar(&in.RelevantDate, "date", "", "the date the item is about or due (YYYY-MM-DD)")
	_ = cmd.MarkFlagRequired("kind")
	return cmd
}

func (a *App) itemListCmd() *cobra.Command {
	var status, kind string
	var overdue, short bool
	var ids []int
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List items (default: open ones, overdue first)",
		Example: `  coachctl item list
  coachctl item list --overdue
  coachctl item list --kind flag --json
  coachctl item list --status all --limit 50
  coachctl item list --id 81`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch status {
			case "open", "done", "dismissed", "all":
			default:
				return usagef("--status must be open, done, dismissed or all")
			}
			if kind != "" && kind != store.KindCommitment && kind != store.KindFlag {
				return usagef("--kind must be commitment or flag")
			}
			s, err := a.store()
			if err != nil {
				return err
			}
			all, err := s.Items()
			if err != nil {
				return err
			}
			today := s.Today()
			want := map[int]bool{}
			for _, id := range ids {
				want[id] = true
			}
			var out []store.Item
			for _, it := range all {
				switch {
				case len(want) > 0 && !want[it.ID]:
				case len(want) == 0 && status != "all" && it.Status != status:
				case kind != "" && it.Kind != kind:
				case overdue && !it.Overdue(today):
				default:
					out = append(out, it)
				}
			}
			sortOpenItems(out, today)
			total := len(out)
			if limit > 0 && len(out) > limit {
				out = out[:limit]
			}
			if a.JSON {
				return a.printJSON(nonNil(out))
			}
			if len(out) == 0 {
				a.printf("No matching items.\n")
				return nil
			}
			if total > len(out) {
				a.printf("Showing %d of %d items; raise --limit for the rest.\n\n", len(out), total)
			}
			maxChars := 0
			if short {
				maxChars = 120
			}
			for _, it := range out {
				a.printf("%s\n", itemLine(it, today, status != "open" || len(want) > 0, maxChars))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "open", "open, done, dismissed or all")
	cmd.Flags().StringVar(&kind, "kind", "", "only commitment or flag")
	cmd.Flags().BoolVar(&overdue, "overdue", false, "only open items whose date has passed")
	cmd.Flags().BoolVar(&short, "short", false, "cap each item at 120 characters")
	cmd.Flags().IntSliceVar(&ids, "id", nil, "only these ids, any status (repeatable)")
	cmd.Flags().IntVar(&limit, "limit", 100, "max items (0 = all)")
	return cmd
}

func (a *App) itemResolveCmd() *cobra.Command {
	var note string
	var dismiss bool
	cmd := &cobra.Command{
		Use:   "resolve <id>",
		Short: "Close an item as done (or dismissed)",
		Example: `  coachctl item resolve 81 --note "Ran the 6x1k on 21.10., calf fine"
  coachctl item resolve 77 --dismiss --note "Superseded by the new block plan"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "item")
			if err != nil {
				return err
			}
			status := store.StatusDone
			if dismiss {
				status = store.StatusDismissed
			}
			s, err := a.store()
			if err != nil {
				return err
			}
			it, err := s.ResolveItem(id, status, note)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(it)
			}
			a.printf("Open item #%d marked %s.\n", it.ID, it.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "what happened, stored next to the item")
	cmd.Flags().BoolVar(&dismiss, "dismiss", false, "dropped rather than done")
	return cmd
}
