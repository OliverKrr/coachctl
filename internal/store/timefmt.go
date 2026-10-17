package store

import (
	"fmt"
	"regexp"
	"time"
)

const (
	dateLayout = "2006-01-02"
	// stampLayout is how the journal writes a moment: minute precision plus
	// the UTC offset, so an entry reads naturally and still converts exactly.
	stampLayout = "2006-01-02 15:04 -07:00"
	// minuteLayout is a local wall-clock time without offset (metrics).
	minuteLayout = "2006-01-02 15:04"
)

var (
	dateRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	dateTimeRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$`)
)

// ParseDate validates a YYYY-MM-DD calendar date.
func ParseDate(s string) (string, error) {
	if !dateRe.MatchString(s) {
		return "", usagef("date %q must be YYYY-MM-DD", s)
	}
	if _, err := time.Parse(dateLayout, s); err != nil {
		return "", usagef("date %q is not a calendar date", s)
	}
	return s, nil
}

// parseMeasuredAt accepts YYYY-MM-DD or "YYYY-MM-DD HH:MM" (local wall clock).
func parseMeasuredAt(s string) (string, error) {
	if dateRe.MatchString(s) {
		return ParseDate(s)
	}
	if dateTimeRe.MatchString(s) {
		if _, err := time.Parse(minuteLayout, s); err != nil {
			return "", usagef("time %q is not a valid date and time", s)
		}
		return s, nil
	}
	return "", usagef("time %q must be YYYY-MM-DD or \"YYYY-MM-DD HH:MM\"", s)
}

// sortKey makes date-only and date-time values comparable as strings: a
// date-only value sorts at the start of its day.
func sortKey(measuredAt string) string {
	if len(measuredAt) == len(dateLayout) {
		return measuredAt + " 00:00"
	}
	return measuredAt
}

// formatStamp renders a journal timestamp in the record's zone.
func (s *Store) formatStamp(t time.Time) string { return t.In(s.Loc).Format(stampLayout) }

func parseStamp(v string) (time.Time, error) {
	t, err := time.Parse(stampLayout, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q: %w", v, err)
	}
	return t, nil
}
