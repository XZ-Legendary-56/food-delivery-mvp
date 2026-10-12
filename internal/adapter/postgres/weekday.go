package postgres

import "time"

// isoWeekday converts a Go time.Weekday (Sunday=0..Saturday=6) to the
// Monday=0..Sunday=6 convention used by venue_schedules.weekday and the
// public API (api/openapi/public.yaml VenueScheduleEntry.weekday: "0 =
// Monday .. 6 = Sunday"). Go's stdlib and this project's wire format
// disagree on which day is zero, so every read/write of the weekday column
// goes through this pair of functions — never a raw int cast.
func isoWeekday(w time.Weekday) int {
	return (int(w) + 6) % 7
}

// goWeekday is isoWeekday's inverse.
func goWeekday(iso int) time.Weekday {
	return time.Weekday((iso + 1) % 7)
}
