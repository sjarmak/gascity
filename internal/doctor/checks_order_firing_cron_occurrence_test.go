package doctor

import (
	"fmt"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/orders"
)

func TestOrderFiringCurrent_TwoWindowCronOvernightGapIsCurrent(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	now := time.Date(2026, 7, 29, 5, 30, 0, 0, loc)
	cityPath, cfg := orderFiringTestCity(t)
	writeOrderFiringRawOrder(t, cityPath, "twice-daily", `[order]
exec = "true"
trigger = "cron"
schedule = "0 10,17 * * *"
tz = "America/New_York"
`)
	writeOrderFiringTestEvents(t, cityPath,
		events.Event{Type: events.ControllerStarted, Ts: now.Add(-24 * time.Hour)},
		events.Event{Type: events.OrderFired, Subject: "twice-daily", Ts: time.Date(2026, 7, 28, 17, 0, 0, 0, loc)},
	)
	lastRunCalled := false
	check := NewOrderFiringCurrentCheck(cfg, cityPath, WithOrderFiringCurrentLastRunFunc(func(orders.Order) (time.Time, error) {
		lastRunCalled = true
		return time.Time{}, fmt.Errorf("history should not be queried before the next cron window")
	}))
	check.clock = func() time.Time { return now }

	result := check.Run(&CheckContext{CityPath: cityPath})

	if result.Status != StatusOK {
		t.Fatalf("status = %v, want OK during overnight gap; msg = %s; details = %v", result.Status, result.Message, result.Details)
	}
	if lastRunCalled {
		t.Fatal("last-run history queried during a current overnight cron gap")
	}
}

func TestOrderFiringCurrent_TwoWindowCronMissedWindowIsOverdue(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	now := time.Date(2026, 7, 29, 17, 5, 0, 0, loc)
	cityPath, cfg := orderFiringTestCity(t)
	writeOrderFiringRawOrder(t, cityPath, "twice-daily", `[order]
exec = "true"
trigger = "cron"
schedule = "0 10,17 * * *"
tz = "America/New_York"
`)
	writeOrderFiringTestEvents(t, cityPath,
		events.Event{Type: events.ControllerStarted, Ts: now.Add(-24 * time.Hour)},
		events.Event{Type: events.OrderFired, Subject: "twice-daily", Ts: time.Date(2026, 7, 29, 10, 0, 0, 0, loc)},
	)

	result := runOrderFiringCurrentTest(t, cfg, cityPath, now)

	if result.Status != StatusWarning {
		t.Fatalf("status = %v, want warning after missed 17:00 window; msg = %s; details = %v", result.Status, result.Message, result.Details)
	}
}

func TestOrderFiringCurrent_SingleWindowCronUsesCompletedOccurrences(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	tests := []struct {
		name       string
		now        time.Time
		wantStatus CheckStatus
	}{
		{
			name:       "before next window",
			now:        time.Date(2026, 7, 29, 2, 0, 0, 0, loc),
			wantStatus: StatusOK,
		},
		{
			name:       "after missed window",
			now:        time.Date(2026, 7, 29, 3, 5, 0, 0, loc),
			wantStatus: StatusWarning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cityPath, cfg := orderFiringTestCity(t)
			writeOrderFiringRawOrder(t, cityPath, "daily", `[order]
exec = "true"
trigger = "cron"
schedule = "0 3 * * *"
tz = "America/New_York"
`)
			writeOrderFiringTestEvents(t, cityPath,
				events.Event{Type: events.ControllerStarted, Ts: tt.now.Add(-48 * time.Hour)},
				events.Event{Type: events.OrderFired, Subject: "daily", Ts: time.Date(2026, 7, 28, 3, 0, 0, 0, loc)},
			)

			result := runOrderFiringCurrentTest(t, cfg, cityPath, tt.now)

			if result.Status != tt.wantStatus {
				t.Fatalf("status = %v, want %v; msg = %s; details = %v", result.Status, tt.wantStatus, result.Message, result.Details)
			}
		})
	}
}

func TestOrderFiringCurrent_CronDSTSemanticsMatchDispatcher(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	tests := []struct {
		name       string
		schedule   string
		now        time.Time
		lastFired  time.Time
		wantStatus CheckStatus
	}{
		{
			name:       "spring-forward gap is a missed occurrence",
			schedule:   "30 2 * * *",
			now:        time.Date(2027, 3, 14, 3, 5, 0, 0, loc),
			lastFired:  time.Date(2027, 3, 13, 2, 30, 0, 0, loc),
			wantStatus: StatusWarning,
		},
		{
			name:       "fall-back repeat is the same wall-clock slot",
			schedule:   "30 1 * * *",
			now:        time.Date(2026, 11, 1, 6, 45, 0, 0, time.UTC).In(loc),
			lastFired:  time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC),
			wantStatus: StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cityPath, cfg := orderFiringTestCity(t)
			writeOrderFiringRawOrder(t, cityPath, "dst-order", `[order]
exec = "true"
trigger = "cron"
schedule = "`+tt.schedule+`"
tz = "America/New_York"
`)
			writeOrderFiringTestEvents(t, cityPath,
				events.Event{Type: events.ControllerStarted, Ts: tt.now.Add(-48 * time.Hour)},
				events.Event{Type: events.OrderFired, Subject: "dst-order", Ts: tt.lastFired},
			)

			result := runOrderFiringCurrentTest(t, cfg, cityPath, tt.now)

			if result.Status != tt.wantStatus {
				t.Fatalf("status = %v, want %v; msg = %s; details = %v", result.Status, tt.wantStatus, result.Message, result.Details)
			}
		})
	}
}
