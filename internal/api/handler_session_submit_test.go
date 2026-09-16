package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/nudgequeue"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

func TestHandleSessionSubmitDefaultsToProviderDefaultBehavior(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Submit Me")
	mgr := session.NewManagerWithOptions(fs.cityBeadStore, fs.sp)
	if err := mgr.Suspend(info.ID); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var accepted asyncAcceptedBody
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if accepted.RequestID == "" {
		t.Fatal("missing request_id")
	}

	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success == nil {
		t.Fatalf("session submit failed: %s: %s", failure.ErrorCode, failure.ErrorMessage)
	}
	// Default intent on a suspended session resumes immediately (not queued).
	if success.Queued {
		t.Fatalf("queued = true, want false (default intent resumes)")
	}
	if success.Intent != string(session.SubmitIntentDefault) {
		t.Fatalf("intent = %q, want %q", success.Intent, session.SubmitIntentDefault)
	}
}

func TestHandleSessionSubmitUsesImmediateDefaultForCodex(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	mgr := session.NewManagerWithOptions(fs.cityBeadStore, fs.sp)
	info, err := mgr.CreateSession(context.Background(), session.CreateOptions{Template: "helper", Title: "Codex Submit", Command: "codex", WorkDir: t.TempDir(), Provider: "codex", Env: nil, Resume: session.ProviderResume{}, Hints: runtime.Config{}, ExtraMeta: map[string]string{"session_origin": "manual"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mgr.Suspend(info.ID); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var accepted asyncAcceptedBody
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if accepted.RequestID == "" {
		t.Fatal("missing request_id")
	}

	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success == nil {
		t.Fatalf("session submit failed: %s: %s", failure.ErrorCode, failure.ErrorMessage)
	}
}

// TestHandleSessionSubmitDeliveredUnobservedReportsSuccessNotFailure covers
// DEFECT 1 from dr-3msk6.1 / gc-7jjy53: gascity-pl observed `gc session
// submit` return submit_failed carrying the exact tmux confirmation-heuristic
// text, while `gc session peek` on the same session showed it already
// RUNNING the submitted command -- a delivery that landed was reported as
// failed. This exercises the real HTTP handler end to end: a provider that
// proves delivery (composer drained) but never observes the busy indicator
// must be reported as submit_succeeded, not submit_failed. On the unfixed
// handler this fails because submitMessageToSession treats any non-nil error
// -- including runtime.ErrNudgeSubmitDeliveredUnobserved -- as submit_failed.
func TestHandleSessionSubmitDeliveredUnobservedReportsSuccessNotFailure(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Delivered Unobserved")
	mgr := session.NewManagerWithOptions(fs.cityBeadStore, fs.sp)
	if err := mgr.Suspend(info.ID); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	fs.sp.NudgeErrors = map[string]error{
		info.SessionName: runtime.ErrNudgeSubmitDeliveredUnobserved,
	}

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var accepted asyncAcceptedBody
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}

	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success == nil {
		t.Fatalf("a proven-delivered submit (composer drained, busy state unobserved) must report success, got failure: code=%q message=%q", failure.ErrorCode, failure.ErrorMessage)
	}
}

// TestHandleSessionSubmitUnconfirmedWithNoActivityReportsUnknownNotFailed
// covers the "unknown" leg of DEFECT 1: when a submit's delivery could not
// be confirmed AND reconciliation against the session's own activity found
// nothing either, the bead requires this be reported distinctly from a
// confirmed failure so a caller keyed on submit_failed does not retry it
// blindly and duplicate the instruction. On the unfixed handler this fails
// because every non-nil submitErr is reported as submit_failed.
func TestHandleSessionSubmitUnconfirmedWithNoActivityReportsUnknownNotFailed(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Unconfirmed No Activity")
	mgr := session.NewManagerWithOptions(fs.cityBeadStore, fs.sp)
	if err := mgr.Suspend(info.ID); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	fs.sp.NudgeErrors = map[string]error{
		info.SessionName: runtime.ErrNudgeSubmitUnconfirmed,
	}
	// No SetActivity call: the session shows no activity at all, so
	// reconciliation cannot prove delivery either.

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var accepted asyncAcceptedBody
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}

	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success != nil {
		t.Fatal("an unreconciled unconfirmed submit reported success; want an unknown-outcome failure so a caller does not treat it as proven delivery")
	}
	if failure.ErrorCode == "submit_failed" {
		t.Fatalf("errorCode = %q, want anything but submit_failed: a caller that retries on submit_failed would duplicate a message that may already have landed (dr-3msk6.1)", failure.ErrorCode)
	}
	if failure.ErrorCode != "submit_unknown" {
		t.Fatalf("errorCode = %q, want %q", failure.ErrorCode, "submit_unknown")
	}
}

// TestHandleSessionSubmitUnconfirmedReconciledByActivityReportsSuccess is the
// exact dr-3msk6.1 scenario: the busy-indicator observation missed, but the
// session moved (peek showed it running the submitted command). Reconciling
// against the session's own activity must resolve this to success, not
// submit_failed and not submit_unknown.
func TestHandleSessionSubmitUnconfirmedReconciledByActivityReportsSuccess(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Unconfirmed Reconciled")
	mgr := session.NewManagerWithOptions(fs.cityBeadStore, fs.sp)
	if err := mgr.Suspend(info.ID); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	fs.sp.NudgeErrors = map[string]error{
		info.SessionName: runtime.ErrNudgeSubmitUnconfirmed,
	}
	// The session shows activity after the send attempt -- proof the
	// message landed, matching what `gc session peek` observed in the
	// incident (the submitted command was RUNNING).
	fs.sp.SetActivity(info.SessionName, time.Now().Add(time.Hour))

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var accepted asyncAcceptedBody
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}

	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success == nil {
		t.Fatalf("a submit reconciled against real session activity must report success, got failure: code=%q message=%q", failure.ErrorCode, failure.ErrorMessage)
	}
}

