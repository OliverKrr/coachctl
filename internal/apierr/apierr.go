// Package apierr is the error an external API call returns. The CLI maps it
// to exit code 3, so scripts can tell "the service failed" from "my call was
// wrong" (2) and "coachctl failed" (1); a 400, 404 or 422 counts as a wrong
// call and exits 2.
package apierr

import "fmt"

// Error is a failed request to an external service.
type Error struct {
	Service string // "intervals.icu", "hevy", "telegram"
	Status  int    // HTTP status, 0 for a transport error
	Detail  string // trimmed response body or transport error
	Hint    string // what the caller can do about it
}

func (e *Error) Error() string {
	msg := e.Service + ": "
	if e.Status != 0 {
		msg += fmt.Sprintf("HTTP %d", e.Status)
		if e.Detail != "" {
			msg += ": " + e.Detail
		}
	} else {
		msg += e.Detail
	}
	if e.Hint != "" {
		msg += " (" + e.Hint + ")"
	}
	return msg
}

// Trim shortens a response body for an error message.
func Trim(body []byte) string {
	const max = 300
	s := string(body)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
