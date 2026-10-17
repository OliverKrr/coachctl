package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Correction is a later statement of what is actually true, attached to an
// entry that turned out wrong. The entry text itself is never rewritten.
type Correction struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// Entry is one journal entry. IDs are global and never reused, so "#147"
// stays a stable reference across months and archiving.
type Entry struct {
	ID          int          `json:"id"`
	At          time.Time    `json:"at"`
	Archived    bool         `json:"archived"`
	Body        string       `json:"body"`
	Corrections []Correction `json:"corrections,omitempty"`
}

// The month files are markdown a person can read, with two line shapes the
// parser relies on. A body line that would look like either is escaped with
// one leading backslash on write and unescaped on read.
var (
	headerRe     = regexp.MustCompile(`^## #(\d+) · (\d{4}-\d{2}-\d{2} \d{2}:\d{2} [+-]\d{2}:\d{2})$`)
	correctionRe = regexp.MustCompile(`^> ⚠ Correction (\d{4}-\d{2}-\d{2} \d{2}:\d{2} [+-]\d{2}:\d{2}): ?(.*)$`)
	monthFileRe  = regexp.MustCompile(`^(\d{4}-\d{2})\.md$`)
)

const correctionTextPrefix = "⚠ Correction "

func lineNeedsEscape(l string) bool {
	bare := strings.TrimLeft(l, `\`)
	return headerRe.MatchString(bare) || correctionRe.MatchString(bare)
}

func escapeBodyLine(l string) string {
	if lineNeedsEscape(l) {
		return `\` + l
	}
	return l
}

func unescapeBodyLine(l string) string {
	if strings.HasPrefix(l, `\`) && lineNeedsEscape(l) {
		return l[1:]
	}
	return l
}

func escapeCorrectionLine(l string) string {
	if strings.HasPrefix(strings.TrimLeft(l, `\`), correctionTextPrefix) {
		return `\` + l
	}
	return l
}

func unescapeCorrectionLine(l string) string {
	if strings.HasPrefix(l, `\`) && strings.HasPrefix(strings.TrimLeft(l, `\`), correctionTextPrefix) {
		return l[1:]
	}
	return l
}

// normalizeText turns CRLF into LF and drops surrounding blank lines and
// trailing whitespace; interior formatting is kept verbatim.
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, " \t\n")
	return strings.TrimLeft(s, "\n")
}

func (s *Store) renderEntry(e Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## #%d · %s\n\n", e.ID, s.formatStamp(e.At))
	for _, l := range strings.Split(e.Body, "\n") {
		b.WriteString(escapeBodyLine(l))
		b.WriteByte('\n')
	}
	for i, c := range e.Corrections {
		if i == 0 {
			b.WriteByte('\n')
		}
		lines := strings.Split(c.Text, "\n")
		fmt.Fprintf(&b, "> ⚠ Correction %s: %s\n", s.formatStamp(c.At), lines[0])
		for _, l := range lines[1:] {
			if l == "" {
				b.WriteString(">\n")
				continue
			}
			b.WriteString("> " + escapeCorrectionLine(l) + "\n")
		}
	}
	return b.String()
}

func (s *Store) renderMonth(title string, entries []Entry) []byte {
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	var b strings.Builder
	b.WriteString("# " + title + "\n")
	for _, e := range entries {
		b.WriteString("\n")
		b.WriteString(s.renderEntry(e))
	}
	return []byte(b.String())
}

// parseMonth reads one month file. Lines before the first entry header (the
// title) are ignored.
func parseMonth(data []byte, archived bool, name string) ([]Entry, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var entries []Entry
	var cur *Entry
	var block []string
	flush := func() error {
		if cur == nil {
			return nil
		}
		if err := fillEntry(cur, block); err != nil {
			return fmt.Errorf("%s: entry #%d: %w", name, cur.ID, err)
		}
		entries = append(entries, *cur)
		return nil
	}
	for _, l := range lines {
		if m := headerRe.FindStringSubmatch(l); m != nil {
			if err := flush(); err != nil {
				return nil, err
			}
			id, _ := strconv.Atoi(m[1])
			at, err := parseStamp(m[2])
			if err != nil {
				return nil, fmt.Errorf("%s: entry #%d: %w", name, id, err)
			}
			cur = &Entry{ID: id, At: at, Archived: archived}
			block = nil
			continue
		}
		if cur != nil {
			block = append(block, l)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return entries, nil
}

func fillEntry(e *Entry, block []string) error {
	split := len(block)
	for i, l := range block {
		if correctionRe.MatchString(l) {
			split = i
			break
		}
	}
	body := make([]string, 0, split)
	for _, l := range block[:split] {
		body = append(body, unescapeBodyLine(l))
	}
	e.Body = normalizeText(strings.Join(body, "\n"))
	var cur *Correction
	var text []string
	finish := func() {
		if cur != nil {
			cur.Text = normalizeText(strings.Join(text, "\n"))
			e.Corrections = append(e.Corrections, *cur)
		}
	}
	for _, l := range block[split:] {
		if m := correctionRe.FindStringSubmatch(l); m != nil {
			finish()
			at, err := parseStamp(m[1])
			if err != nil {
				return err
			}
			cur = &Correction{At: at}
			text = []string{m[2]}
			continue
		}
		switch {
		case l == "":
			continue
		case l == ">":
			text = append(text, "")
		case strings.HasPrefix(l, "> "):
			text = append(text, unescapeCorrectionLine(l[2:]))
		default:
			return fmt.Errorf("unexpected line after a correction: %q", l)
		}
	}
	finish()
	return nil
}

func (s *Store) monthPath(month string, archived bool) string {
	if archived {
		return s.path(filepath.Join(ArchiveDir, month+".md"))
	}
	return s.path(filepath.Join(JournalDir, month+".md"))
}

func monthTitle(month string, archived bool) string {
	if archived {
		return "Journal archive " + month
	}
	return "Journal " + month
}

func (s *Store) monthOf(t time.Time) string { return t.In(s.Loc).Format("2006-01") }

func (s *Store) monthFiles(archived bool) ([]string, error) {
	dir := s.path(JournalDir)
	if archived {
		dir = s.path(ArchiveDir)
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var months []string
	for _, e := range entries {
		if m := monthFileRe.FindStringSubmatch(e.Name()); m != nil && !e.IsDir() {
			months = append(months, m[1])
		}
	}
	sort.Strings(months)
	return months, nil
}

func (s *Store) readMonth(month string, archived bool) ([]Entry, error) {
	path := s.monthPath(month, archived)
	data, err := readFileIfExists(path)
	if err != nil || data == nil {
		return nil, err
	}
	rel, _ := filepath.Rel(s.Root, path)
	return parseMonth(data, archived, rel)
}

func (s *Store) writeMonth(month string, archived bool, entries []Entry) error {
	path := s.monthPath(month, archived)
	if len(entries) == 0 {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return writeFileAtomic(path, s.renderMonth(monthTitle(month, archived), entries))
}

// Journal returns every entry, active and archived, oldest first. Duplicate
// ids are an error: they mean a file was edited by hand.
func (s *Store) Journal() ([]Entry, error) {
	var all []Entry
	for _, archived := range []bool{false, true} {
		months, err := s.monthFiles(archived)
		if err != nil {
			return nil, err
		}
		for _, m := range months {
			entries, err := s.readMonth(m, archived)
			if err != nil {
				return nil, err
			}
			all = append(all, entries...)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	for i := 1; i < len(all); i++ {
		if all[i].ID == all[i-1].ID {
			return nil, fmt.Errorf("journal entry #%d appears twice; resolve it by hand (git log -p %s)", all[i].ID, JournalDir)
		}
	}
	return all, nil
}

// AddJournal appends a new entry stamped now.
func (s *Store) AddJournal(body string) (Entry, error) {
	body = normalizeText(body)
	if body == "" {
		return Entry{}, usagef("journal entry is empty: pass the text on stdin")
	}
	unlock, err := s.lock()
	if err != nil {
		return Entry{}, err
	}
	defer unlock()
	all, err := s.Journal()
	if err != nil {
		return Entry{}, err
	}
	next := 1
	if len(all) > 0 {
		next = all[len(all)-1].ID + 1
	}
	e := Entry{ID: next, At: s.Now(), Body: body}
	return e, s.insertEntries([]Entry{e})
}

// insertEntries writes entries into their month files. Callers hold the lock.
func (s *Store) insertEntries(entries []Entry) error {
	type key struct {
		month    string
		archived bool
	}
	byMonth := map[key][]Entry{}
	for _, e := range entries {
		k := key{s.monthOf(e.At), e.Archived}
		byMonth[k] = append(byMonth[k], e)
	}
	keys := make([]key, 0, len(byMonth))
	for k := range byMonth {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].month != keys[j].month {
			return keys[i].month < keys[j].month
		}
		return !keys[i].archived && keys[j].archived
	})
	for _, k := range keys {
		existing, err := s.readMonth(k.month, k.archived)
		if err != nil {
			return err
		}
		if err := s.writeMonth(k.month, k.archived, append(existing, byMonth[k]...)); err != nil {
			return err
		}
	}
	return nil
}

// JournalEntry returns one entry by id.
func (s *Store) JournalEntry(id int) (Entry, error) {
	all, err := s.Journal()
	if err != nil {
		return Entry{}, err
	}
	for _, e := range all {
		if e.ID == id {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("journal entry #%d: %w", id, ErrNotFound)
}

// CorrectJournal attaches a correction to an entry. Earlier corrections stay.
func (s *Store) CorrectJournal(id int, text string) (Entry, error) {
	text = normalizeText(text)
	if text == "" {
		return Entry{}, usagef("correction is empty: pass what is actually true on stdin")
	}
	return s.updateEntry(id, func(e *Entry) {
		e.Corrections = append(e.Corrections, Correction{At: s.Now(), Text: text})
	})
}

// ArchiveJournal moves an entry into journal/archive/ (or back). An archived
// entry keeps its id and stays readable; session start stops showing it.
func (s *Store) ArchiveJournal(id int, archived bool) (Entry, error) {
	return s.updateEntry(id, func(e *Entry) { e.Archived = archived })
}

func (s *Store) updateEntry(id int, change func(*Entry)) (Entry, error) {
	unlock, err := s.lock()
	if err != nil {
		return Entry{}, err
	}
	defer unlock()
	all, err := s.Journal()
	if err != nil {
		return Entry{}, err
	}
	var old *Entry
	for i := range all {
		if all[i].ID == id {
			old = &all[i]
			break
		}
	}
	if old == nil {
		return Entry{}, fmt.Errorf("journal entry #%d: %w", id, ErrNotFound)
	}
	updated := *old
	updated.Corrections = append([]Correction(nil), old.Corrections...)
	change(&updated)

	month := s.monthOf(old.At)
	from, err := s.readMonth(month, old.Archived)
	if err != nil {
		return Entry{}, err
	}
	kept := from[:0]
	for _, e := range from {
		if e.ID != id {
			kept = append(kept, e)
		}
	}
	if updated.Archived == old.Archived {
		return updated, s.writeMonth(month, old.Archived, append(kept, updated))
	}
	to, err := s.readMonth(month, updated.Archived)
	if err != nil {
		return Entry{}, err
	}
	// Write the destination first: a crash in between leaves a duplicate id,
	// which Journal() reports, never a lost entry.
	if err := s.writeMonth(month, updated.Archived, append(to, updated)); err != nil {
		return Entry{}, err
	}
	return updated, s.writeMonth(month, old.Archived, kept)
}

// ImportJournal writes entries with their original ids. The record must not
// have a journal yet.
func (s *Store) ImportJournal(entries []Entry) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	existing, err := s.Journal()
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return usagef("the record already has %d journal entries; import needs an empty journal", len(existing))
	}
	for i := range entries {
		entries[i].Body = normalizeText(entries[i].Body)
		if entries[i].Body == "" {
			entries[i].Body = "(empty entry)"
		}
	}
	return s.insertEntries(entries)
}
