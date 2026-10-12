package catalog

import "time"

// ScheduleEntry is one weekday's opening window (docs/PLAN.md 9:
// venue_schedules). MVP simplification, matching the
// venue_schedules_order CHECK in migrations/00003_venues.sql: one interval
// per weekday, no shift crossing midnight.
type ScheduleEntry struct {
	Weekday  time.Weekday
	OpensAt  time.Duration // time-of-day offset from midnight
	ClosesAt time.Duration
}

// IsOpenAt reports whether entries contains a window covering at, using
// at's weekday and time-of-day in at's own location. The window is
// half-open [OpensAt, ClosesAt): opening time counts as open, closing time
// does not.
func IsOpenAt(entries []ScheduleEntry, at time.Time) bool {
	weekday := at.Weekday()
	sinceMidnight := timeOfDay(at)
	for _, e := range entries {
		if e.Weekday != weekday {
			continue
		}
		if sinceMidnight >= e.OpensAt && sinceMidnight < e.ClosesAt {
			return true
		}
	}
	return false
}

func timeOfDay(t time.Time) time.Duration {
	h, m, s := t.Clock()
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second
}

// NextOpenAfter returns the next moment at or after "after" when entries has
// an opening window starting, or nil if entries is empty (a venue with no
// schedule at all has no known next opening). It looks up to 7 days ahead
// (8 candidates, 0..7): a schedule can have as few as one entry, so when
// today's own window has already passed, the next occurrence is a full
// week later, not sooner — going only to day 6 would miss it.
func NextOpenAfter(entries []ScheduleEntry, after time.Time) *time.Time {
	if len(entries) == 0 {
		return nil
	}

	sinceMidnight := timeOfDay(after)
	for daysAhead := 0; daysAhead <= 7; daysAhead++ {
		weekday := time.Weekday((int(after.Weekday()) + daysAhead) % 7)

		var best *time.Duration
		for _, e := range entries {
			if e.Weekday != weekday {
				continue
			}
			if daysAhead == 0 && e.OpensAt <= sinceMidnight {
				continue // today's window already started or passed
			}
			if best == nil || e.OpensAt < *best {
				opensAt := e.OpensAt
				best = &opensAt
			}
		}
		if best == nil {
			continue
		}

		day := after.AddDate(0, 0, daysAhead)
		result := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location()).Add(*best)
		return &result
	}
	return nil
}
