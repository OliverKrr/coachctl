package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Series kinds. A state series holds claims about the present (FTP, a
// threshold, an HRV baseline): a new value supersedes the previous one, and
// reads return what is valid now. An event series holds additive facts
// (weekly volume, adherence): nothing supersedes, every point counts.
const (
	KindState = "state"
	KindEvent = "event"
)

var seriesNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Series is one definition in metrics/series.toml.
type Series struct {
	Name           string `toml:"-" json:"name"`
	Kind           string `toml:"kind" json:"kind"`
	Unit           string `toml:"unit,omitempty" json:"unit,omitempty"`
	StaleAfterDays int    `toml:"stale_after_days,omitempty" json:"stale_after_days,omitempty"`
	Description    string `toml:"description,omitempty" json:"description,omitempty"`
}

// Point is one measurement in metrics/<series>.jsonl. MeasuredAt is a local
// date (YYYY-MM-DD) or wall-clock minute ("YYYY-MM-DD HH:MM").
type Point struct {
	ID         int       `json:"id"`
	Value      float64   `json:"value"`
	Unit       string    `json:"unit,omitempty"`
	Note       string    `json:"note,omitempty"`
	MeasuredAt string    `json:"measured_at"`
	CreatedAt  time.Time `json:"created_at"`
}

// ValidSeriesName reports whether name can be a series (and its file name).
func ValidSeriesName(name string) bool { return seriesNameRe.MatchString(name) }

// SeriesDefs parses metrics/series.toml. A missing file is an empty set.
func (s *Store) SeriesDefs() (map[string]Series, error) {
	data, err := readFileIfExists(s.path(SeriesFile))
	if err != nil {
		return nil, err
	}
	raw := map[string]Series{}
	if len(data) > 0 {
		md, err := toml.Decode(string(data), &raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", SeriesFile, err)
		}
		if undecoded := md.Undecoded(); len(undecoded) > 0 {
			return nil, fmt.Errorf("%s: unknown key %s (allowed: kind, unit, stale_after_days, description)", SeriesFile, undecoded[0])
		}
	}
	defs := make(map[string]Series, len(raw))
	for name, def := range raw {
		def.Name = name
		if err := validateSeries(def); err != nil {
			return nil, fmt.Errorf("%s: %w", SeriesFile, err)
		}
		defs[name] = def
	}
	return defs, nil
}

func validateSeries(def Series) error {
	if !ValidSeriesName(def.Name) {
		return fmt.Errorf("series name %q must be lowercase letters, digits, '.', '-' or '_' (max 64)", def.Name)
	}
	if def.Kind != KindState && def.Kind != KindEvent {
		return fmt.Errorf("series %q: kind must be \"state\" or \"event\"", def.Name)
	}
	if def.StaleAfterDays < 0 {
		return fmt.Errorf("series %q: stale_after_days must be positive", def.Name)
	}
	if def.StaleAfterDays > 0 && def.Kind != KindState {
		return fmt.Errorf("series %q: stale_after_days only applies to state series", def.Name)
	}
	return nil
}

func (s *Store) series(name string) (Series, error) {
	defs, err := s.SeriesDefs()
	if err != nil {
		return Series{}, err
	}
	def, ok := defs[name]
	if !ok {
		return Series{}, usagef("series %q is not defined in %s: define it with `coachctl metric define %s --kind state|event [--unit U]`", name, SeriesFile, name)
	}
	return def, nil
}

// DefineSeries appends a new definition to series.toml. Existing definitions
// are hand-edited in the file; this never rewrites it, so comments survive.
func (s *Store) DefineSeries(def Series) error {
	if err := validateSeries(def); err != nil {
		return &UsageError{Msg: err.Error()}
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	defs, err := s.SeriesDefs()
	if err != nil {
		return err
	}
	if _, ok := defs[def.Name]; ok {
		return usagef("series %q already exists; change it by editing %s", def.Name, SeriesFile)
	}
	data, err := readFileIfExists(s.path(SeriesFile))
	if err != nil {
		return err
	}
	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		b.WriteByte('\n')
	}
	if len(data) > 0 {
		b.WriteByte('\n')
	}
	b.WriteString(renderSeries(def))
	return writeFileAtomic(s.path(SeriesFile), []byte(b.String()))
}

func renderSeries(def Series) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\nkind = %q\n", def.Name, def.Kind)
	if def.Unit != "" {
		fmt.Fprintf(&b, "unit = %q\n", def.Unit)
	}
	if def.StaleAfterDays > 0 {
		fmt.Fprintf(&b, "stale_after_days = %d\n", def.StaleAfterDays)
	}
	if def.Description != "" {
		fmt.Fprintf(&b, "description = %q\n", def.Description)
	}
	return b.String()
}

