package jobs

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed five-field cron expression:
// minute hour day-of-month month day-of-week.
type Schedule struct {
	minute uint64
	hour   uint64
	dom    uint64
	month  uint64
	dow    uint64

	domAll bool
	dowAll bool

	expr string
}

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var dayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// ParseSchedule parses a standard five-field cron expression. Fields accept
// "*", numbers, comma lists, ranges, and "/step" suffixes. Month and weekday
// names are also accepted.
func ParseSchedule(expr string) (Schedule, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("schedule %q: want 5 fields (minute hour day-of-month month day-of-week)", expr)
	}
	var s Schedule
	var err error
	if s.minute, err = parseField(fields[0], 0, 59, nil); err != nil {
		return Schedule{}, fmt.Errorf("schedule %q: minute: %w", expr, err)
	}
	if s.hour, err = parseField(fields[1], 0, 23, nil); err != nil {
		return Schedule{}, fmt.Errorf("schedule %q: hour: %w", expr, err)
	}
	if s.dom, err = parseField(fields[2], 1, 31, nil); err != nil {
		return Schedule{}, fmt.Errorf("schedule %q: day-of-month: %w", expr, err)
	}
	if s.month, err = parseField(fields[3], 1, 12, monthNames); err != nil {
		return Schedule{}, fmt.Errorf("schedule %q: month: %w", expr, err)
	}
	if s.dow, err = parseField(fields[4], 0, 7, dayNames); err != nil {
		return Schedule{}, fmt.Errorf("schedule %q: day-of-week: %w", expr, err)
	}
	if s.dow&(1<<7) != 0 {
		s.dow |= 1
		s.dow &^= 1 << 7
	}
	s.domAll = strings.TrimSpace(fields[2]) == "*"
	s.dowAll = strings.TrimSpace(fields[4]) == "*"
	s.expr = strings.Join(fields, " ")
	return s, nil
}

func parseField(field string, min, max int, names map[string]int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, fmt.Errorf("empty list entry")
		}
		step := 1
		base := part
		if idx := strings.Index(part, "/"); idx >= 0 {
			base = part[:idx]
			n, err := strconv.Atoi(strings.TrimSpace(part[idx+1:]))
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("invalid step %q", part[idx+1:])
			}
			step = n
		}
		lo, hi := min, max
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			parts := strings.SplitN(base, "-", 2)
			var err error
			lo, err = fieldValue(parts[0], names)
			if err != nil {
				return 0, err
			}
			hi, err = fieldValue(parts[1], names)
			if err != nil {
				return 0, err
			}
		default:
			v, err := fieldValue(base, names)
			if err != nil {
				return 0, err
			}
			lo, hi = v, v
		}
		if lo < min || hi > max || lo > hi {
			return 0, fmt.Errorf("value %q out of range %d-%d", base, min, max)
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

func fieldValue(s string, names map[string]int) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if names != nil {
		if v, ok := names[s]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", s)
	}
	return v, nil
}

func (s Schedule) bit(bits uint64, v int) bool {
	return bits&(1<<uint(v)) != 0
}

// Next returns the next time after t that matches the schedule in t's
// location, or the zero time if none is found within five years.
func (s Schedule) Next(after time.Time) time.Time {
	if s.expr == "" {
		return time.Time{}
	}
	loc := after.Location()
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		if !s.bit(s.month, int(t.Month())) {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.bit(s.hour, t.Hour()) {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			continue
		}
		if !s.bit(s.minute, t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func (s Schedule) dayMatches(t time.Time) bool {
	dom := s.bit(s.dom, t.Day())
	dow := s.bit(s.dow, int(t.Weekday()))
	switch {
	case s.domAll && s.dowAll:
		return true
	case s.domAll:
		return dow
	case s.dowAll:
		return dom
	default:
		return dom || dow
	}
}

// String returns the normalized expression.
func (s Schedule) String() string { return s.expr }
