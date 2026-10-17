package icu

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Object is one API object as returned, so --json passes every field through.
type Object = map[string]any

// Num reads a numeric field.
func Num(o Object, key string) (float64, bool) {
	switch v := o[key].(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case int:
		return float64(v), true
	}
	return 0, false
}

// Str reads a string field ("" when absent or not a string).
func Str(o Object, key string) string {
	switch v := o[key].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

// Athlete fetches the athlete with sport settings.
func (c *Client) Athlete(ctx context.Context) (Object, error) {
	var out Object
	err := c.Get(ctx, "/athlete/{athlete}", nil, &out)
	return out, err
}

// Activities lists completed activities between two local dates (inclusive).
func (c *Client) Activities(ctx context.Context, oldest, newest string, fields []string) ([]Object, error) {
	q := url.Values{"oldest": {oldest}, "newest": {newest}}
	if len(fields) > 0 {
		q.Set("fields", strings.Join(withID(fields), ","))
	}
	var out []Object
	if err := c.Get(ctx, "/athlete/{athlete}/activities", q, &out); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return Str(out[i], "start_date_local") < Str(out[j], "start_date_local") })
	return out, nil
}

func withID(fields []string) []string {
	for _, f := range fields {
		if f == "id" {
			return fields
		}
	}
	return append([]string{"id"}, fields...)
}

// Activity fetches one activity, with its intervals when asked.
func (c *Client) Activity(ctx context.Context, id string, intervals bool) (Object, error) {
	q := url.Values{}
	if intervals {
		q.Set("intervals", "true")
	}
	var out Object
	err := c.Get(ctx, "/activity/"+url.PathEscape(id), q, &out)
	return out, err
}

// Stream is one per-sample channel of an activity.
type Stream struct {
	Type string    `json:"type"`
	Data []float64 `json:"data"`
}

// Streams fetches per-sample data. Missing samples (null) become NaN.
func (c *Client) Streams(ctx context.Context, id string, types []string) ([]Stream, error) {
	q := url.Values{}
	if len(types) > 0 {
		q.Set("types", strings.Join(types, ","))
	}
	var raw []struct {
		Type string     `json:"type"`
		Data []*float64 `json:"data"`
	}
	if err := c.Get(ctx, "/activity/"+url.PathEscape(id)+"/streams.json", q, &raw); err != nil {
		return nil, err
	}
	out := make([]Stream, 0, len(raw))
	for _, r := range raw {
		s := Stream{Type: r.Type, Data: make([]float64, len(r.Data))}
		for i, v := range r.Data {
			if v == nil {
				s.Data[i] = math.NaN()
			} else {
				s.Data[i] = *v
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// Wellness lists daily wellness records between two dates; each record's id
// is its date.
func (c *Client) Wellness(ctx context.Context, oldest, newest string) ([]Object, error) {
	q := url.Values{"oldest": {oldest}, "newest": {newest}}
	var out []Object
	if err := c.Get(ctx, "/athlete/{athlete}/wellness.json", q, &out); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return Str(out[i], "id") < Str(out[j], "id") })
	return out, nil
}

// Events lists calendar events (planned workouts, notes, races) between two
// dates, optionally limited to categories such as WORKOUT or RACE_A. resolve
// turns zone and percentage targets into watts, bpm and m/s.
func (c *Client) Events(ctx context.Context, oldest, newest string, categories []string, resolve bool) ([]Object, error) {
	q := url.Values{"oldest": {oldest}, "newest": {newest}}
	if resolve {
		q.Set("resolve", "true")
	}
	if len(categories) > 0 {
		q.Set("category", strings.Join(categories, ","))
	}
	var out []Object
	if err := c.Get(ctx, "/athlete/{athlete}/events.json", q, &out); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return Str(out[i], "start_date_local") < Str(out[j], "start_date_local") })
	return out, nil
}

// Event fetches one calendar event.
func (c *Client) Event(ctx context.Context, id string) (Object, error) {
	var out Object
	err := c.Get(ctx, "/athlete/{athlete}/events/"+url.PathEscape(id), nil, &out)
	return out, err
}

// Summary returns one row per week (the row's date is the week start) with
// totals and the fitness model at the end of the week.
func (c *Client) Summary(ctx context.Context, start, end string) ([]Object, error) {
	q := url.Values{"start": {start}, "end": {end}}
	var out []Object
	if err := c.Get(ctx, "/athlete/{athlete}/athlete-summary.json", q, &out); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return Str(out[i], "date") < Str(out[j], "date") })
	return out, nil
}

// CreateEvent posts a calendar event. With a uid, an existing event carrying
// the same uid is updated instead, so a re-push does not duplicate.
func (c *Client) CreateEvent(ctx context.Context, event Object) (Object, error) {
	q := url.Values{"upsertOnUid": {"false"}}
	if Str(event, "uid") != "" {
		q.Set("upsertOnUid", "true")
	}
	var out Object
	err := c.Send(ctx, http.MethodPost, "/athlete/{athlete}/events", q, event, &out)
	return out, err
}

// UpdateEvent changes the given fields of an event.
func (c *Client) UpdateEvent(ctx context.Context, id string, fields Object) (Object, error) {
	var out Object
	err := c.Send(ctx, http.MethodPut, "/athlete/{athlete}/events/"+url.PathEscape(id), nil, fields, &out)
	return out, err
}

// DeleteEvent removes one event.
func (c *Client) DeleteEvent(ctx context.Context, id string) error {
	return c.Send(ctx, http.MethodDelete, "/athlete/{athlete}/events/"+url.PathEscape(id), nil, nil, nil)
}

// TodayIn is the current date in an IANA zone, falling back to fallback when
// the zone is unknown.
func TodayIn(now time.Time, zone string, fallback *time.Location) (time.Time, *time.Location) {
	loc := fallback
	if zone != "" {
		if l, err := time.LoadLocation(zone); err == nil {
			loc = l
		}
	}
	return now.In(loc), loc
}

// Pace renders metres per second as min:ss per km (or per 100 m for swims).
func Pace(mps float64, per100m bool) string {
	if mps <= 0 || math.IsNaN(mps) {
		return ""
	}
	secs := 1000 / mps
	unit := "/km"
	if per100m {
		secs, unit = 100/mps, "/100m"
	}
	s := int(math.Round(secs))
	return fmt.Sprintf("%d:%02d%s", s/60, s%60, unit)
}

// Duration renders seconds as h:mm:ss.
func Duration(secs float64) string {
	if secs <= 0 || math.IsNaN(secs) {
		return ""
	}
	s := int(math.Round(secs))
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}
