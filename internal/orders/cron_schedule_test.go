package orders

import (
	"strings"
	"testing"
	"time"
)

func TestValidateCronRejectsUnsupportedRange(t *testing.T) {
	order := Order{
		Name:     "business-hours",
		Exec:     "true",
		Trigger:  "cron",
		Schedule: "0 9-17 * * *",
	}

	err := Validate(order)

	if err == nil {
		t.Fatal("Validate accepted a range the scheduler cannot evaluate")
	}
	if !strings.Contains(err.Error(), "hour") {
		t.Fatalf("Validate error = %q, want hour-field context", err)
	}
}

func TestCronScheduleMatchesSupportedGrammar(t *testing.T) {
	schedule, err := ParseCronSchedule("*/15 1,3 * * 1,3,5")
	if err != nil {
		t.Fatalf("ParseCronSchedule: %v", err)
	}
	if !schedule.Matches(time.Date(2026, 7, 29, 3, 30, 0, 0, time.UTC)) {
		t.Fatal("supported step and comma grammar did not match")
	}
	if schedule.Matches(time.Date(2026, 7, 29, 2, 30, 0, 0, time.UTC)) {
		t.Fatal("schedule matched an hour outside its comma list")
	}
}

func TestCheckTriggerCronRejectsUnsupportedRange(t *testing.T) {
	order := Order{
		Name:     "business-hours",
		Trigger:  "cron",
		Schedule: "0 9-17 * * *",
	}

	result := CheckTrigger(order, time.Now(), neverRan, nil, nil)

	if result.Due {
		t.Fatal("range schedule reported due")
	}
	if !strings.Contains(result.Reason, "bad cron schedule") {
		t.Fatalf("reason = %q, want bad cron schedule", result.Reason)
	}
}
