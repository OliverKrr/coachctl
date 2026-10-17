// Package importer moves a coaching-mcp snapshot into a coachctl record. A
// snapshot directory holds SKILL.md, sections/, references/ and records.json
// (journal, open items, metrics, series definitions, with their ids).
package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/OliverKrr/coachctl/internal/store"
)

// RecordsFile is the machine-readable part of a snapshot.
const RecordsFile = "records.json"

// Records mirrors records.json. Timestamps are SQLite UTC
// ("YYYY-MM-DD HH:MM:SS"); measured_at may also be a local date or minute.
type Records struct {
	Format  string `json:"format"`
	Journal []struct {
		ID          int     `json:"id"`
		Entry       string  `json:"entry"`
		CreatedAt   string  `json:"created_at"`
		Correction  *string `json:"correction"`
		CorrectedAt *string `json:"corrected_at"`
		ArchivedAt  *string `json:"archived_at"`
	} `json:"journal"`
	OpenItems []struct {
		ID           int     `json:"id"`
		Kind         string  `json:"kind"`
		Content      string  `json:"content"`
		Status       string  `json:"status"`
		Source       *string `json:"source"`
		DedupKey     *string `json:"dedup_key"`
		RelevantDate *string `json:"relevant_date"`
		ResolvedNote *string `json:"resolved_note"`
		CreatedAt    string  `json:"created_at"`
		UpdatedAt    string  `json:"updated_at"`
	} `json:"open_items"`
	MetricSeries []struct {
		Name           string `json:"name"`
		Kind           string `json:"kind"`
		StaleAfterDays *int   `json:"stale_after_days"`
	} `json:"metric_series"`
	Metrics []struct {
		ID         int     `json:"id"`
		Name       string  `json:"name"`
		Value      float64 `json:"value"`
		Unit       *string `json:"unit"`
		Note       *string `json:"note"`
		MeasuredAt string  `json:"measured_at"`
		CreatedAt  string  `json:"created_at"`
	} `json:"metrics"`
	Routines []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"routines"`
}

// Report says what an import wrote.
type Report struct {
	Main       bool              `json:"main"`
	Sections   int               `json:"sections"`
	References int               `json:"references"`
	Journal    int               `json:"journal_entries"`
	Archived   int               `json:"archived_entries"`
	Items      int               `json:"open_items"`
	Series     int               `json:"series"`
	Points     int               `json:"metric_points"`
	Renamed    map[string]string `json:"renamed_series,omitempty"`
	Routines   []string          `json:"routines_not_imported,omitempty"`
}

const sqliteLayout = "2006-01-02 15:04:05"

func parseUTC(v string) (time.Time, error) {
	t, err := time.ParseInLocation(sqliteLayout, v, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q: %w", v, err)
	}
	return t, nil
}

var (
	localMinuteRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$`)
	localDateRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	utcSecondRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)
	slugRe        = regexp.MustCompile(`[^a-z0-9._-]+`)
)

// measuredAt converts a stored measured_at. A value with seconds is the
// server's default UTC clock; a date or a minute was given by the user in
// their local time and is kept.
func measuredAt(v string, loc *time.Location) (string, error) {
	switch {
	case localDateRe.MatchString(v), localMinuteRe.MatchString(v):
		return v, nil
	case utcSecondRe.MatchString(v):
		t, err := parseUTC(v)
		if err != nil {
			return "", err
		}
		return t.In(loc).Format("2006-01-02 15:04"), nil
	}
	return "", fmt.Errorf("measured_at %q is not a date or date-time", v)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Run imports dir into s. Prose files that already exist with different
// content are conflicts unless force is set; starter is the content `coachctl
// init` writes to main.md, which an import may always replace.
func Run(s *store.Store, dir string, force bool, starter string) (Report, error) {
	var rep Report
	data, err := os.ReadFile(filepath.Join(dir, RecordsFile))
	if errors.Is(err, os.ErrNotExist) {
		return rep, &store.UsageError{Msg: fmt.Sprintf("%s has no %s: take the snapshot with a coaching-mcp version that writes it (coaching-mcp-snapshot <dir> --db <skill.db>)", dir, RecordsFile)}
	}
	if err != nil {
		return rep, err
	}
	var recs Records
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&recs); err != nil {
		return rep, fmt.Errorf("%s: %w", RecordsFile, err)
	}
	if !strings.HasPrefix(recs.Format, "coaching-mcp-records/1") {
		return rep, &store.UsageError{Msg: fmt.Sprintf("%s has format %q; this coachctl reads coaching-mcp-records/1", RecordsFile, recs.Format)}
	}

	prose, err := proseFiles(dir)
	if err != nil {
		return rep, err
	}
	if !force {
		var conflicts []string
		for dst, content := range prose {
			old, err := os.ReadFile(filepath.Join(s.Root, dst))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return rep, err
			}
			same := strings.TrimRight(string(old), "\n") == strings.TrimRight(content, "\n")
			if !same && (dst != store.MainFile || string(old) != starter) {
				conflicts = append(conflicts, dst)
			}
		}
		if len(conflicts) > 0 {
			sort.Strings(conflicts)
			return rep, &store.UsageError{Msg: "these record files already exist with other content: " + strings.Join(conflicts, ", ") + " (re-run with --force to overwrite them)"}
		}
	}

	entries, err := journalEntries(recs, s.Loc)
	if err != nil {
		return rep, err
	}
	items, err := openItems(recs, s.Loc)
	if err != nil {
		return rep, err
	}
	defs, points, renamed, err := metrics(recs, s.Loc)
	if err != nil {
		return rep, err
	}

	// Records first: they refuse a non-empty record, before any prose moves.
	if err := s.ImportJournal(entries); err != nil {
		return rep, err
	}
	if err := s.ImportItems(items); err != nil {
		return rep, err
	}
	if err := s.ImportMetrics(defs, points); err != nil {
		return rep, err
	}
	for dst, content := range prose {
		if err := s.WriteDoc(dst, content); err != nil {
			return rep, err
		}
		switch {
		case dst == store.MainFile:
			rep.Main = true
		case strings.HasPrefix(dst, store.SectionsDir+"/"):
			rep.Sections++
		default:
			rep.References++
		}
	}
	rep.Journal = len(entries)
	for _, e := range entries {
		if e.Archived {
			rep.Archived++
		}
	}
	rep.Items = len(items)
	rep.Series = len(defs)
	for _, ps := range points {
		rep.Points += len(ps)
	}
	if len(renamed) > 0 {
		rep.Renamed = renamed
	}
	for _, r := range recs.Routines {
		rep.Routines = append(rep.Routines, r.Name)
	}
	return rep, nil
}

