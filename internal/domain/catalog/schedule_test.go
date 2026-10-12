package catalog_test

import (
	"food-delivery-mvp/internal/domain/catalog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mondayNineToTen is a schedule open only Monday 09:00-10:00, used to probe
// the boundary conditions of the half-open interval.
var mondayNineToTen = []catalog.ScheduleEntry{
	{Weekday: time.Monday, OpensAt: 9 * time.Hour, ClosesAt: 10 * time.Hour},
}

func at(weekday time.Weekday, hour, minute int) time.Time {
	// 2024-01-01 was a Monday; walk forward to land on the requested weekday.
	base := time.Date(2024, 1, 1, hour, minute, 0, 0, time.UTC)
	offset := (int(weekday) - int(base.Weekday()) + 7) % 7
	return base.AddDate(0, 0, offset)
}

func TestIsOpenAt_WithinWindow(t *testing.T) {
	assert.True(t, catalog.IsOpenAt(mondayNineToTen, at(time.Monday, 9, 30)))
}

func TestIsOpenAt_AtOpeningTimeIsOpen(t *testing.T) {
	assert.True(t, catalog.IsOpenAt(mondayNineToTen, at(time.Monday, 9, 0)))
}

func TestIsOpenAt_AtClosingTimeIsClosed(t *testing.T) {
	assert.False(t, catalog.IsOpenAt(mondayNineToTen, at(time.Monday, 10, 0)),
		"the window is half-open: closing time itself must count as closed")
}

func TestIsOpenAt_BeforeOpening(t *testing.T) {
	assert.False(t, catalog.IsOpenAt(mondayNineToTen, at(time.Monday, 8, 59)))
}

func TestIsOpenAt_WrongWeekday(t *testing.T) {
	assert.False(t, catalog.IsOpenAt(mondayNineToTen, at(time.Tuesday, 9, 30)))
}

func TestIsOpenAt_NoEntries(t *testing.T) {
	assert.False(t, catalog.IsOpenAt(nil, at(time.Monday, 9, 30)))
}

func TestIsOpenAt_MultipleDaysOnlyMatchingOneApplies(t *testing.T) {
	schedule := []catalog.ScheduleEntry{
		{Weekday: time.Monday, OpensAt: 9 * time.Hour, ClosesAt: 17 * time.Hour},
		{Weekday: time.Tuesday, OpensAt: 12 * time.Hour, ClosesAt: 20 * time.Hour},
	}

	assert.True(t, catalog.IsOpenAt(schedule, at(time.Tuesday, 12, 30)))
	assert.False(t, catalog.IsOpenAt(schedule, at(time.Tuesday, 9, 30)), "Monday's hours must not leak into Tuesday")
}

func TestNextOpenAfter_NoEntries(t *testing.T) {
	assert.Nil(t, catalog.NextOpenAfter(nil, at(time.Monday, 9, 30)))
}

func TestNextOpenAfter_LaterTodayIfNotYetOpened(t *testing.T) {
	next := catalog.NextOpenAfter(mondayNineToTen, at(time.Monday, 8, 0))

	require.NotNil(t, next)
	assert.Equal(t, at(time.Monday, 9, 0), *next)
}

func TestNextOpenAfter_NextWeekIfTodaysWindowAlreadyPassed(t *testing.T) {
	next := catalog.NextOpenAfter(mondayNineToTen, at(time.Monday, 11, 0))

	require.NotNil(t, next)
	assert.Equal(t, at(time.Monday, 9, 0).AddDate(0, 0, 7), *next)
}

func TestNextOpenAfter_SkipsToTheNearestFutureDay(t *testing.T) {
	schedule := []catalog.ScheduleEntry{
		{Weekday: time.Wednesday, OpensAt: 9 * time.Hour, ClosesAt: 17 * time.Hour},
	}

	next := catalog.NextOpenAfter(schedule, at(time.Monday, 9, 30))

	require.NotNil(t, next)
	assert.Equal(t, at(time.Wednesday, 9, 0), *next)
}

func TestNextOpenAfter_WhileCurrentlyOpenStillReportsToday(t *testing.T) {
	// A venue open right now has no "next opening" in the everyday sense,
	// but NextOpenAfter is defined purely in terms of "when does a window
	// next start" — callers only use it when IsOpen is already false.
	next := catalog.NextOpenAfter(mondayNineToTen, at(time.Monday, 9, 30))

	require.NotNil(t, next)
	assert.Equal(t, at(time.Monday, 9, 0).AddDate(0, 0, 7), *next)
}
