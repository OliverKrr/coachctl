package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/OliverKrr/coachctl/internal/store"
	"github.com/spf13/cobra"
)

const headlineMaxChars = 160

func stamp(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02 15:04") }

// correctionLines renders corrections in full: a clipped correction would
// let the wrong statement it overrules be read as true.
func correctionLines(e store.Entry, loc *time.Location) string {
	var b strings.Builder
	for _, c := range e.Corrections {
		fmt.Fprintf(&b, "\n⚠ Correction [%s]: %s", stamp(c.At, loc), c.Text)
	}
	return b.String()
}

func archivedMark(e store.Entry) string {
	if e.Archived {
		return " [archived]"
	}
	return ""
}

func journalHeadline(e store.Entry, loc *time.Location) string {
	lines := strings.Split(e.Body, "\n")
	first := strings.TrimSpace(lines[0])
	if r := []rune(first); len(r) > headlineMaxChars {
		first = string(r[:headlineMaxChars]) + "…"
	}
	more := ""
	if n := len(lines) - 1; n > 0 {
		more = fmt.Sprintf(" (+%d more line%s)", n, plural(n))
	}
	return fmt.Sprintf("#%d [%s]%s %s%s%s", e.ID, stamp(e.At, loc), archivedMark(e), first, more, correctionLines(e, loc))
}

// journalFull renders an entry; maxChars > 0 caps the body (never the
// corrections) and names the command that returns the rest.
func journalFull(e store.Entry, loc *time.Location, maxChars int) string {
	body := e.Body
	if r := []rune(body); maxChars > 0 && len(r) > maxChars {
		body = fmt.Sprintf("%s… (+%d chars — coachctl journal show %d)", string(r[:maxChars]), len(r)-maxChars, e.ID)
	}
	return fmt.Sprintf("#%d [%s]%s %s%s", e.ID, stamp(e.At, loc), archivedMark(e), body, correctionLines(e, loc))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func parseID(s, what string) (int, error) {
	id, err := strconv.Atoi(strings.TrimPrefix(s, "#"))
	if err != nil || id <= 0 {
		return 0, usagef("%s id %q must be a positive number", what, s)
	}
	return id, nil
}

func parseIDs(args []string, what string) ([]int, error) {
	ids := make([]int, 0, len(args))
	for _, a := range args {
		id, err := parseID(a, what)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (a *App) journalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "journal",
		Short: "Append, read, correct and archive journal entries",
		Long: `The journal is the session history: decisions, data changes, observations.
Entries live in journal/YYYY-MM.md and are written only through coachctl, which
stamps the time and the id. Read them here, or with rg in the files.`,
	}
	cmd.AddCommand(a.journalAddCmd(), a.journalListCmd(), a.journalShowCmd(), a.journalCorrectCmd(), a.journalArchiveCmd())
	return cmd
}

func (a *App) journalAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Append an entry (text on stdin)",
		Long:  "Append an entry. coachctl records the timestamp, so do not start the text with a date.",
		Example: `  coachctl journal add <<'EOF'
Weekly review: 4 of 5 sessions done, long run cut short (calf).
Decided: no intensity until the calf is symptom-free for 3 days.
EOF`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := a.readStdin("the journal entry")
			if err != nil {
				return err
			}
			s, err := a.store()
			if err != nil {
				return err
			}
			e, err := s.AddJournal(text)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(e)
			}
			a.printf("Journal entry #%d saved (%s).\n", e.ID, stamp(e.At, s.Loc))
			return nil
		},
	}
}