func (s *Store) pointsPath(series string) string {
	return s.path(filepath.Join(MetricsDir, series+".jsonl"))
}

// Points returns a series' measurements in measurement order (ties by id).
func (s *Store) Points(series string) ([]Point, error) {
	data, err := readFileIfExists(s.pointsPath(series))
	if err != nil {
		return nil, err
	}
	var points []Point
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var p Point
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, fmt.Errorf("%s/%s.jsonl line %d: %w", MetricsDir, series, line, err)
		}
		if _, err := parseMeasuredAt(p.MeasuredAt); err != nil {
			return nil, fmt.Errorf("%s/%s.jsonl line %d: %w", MetricsDir, series, line, err)
		}
		points = append(points, p)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sortPoints(points)
	return points, nil
}

func sortPoints(points []Point) {
	sort.SliceStable(points, func(i, j int) bool {
		ki, kj := sortKey(points[i].MeasuredAt), sortKey(points[j].MeasuredAt)
		if ki != kj {
			return ki < kj
		}
		return points[i].ID < points[j].ID
	})
}

func (s *Store) writePoints(series string, points []Point) error {
	sortPoints(points)
	var b bytes.Buffer
	for _, p := range points {
		line, err := json.Marshal(p)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return writeFileAtomic(s.pointsPath(series), b.Bytes())
}

// seriesWithData lists every series that has a points file.
func (s *Store) seriesWithData() ([]string, error) {
	entries, err := os.ReadDir(s.path(MetricsDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".jsonl"); ok && !e.IsDir() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// nextPointID scans every series: ids are global, so "#412" names one
// measurement without its series.
func (s *Store) nextPointID() (int, error) {
	names, err := s.seriesWithData()
	if err != nil {
		return 0, err
	}
	next := 1
	for _, name := range names {
		points, err := s.Points(name)
		if err != nil {
			return 0, err
		}
		for _, p := range points {
			if p.ID >= next {
				next = p.ID + 1
			}
		}
	}
	return next, nil
}

// NewPoint is the input of RecordMetric.
type NewPoint struct {
	Series     string
	Value      float64
	Unit       string
	Note       string
	MeasuredAt string
}

// Recorded is what RecordMetric reports back.
type Recorded struct {
	Point      Point  `json:"point"`
	Series     Series `json:"series"`
	Supersedes *Point `json:"supersedes,omitempty"`
	// Historical is set when a state value was backdated behind a newer one.
	Historical *Point `json:"superseded_by,omitempty"`
}

// RecordMetric appends one measurement to a defined series.
func (s *Store) RecordMetric(in NewPoint) (Recorded, error) {
	if math.IsNaN(in.Value) || math.IsInf(in.Value, 0) {
		return Recorded{}, usagef("value must be a finite number")
	}
	def, err := s.series(in.Series)
	if err != nil {
		return Recorded{}, err
	}
	unit := in.Unit
	if unit == "" {
		unit = def.Unit
	} else if def.Unit != "" && unit != def.Unit {
		return Recorded{}, usagef("series %q is measured in %q, not %q: convert the value, or record into a different series", def.Name, def.Unit, unit)
	}
	measuredAt := in.MeasuredAt
	if measuredAt == "" {
		measuredAt = s.Now().Format(minuteLayout)
	} else if measuredAt, err = parseMeasuredAt(measuredAt); err != nil {
		return Recorded{}, err
	}
	unlock, err := s.lock()
	if err != nil {
		return Recorded{}, err
	}
	defer unlock()
	id, err := s.nextPointID()
	if err != nil {
		return Recorded{}, err
	}
	points, err := s.Points(def.Name)
	if err != nil {
		return Recorded{}, err
	}
	p := Point{ID: id, Value: in.Value, Unit: unit, Note: normalizeText(in.Note), MeasuredAt: measuredAt, CreatedAt: s.Now()}
	out := Recorded{Point: p, Series: def}
	if def.Kind == KindState {
		prevCurrent := current(points)
		withNew := append(append([]Point(nil), points...), p)
		sortPoints(withNew)
		if cur := current(withNew); cur != nil && cur.ID != p.ID {
			out.Historical = cur
		} else if prevCurrent != nil {
			out.Supersedes = prevCurrent
		}
	}
	return out, s.writePoints(def.Name, append(points, p))
}

func current(sorted []Point) *Point {
	if len(sorted) == 0 {
		return nil
	}
	p := sorted[len(sorted)-1]
	return &p
}

// DeletePoint removes one measurement by id (a typo, a wrong unit).
func (s *Store) DeletePoint(id int) (string, Point, error) {
	unlock, err := s.lock()
	if err != nil {
		return "", Point{}, err
	}
	defer unlock()
	names, err := s.seriesWithData()
	if err != nil {
		return "", Point{}, err
	}
	for _, name := range names {
		points, err := s.Points(name)
		if err != nil {
			return "", Point{}, err
		}
		for i, p := range points {
			if p.ID == id {
				rest := append(points[:i:i], points[i+1:]...)
				return name, p, s.writePoints(name, rest)
			}
		}
	}
	return "", Point{}, fmt.Errorf("metric point #%d: %w", id, ErrNotFound)
}

// Window is a state value with the span it was valid for.
type Window struct {
	Point
	ValidTo string `json:"valid_to,omitempty"`
}

// StateHistory returns every value of a state series with its validity
// window: each value is valid until the next measurement.
func StateHistory(points []Point) []Window {
	out := make([]Window, len(points))
	for i, p := range points {
		out[i] = Window{Point: p}
		if i+1 < len(points) {
			out[i].ValidTo = points[i+1].MeasuredAt
		}
	}
	return out
}

// ValueAsOf returns the state value valid at a moment, or nil.
func ValueAsOf(points []Point, at string) *Point {
	key := sortKey(at)
	if len(at) == len(dateLayout) {
		key = at + " 23:59"
	}
	var found *Point
	for i := range points {
		if sortKey(points[i].MeasuredAt) <= key {
			found = &points[i]
		}
	}
	return found
}

// SeriesSummary is one row of the metrics overview.
type SeriesSummary struct {
	Series
	Defined bool   `json:"defined"`
	Count   int    `json:"count"`
	First   string `json:"first,omitempty"`
	Last    string `json:"last,omitempty"`
	Latest  *Point `json:"latest,omitempty"`
	Stale   bool   `json:"stale"`
}

// Summaries lists every defined series and every series with data.
func (s *Store) Summaries() ([]SeriesSummary, error) {
	defs, err := s.SeriesDefs()
	if err != nil {
		return nil, err
	}
	names, err := s.seriesWithData()
	if err != nil {
		return nil, err
	}
	all := map[string]bool{}
	for n := range defs {
		all[n] = true
	}
	for _, n := range names {
		all[n] = true
	}
	sorted := make([]string, 0, len(all))
	for n := range all {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	today := s.Today()
	var out []SeriesSummary
	for _, n := range sorted {
		def, defined := defs[n]
		if !defined {
			def = Series{Name: n, Kind: KindEvent}
		}
		points, err := s.Points(n)
		if err != nil {
			return nil, err
		}
		row := SeriesSummary{Series: def, Defined: defined, Count: len(points)}
		if len(points) > 0 {
			row.First = points[0].MeasuredAt
			row.Last = points[len(points)-1].MeasuredAt
			row.Latest = current(points)
			row.Stale = isStale(def, row.Latest, today)
		}
		out = append(out, row)
	}
	return out, nil
}

func isStale(def Series, latest *Point, today string) bool {
	if def.Kind != KindState || def.StaleAfterDays <= 0 || latest == nil {
		return false
	}
	measured, err := time.Parse(dateLayout, latest.MeasuredAt[:len(dateLayout)])
	if err != nil {
		return false
	}
	now, err := time.Parse(dateLayout, today)
	if err != nil {
		return false
	}
	return now.Sub(measured) > time.Duration(def.StaleAfterDays)*24*time.Hour
}

// Stale lists the state series whose current value is past stale_after_days.
func (s *Store) Stale() ([]SeriesSummary, error) {
	all, err := s.Summaries()
	if err != nil {
		return nil, err
	}
	var out []SeriesSummary
	for _, row := range all {
		if row.Stale {
			out = append(out, row)
		}
	}
	return out, nil
}

// ImportMetrics writes series definitions and points into an empty metrics
// directory, keeping point ids.
func (s *Store) ImportMetrics(defs []Series, points map[string][]Point) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	existing, err := s.seriesWithData()
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return usagef("the record already has metric data; import needs an empty %s/", MetricsDir)
	}
	have, err := s.SeriesDefs()
	if err != nil {
		return err
	}
	var b strings.Builder
	data, err := readFileIfExists(s.path(SeriesFile))
	if err != nil {
		return err
	}
	b.Write(data)
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	for _, def := range defs {
		if err := validateSeries(def); err != nil {
			return err
		}
		if _, ok := have[def.Name]; ok {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderSeries(def))
	}
	if err := writeFileAtomic(s.path(SeriesFile), []byte(b.String())); err != nil {
		return err
	}
	for name, ps := range points {
		for _, p := range ps {
			if _, err := parseMeasuredAt(p.MeasuredAt); err != nil {
				return fmt.Errorf("series %s point #%d: %w", name, p.ID, err)
			}
		}
		if err := s.writePoints(name, ps); err != nil {
			return err
		}
	}
	return nil
}