func TestHandleSessionSubmitFollowUpQueuesMessage(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Queue Me")

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"later please","intent":"follow_up"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var accepted asyncAcceptedBody
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if accepted.RequestID == "" {
		t.Fatal("missing request_id")
	}

	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success == nil {
		t.Fatalf("session submit failed: %s: %s", failure.ErrorCode, failure.ErrorMessage)
	}

	state, err := nudgequeue.LoadState(fs.cityPath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Pending) != 1 {
		t.Fatalf("pending queued submits = %d, want 1", len(state.Pending))
	}
	item := state.Pending[0]
	if item.SessionID != info.ID {
		t.Fatalf("SessionID = %q, want %q", item.SessionID, info.ID)
	}
	if item.Message != "later please" {
		t.Fatalf("Message = %q, want %q", item.Message, "later please")
	}
}

func TestHandleSessionGetIncludesSubmissionCapabilities(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Capabilities")
	if err := fs.cityBeadStore.Update(info.ID, beads.UpdateOpts{
		Metadata: map[string]string{
			"pool_managed": "true",
			"pool_slot":    "1",
		},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", cityURL(fs, "/session/")+info.ID, nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp sessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.SubmissionCapabilities.SupportsFollowUp {
		t.Fatal("SupportsFollowUp = false, want true")
	}
	if !resp.SubmissionCapabilities.SupportsInterruptNow {
		t.Fatal("SupportsInterruptNow = false, want true")
	}
}

func TestHandleSessionStopUsesSoftEscapeForCodex(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	mgr := session.NewManagerWithOptions(fs.cityBeadStore, fs.sp)
	info, err := mgr.CreateSession(context.Background(), session.CreateOptions{Template: "helper", Title: "Codex", Command: "codex", WorkDir: t.TempDir(), Provider: "codex", Env: nil, Resume: session.ProviderResume{}, Hints: runtime.Config{}, ExtraMeta: map[string]string{"session_origin": "manual"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := fs.cityBeadStore.Update(info.ID, beads.UpdateOpts{
		Metadata: map[string]string{"pool_managed": "true"},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	rec := httptest.NewRecorder()
	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/stop", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var sawEscape, sawInterrupt bool
	for _, call := range fs.sp.Calls {
		if call.Method == "SendKeys" && call.Name == info.SessionName && call.Message == "Escape" {
			sawEscape = true
		}
		if call.Method == "Interrupt" && call.Name == info.SessionName {
			sawInterrupt = true
		}
	}
	if !sawEscape {
		t.Fatalf("calls = %#v, want SendKeys(Escape)", fs.sp.Calls)
	}
	if sawInterrupt {
		t.Fatalf("calls = %#v, did not want Interrupt for codex stop", fs.sp.Calls)
	}
}