func (a *App) journalListCmd() *cobra.Command {
	var since, until, grep string
	var limit int
	var headlines, archived bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List entries, newest first",
		Example: `  coachctl journal list --since 2026-09-01 --headlines
  coachctl journal list --grep 'SESSION-DEBRIEF 1234' --limit 1
  coachctl journal list --json --limit 50`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var re *regexp.Regexp
			if grep != "" {
				var err error
				if re, err = regexp.Compile("(?i)" + grep); err != nil {
					return usagef("--grep: %v", err)
				}
			}
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
			all, err := s.Journal()
			if err != nil {
				return err
			}
			var out []store.Entry
			for i := len(all) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
				e := all[i]
				day := e.At.In(s.Loc).Format("2006-01-02")
				switch {
				case e.Archived && !archived:
				case since != "" && day < since:
				case until != "" && day > until:
				case re != nil && !re.MatchString(e.Body) && !matchesCorrection(re, e):
				default:
					out = append(out, e)
				}
			}
			if a.JSON {
				return a.printJSON(nonNil(out))
			}
			if len(out) == 0 {
				a.printf("No journal entries match.\n")
				return nil
			}
			for i, e := range out {
				if headlines {
					a.printf("%s\n", journalHeadline(e, s.Loc))
					continue
				}
				if i > 0 {
					a.printf("\n---\n\n")
				}
				a.printf("%s\n", journalFull(e, s.Loc, 0))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "only entries on or after this date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&until, "until", "", "only entries on or before this date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&grep, "grep", "", "only entries matching this case-insensitive regexp")
	cmd.Flags().IntVar(&limit, "limit", 20, "max entries (0 = all)")
	cmd.Flags().BoolVar(&headlines, "headlines", false, "one line per entry")
	cmd.Flags().BoolVar(&archived, "archived", false, "include archived entries")
	return cmd
}

func matchesCorrection(re *regexp.Regexp, e store.Entry) bool {
	for _, c := range e.Corrections {
		if re.MatchString(c.Text) {
			return true
		}
	}
	return false
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (a *App) journalShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "show <id>...",
		Short:   "Print entries in full, archived ones included",
		Example: "  coachctl journal show 147 152",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args, "journal entry")
			if err != nil {
				return err
			}
			s, err := a.store()
			if err != nil {
				return err
			}
			var out []store.Entry
			for _, id := range ids {
				e, err := s.JournalEntry(id)
				if err != nil {
					return err
				}
				out = append(out, e)
			}
			if a.JSON {
				return a.printJSON(out)
			}
			for i, e := range out {
				if i > 0 {
					a.printf("\n---\n\n")
				}
				a.printf("%s\n", journalFull(e, s.Loc, 0))
			}
			return nil
		},
	}
}

func (a *App) journalCorrectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "correct <id>",
		Short: "Attach a correction to an entry (text on stdin)",
		Long: `Attach what is actually true to an entry that stated something wrong. The
entry text stays; every read of it now carries the correction. Use this
instead of a new entry saying an old one was wrong: a reader would have to
find that entry first. Correcting again adds a second correction.`,
		Example: `  coachctl journal correct 147 <<'EOF'
The long run was 24 km, not 28: the watch double-counted a lap.
EOF`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "journal entry")
			if err != nil {
				return err
			}
			text, err := a.readStdin("the correction")
			if err != nil {
				return err
			}
			s, err := a.store()
			if err != nil {
				return err
			}
			e, err := s.CorrectJournal(id, text)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(e)
			}
			a.printf("Correction attached to journal entry #%d; the original text is unchanged.\n", e.ID)
			return nil
		},
	}
}

func (a *App) journalArchiveCmd() *cobra.Command {
	var undo bool
	cmd := &cobra.Command{
		Use:   "archive <id>...",
		Short: "Move entries to journal/archive/ (session start stops showing them)",
		Long: `Archiving is growth control, not deletion. Archive a period once its
substance is condensed into a reference document. Archived entries keep their
ids and stay readable with ` + "`journal show`" + ` and ` + "`journal list --archived`" + `.`,
		Example: "  coachctl journal archive 101 102 103\n  coachctl journal archive 102 --undo",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args, "journal entry")
			if err != nil {
				return err
			}
			s, err := a.store()
			if err != nil {
				return err
			}
			var done []store.Entry
			for _, id := range ids {
				e, err := s.ArchiveJournal(id, !undo)
				if err != nil {
					return err
				}
				done = append(done, e)
			}
			if a.JSON {
				return a.printJSON(done)
			}
			verb := "archived"
			if undo {
				verb = "restored from the archive"
			}
			a.printf("%d journal entr%s %s.\n", len(done), map[bool]string{true: "y", false: "ies"}[len(done) == 1], verb)
			return nil
		},
	}
	cmd.Flags().BoolVar(&undo, "undo", false, "move entries back out of the archive")
	return cmd
}