// proseFiles maps record paths to content: SKILL.md becomes main.md.
func proseFiles(dir string) (map[string]string, error) {
	out := map[string]string{}
	main, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("snapshot has no SKILL.md: %w", err)
	}
	out[store.MainFile] = string(main)
	for _, sub := range []string{store.SectionsDir, store.ReferencesDir} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			content, err := os.ReadFile(filepath.Join(dir, sub, e.Name()))
			if err != nil {
				return nil, err
			}
			out[sub+"/"+e.Name()] = string(content)
		}
	}
	return out, nil
}

func journalEntries(recs Records, loc *time.Location) ([]store.Entry, error) {
	out := make([]store.Entry, 0, len(recs.Journal))
	for _, j := range recs.Journal {
		at, err := parseUTC(j.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("journal #%d: %w", j.ID, err)
		}
		e := store.Entry{ID: j.ID, At: at.In(loc), Body: j.Entry, Archived: j.ArchivedAt != nil}
		if c := strings.TrimSpace(deref(j.Correction)); c != "" {
			when := at
			if j.CorrectedAt != nil {
				if when, err = parseUTC(*j.CorrectedAt); err != nil {
					return nil, fmt.Errorf("journal #%d: %w", j.ID, err)
				}
			}
			e.Corrections = []store.Correction{{At: when.In(loc), Text: c}}
		}
		out = append(out, e)
	}
	return out, nil
}

func openItems(recs Records, loc *time.Location) ([]store.Item, error) {
	out := make([]store.Item, 0, len(recs.OpenItems))
	for _, r := range recs.OpenItems {
		created, err := parseUTC(r.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("open item #%d: %w", r.ID, err)
		}
		updated, err := parseUTC(r.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("open item #%d: %w", r.ID, err)
		}
		out = append(out, store.Item{
			ID: r.ID, Kind: r.Kind, Status: r.Status, Content: r.Content,
			Source: deref(r.Source), DedupKey: deref(r.DedupKey), RelevantDate: deref(r.RelevantDate),
			ResolvedNote: deref(r.ResolvedNote), CreatedAt: created.In(loc), UpdatedAt: updated.In(loc),
		})
	}
	return out, nil
}

func slug(name string) string {
	s := slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	s = strings.Trim(s, "-._")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func metrics(recs Records, loc *time.Location) ([]store.Series, map[string][]store.Point, map[string]string, error) {
	renamed := map[string]string{}
	rename := func(name string) (string, error) {
		if store.ValidSeriesName(name) {
			return name, nil
		}
		s := slug(name)
		if !store.ValidSeriesName(s) {
			return "", fmt.Errorf("metric series %q has no usable file name", name)
		}
		renamed[name] = s
		return s, nil
	}
	kinds := map[string]store.Series{}
	for _, ms := range recs.MetricSeries {
		name, err := rename(ms.Name)
		if err != nil {
			return nil, nil, nil, err
		}
		def := store.Series{Name: name, Kind: ms.Kind}
		if ms.StaleAfterDays != nil && ms.Kind == store.KindState {
			def.StaleAfterDays = *ms.StaleAfterDays
		}
		kinds[name] = def
	}
	points := map[string][]store.Point{}
	for _, m := range recs.Metrics {
		name, err := rename(m.Name)
		if err != nil {
			return nil, nil, nil, err
		}
		at, err := measuredAt(m.MeasuredAt, loc)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("metric #%d: %w", m.ID, err)
		}
		created, err := parseUTC(m.CreatedAt)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("metric #%d: %w", m.ID, err)
		}
		points[name] = append(points[name], store.Point{
			ID: m.ID, Value: m.Value, Unit: deref(m.Unit), Note: deref(m.Note), MeasuredAt: at, CreatedAt: created.In(loc),
		})
		if _, ok := kinds[name]; !ok {
			kinds[name] = store.Series{Name: name, Kind: store.KindEvent}
		}
	}
	defs := make([]store.Series, 0, len(kinds))
	for name, def := range kinds {
		// A series' unit is its newest point's unit; older points keep theirs.
		if ps := points[name]; len(ps) > 0 {
			newest := ps[0]
			for _, p := range ps[1:] {
				if p.ID > newest.ID {
					newest = p
				}
			}
			def.Unit = newest.Unit
		}
		defs = append(defs, def)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs, points, renamed, nil
}
