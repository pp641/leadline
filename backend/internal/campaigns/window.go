package campaigns

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// Window is a campaign's sending window in its own timezone.
type Window struct {
	Location *time.Location
	Start    time.Duration // offset from midnight, e.g. 9h
	End      time.Duration // exclusive
	Days     []int         // ISO weekdays, 1 = Monday … 7 = Sunday
}

// ParseClock parses "HH:MM" into an offset from midnight.
func ParseClock(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q, expected HH:MM", s)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

func NewWindow(tz, start, end string, days []int) (Window, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return Window{}, fmt.Errorf("invalid timezone %q", tz)
	}
	s, err := ParseClock(start)
	if err != nil {
		return Window{}, err
	}
	e, err := ParseClock(end)
	if err != nil {
		return Window{}, err
	}
	if e <= s {
		return Window{}, fmt.Errorf("send window end must be after start")
	}
	return Window{Location: loc, Start: s, End: e, Days: days}, nil
}

func isoWeekday(t time.Time) int {
	if wd := int(t.Weekday()); wd != 0 {
		return wd
	}
	return 7
}

// Open reports whether now falls inside the window.
func (w Window) Open(now time.Time) bool {
	local := now.In(w.Location)
	if !slices.Contains(w.Days, isoWeekday(local)) {
		return false
	}
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, w.Location)
	off := local.Sub(midnight)
	return off >= w.Start && off < w.End
}

// Length is the duration of the window on a sending day.
func (w Window) Length() time.Duration { return w.End - w.Start }

// PerTick spreads a daily limit evenly across the window so sends trickle
// out instead of bursting at the start of the day.
func PerTick(dailyLimit int, window, tick time.Duration) int {
	if dailyLimit <= 0 || window <= 0 {
		return 0
	}
	n := int(math.Ceil(float64(dailyLimit) * float64(tick) / float64(window)))
	return max(1, n)
}
