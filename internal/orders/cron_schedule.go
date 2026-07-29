package orders

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cronField struct {
	values [60]bool
}

func (f cronField) matches(value int) bool {
	return value >= 0 && value < len(f.values) && f.values[value]
}

// CronSchedule is a validated five-field cron schedule using the grammar
// supported by the order dispatcher.
type CronSchedule struct {
	fields [5]cronField
}

// ParseCronSchedule validates and compiles the cron grammar shared by order
// loading, dispatch, and diagnostics. Supported field forms are "*", "*/N",
// exact integers, and comma-separated combinations of those forms.
func ParseCronSchedule(schedule string) (CronSchedule, error) {
	rawFields := strings.Fields(schedule)
	if len(rawFields) != 5 {
		return CronSchedule{}, fmt.Errorf("invalid cron schedule: want 5 fields, got %d", len(rawFields))
	}
	specs := []struct {
		name     string
		min, max int
	}{
		{name: "minute", min: 0, max: 59},
		{name: "hour", min: 0, max: 23},
		{name: "day-of-month", min: 1, max: 31},
		{name: "month", min: 1, max: 12},
		{name: "day-of-week", min: 0, max: 6},
	}
	var parsed CronSchedule
	for i, spec := range specs {
		field, err := parseCronField(rawFields[i], spec.min, spec.max)
		if err != nil {
			return CronSchedule{}, fmt.Errorf("invalid cron schedule: %s field %q: %w", spec.name, rawFields[i], err)
		}
		parsed.fields[i] = field
	}
	return parsed, nil
}

func parseCronField(raw string, lowerBound, upperBound int) (cronField, error) {
	var field cronField
	for _, part := range strings.Split(raw, ",") {
		switch {
		case part == "*":
			for value := lowerBound; value <= upperBound; value++ {
				field.values[value] = true
			}
		case strings.HasPrefix(part, "*/"):
			step, err := strconv.Atoi(strings.TrimPrefix(part, "*/"))
			if err != nil || step <= 0 {
				return cronField{}, fmt.Errorf("invalid step %q", part)
			}
			for value := lowerBound; value <= upperBound; value++ {
				if value%step == 0 {
					field.values[value] = true
				}
			}
		default:
			value, err := strconv.Atoi(part)
			if err != nil {
				return cronField{}, fmt.Errorf("unsupported value %q", part)
			}
			if value < lowerBound || value > upperBound {
				return cronField{}, fmt.Errorf("value %d outside %d-%d", value, lowerBound, upperBound)
			}
			field.values[value] = true
		}
	}
	return field, nil
}

// Matches reports whether the schedule matches the wall-clock fields of ts.
func (s CronSchedule) Matches(ts time.Time) bool {
	return s.fields[0].matches(ts.Minute()) &&
		s.fields[1].matches(ts.Hour()) &&
		s.fields[2].matches(ts.Day()) &&
		s.fields[3].matches(int(ts.Month())) &&
		s.fields[4].matches(int(ts.Weekday()))
}

// HasCompletedCronOccurrence reports whether a fireable cron wall-clock slot
// occurred after baseline and has fully completed by now. Unlike dispatch
// catch-up, this historical query has no fixed lookback: baseline is its only
// lower bound. The current wall minute remains incomplete through a DST
// fall-back repeat, and repeated wall minutes count as one slot.
func HasCompletedCronOccurrence(order Order, baseline, now time.Time) (bool, error) {
	if baseline.IsZero() {
		return false, fmt.Errorf("cron occurrence baseline is required")
	}
	schedule, err := ParseCronSchedule(order.Schedule)
	if err != nil {
		return false, err
	}
	loc, err := resolveOrderLocation(order, now)
	if err != nil {
		return false, err
	}
	baseline = baseline.In(loc)
	now = now.In(loc)
	if now.Before(baseline) {
		return false, nil
	}

	baselineWallMinute := baseline.Format(wallMinuteLayout)
	currentWallMinute := now.Format(wallMinuteLayout)
	start := baseline.Truncate(time.Minute).Add(time.Minute)
	prev := start.Add(-time.Minute)
	for ts := start; !ts.After(now); ts = ts.Add(time.Minute) {
		_, previousOffset := prev.Zone()
		_, currentOffset := ts.Zone()
		if currentOffset > previousOffset && matchesInWallGap(schedule.Matches, prev, ts) {
			if ts.Format(wallMinuteLayout) != currentWallMinute {
				return true, nil
			}
		}
		if schedule.Matches(ts) {
			wallMinute := ts.Format(wallMinuteLayout)
			if wallMinute != baselineWallMinute &&
				wallMinute != currentWallMinute &&
				!wallMinuteOccursAgain(wallMinute, now) {
				return true, nil
			}
		}
		prev = ts
	}
	return false, nil
}

func wallMinuteOccursAgain(wallMinute string, now time.Time) bool {
	const wallDateLayout = "2006-01-02"
	targetDate := wallMinute[:len(wallDateLayout)]
	if now.Format(wallDateLayout) != targetDate {
		return false
	}
	for candidate := now.Truncate(time.Minute).Add(time.Minute); ; candidate = candidate.Add(time.Minute) {
		candidateDate := candidate.Format(wallDateLayout)
		if candidateDate > targetDate {
			return false
		}
		if candidate.Format(wallMinuteLayout) == wallMinute {
			return true
		}
	}
}
