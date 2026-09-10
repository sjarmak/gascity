//go:build (linux && !android) || (darwin && !ios)

package productmetrics

import (
	"context"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// The lock deadline must still drop an event when the injected decision clock
// is frozen. This isolates the early-drop path from spawn policy assertions.
func TestRecordOnceLockDeadlineWithFrozenDecisionClock(t *testing.T) {
	for _, tc := range []struct {
		expire   bool
		injected bool
	}{{false, false}, {true, false}, {true, true}} {
		synctest.Test(t, func(t *testing.T) {
			home, service, permit := newRecordServiceFixture(t, testEventIDOne)
			// Exercise the constructor's production default, not the fixture override.
			deps := service.deps
			deps.newRecordLockContext = nil
			service = mustOpenTestService(t, deps)
			if tc.injected {
				service.deps.newRecordLockContext = func(parent context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
					if budget != defaultRecordDecisionBudget {
						t.Fatalf("lock budget = %v", budget)
					}
					return context.WithCancel(parent)
				}
			}
			injected := false
			service.deps.storageHooks.beforeMetadataAttempt = func(path string) error {
				if path == filepath.Join(home.Root(), stateLockName) && !injected {
					injected = true
					// Advance the timer under test, not elapsed host time.
					if tc.expire {
						<-time.After(defaultRecordDecisionBudget)
					}
				}
				return nil
			}
			want := RecordStored
			if tc.expire && !tc.injected {
				want = RecordDropped
			}
			if got := service.RecordOnce(permit, CommandHelp); got != want {
				t.Fatalf("case=%+v: record = %v, want %v", tc, got, want)
			}
			if !injected {
				t.Fatal("state-lock metadata boundary was not reached")
			}
			if got := service.deps.now(); !got.Equal(testRecordHour) {
				t.Fatalf("decision clock moved: %v", got)
			}
		})
	}
}
