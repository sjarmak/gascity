package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubExecRecord is one captured invocation by the fake exec.Command
// installed via dispatchExecCommand.
type stubExecRecord struct {
	Name string
	Args []string
	Dir  string
}

// installStubExecCommand replaces dispatchExecCommand with a fake that
// records the invocations and produces deterministic stdout per command
// name. It returns a pointer to the recorded slice and a restore func.
//
// `bd` invocations emit a JSON line containing {"id": bdID}; `gc`
// invocations emit a single OK line. Tests asserting failure modes can
// override behavior by inspecting Name/Args before this stub returns.
func installStubExecCommand(t *testing.T, bdID string, gcExitCode int) (*[]stubExecRecord, func()) {
	t.Helper()
	prev := dispatchExecCommand
	var records []stubExecRecord

	dispatchExecCommand = func(name string, args ...string) *exec.Cmd {
		records = append(records, stubExecRecord{Name: name, Args: append([]string(nil), args...)})

		// Each invocation routes to a tiny shell script that echoes a
		// canned line so the dispatcher can parse JSON from `bd create`
		// and proceed past `gc sling`.
		var script string
		switch name {
		case "bd":
			script = "printf '%s\\n' '" + `{"id":"` + bdID + `","title":"x","type":"task"}` + "'"
		case "gc":
			if gcExitCode != 0 {
				script = "echo gc-fail >&2; exit 1"
			} else {
				script = "echo ok"
			}
		default:
			script = "echo unhandled-stub-cmd: " + name
		}
		c := exec.Command("sh", "-c", script)
		// Caller will set Dir.
		return c
	}
	return &records, func() { dispatchExecCommand = prev }
}

// seedRoutesJSONL writes a routes.jsonl with the supplied prefix→relpath
// pairs (path is relative to cityPath; absolute when starting with /).
// Returns cityPath.
func seedRoutesJSONL(t *testing.T, cityPath string, entries map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(cityPath, ".beads"), 0o700); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	var lines []string
	for prefix, p := range entries {
		b, err := json.Marshal(struct {
			Prefix string `json:"prefix"`
			Path   string `json:"path"`
		}{Prefix: prefix, Path: p})
		if err != nil {
			t.Fatalf("marshal route: %v", err)
		}
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(filepath.Join(cityPath, ".beads", "routes.jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write routes.jsonl: %v", err)
	}
}

// TestSlackInteractionsRigDispatchHappyPathSlash exercises the full
// rig-target dispatch flow on a slash command: ResolveSlingTarget
// returns sling target + fix formula, rigWorkdir resolves, the
// dispatcher invokes `bd create` then `gc sling` with the expected
// arguments and working directories.
func TestSlackInteractionsRigDispatchHappyPathSlash(t *testing.T) {
	cityPath := t.TempDir()
	rigDir := filepath.Join(cityPath, "rigs", "alpha")
	if err := os.MkdirAll(rigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seedRoutesJSONL(t, cityPath, map[string]string{"alpha": "rigs/alpha"})

	cfg := config{
		slackSigningKey: "secret",
		accountID:       "T1",
		cityName:        "test-city",
		cityPath:        cityPath,
	}
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	if err := rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs:  []string{"C1"},
		SlingTarget: "mission-control/polecat",
		FixFormula:  "fix-bug",
		CreatedAt:   now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	records, restore := installStubExecCommand(t, "bd-42", 0)
	t.Cleanup(restore)
	// Hook to wait until both bd + gc fired.
	done := make(chan struct{})
	prevHook := dispatchTestCompletionHook
	dispatchTestCompletionHook = func() { close(done) }
	t.Cleanup(func() { dispatchTestCompletionHook = prevHook })

	body := []byte(url.Values{
		"team_id":    {"T1"},
		"channel_id": {"C1"},
		"command":    {"/gc"},
		"text":       {"deploy now"},
		"user_id":    {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, chanReg, rigReg)(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Routing") || !strings.Contains(rec.Body.String(), "alpha") {
		t.Errorf("ephemeral ack should mention Routing+rig: %s", rec.Body.String())
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch goroutine did not finish within 2s")
	}

	if len(*records) < 2 {
		t.Fatalf("expected >=2 exec calls, got %d: %+v", len(*records), *records)
	}
	bd := (*records)[0]
	if bd.Name != "bd" || len(bd.Args) < 2 || bd.Args[0] != "create" {
		t.Errorf("bd invocation = %+v, want `bd create ...`", bd)
	}
	gc := (*records)[1]
	if gc.Name != "gc" || len(gc.Args) < 4 ||
		gc.Args[0] != "sling" || gc.Args[1] != "mission-control/polecat" || gc.Args[2] != "bd-42" {
		t.Errorf("gc invocation args = %v, want `gc sling mission-control/polecat bd-42 ...`", gc.Args)
	}
	// fix_formula present → expect --on flag with value
	foundOn := false
	for i, a := range gc.Args {
		if a == "--on" && i+1 < len(gc.Args) && gc.Args[i+1] == "fix-bug" {
			foundOn = true
		}
	}
	if !foundOn {
		t.Errorf("gc args should include `--on fix-bug`: %v", gc.Args)
	}
}

// TestSlackInteractionsRigDispatchEmptyFixFormulaOmitsOn — when the
// rig record has no fix_formula, dispatch must NOT pass --on (gc uses
// its own default). Documented choice in cby.18.3.
func TestSlackInteractionsRigDispatchEmptyFixFormulaOmitsOn(t *testing.T) {
	cityPath := t.TempDir()
	seedRoutesJSONL(t, cityPath, map[string]string{"alpha": "."})

	cfg := config{
		slackSigningKey: "secret", accountID: "T1",
		cityName: "test-city", cityPath: cityPath,
	}
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	if err := rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs:  []string{"C1"},
		SlingTarget: "mission-control/polecat",
		// FixFormula intentionally empty
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	records, restore := installStubExecCommand(t, "bd-99", 0)
	t.Cleanup(restore)
	done := make(chan struct{})
	prevHook := dispatchTestCompletionHook
	dispatchTestCompletionHook = func() { close(done) }
	t.Cleanup(func() { dispatchTestCompletionHook = prevHook })

	body := []byte(url.Values{
		"team_id": {"T1"}, "channel_id": {"C1"},
		"command": {"/gc"}, "text": {"hi"}, "user_id": {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, chanReg, rigReg)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch did not finish")
	}
	if len(*records) < 2 {
		t.Fatalf("want >=2 exec records, got %+v", *records)
	}
	gc := (*records)[1]
	for _, a := range gc.Args {
		if a == "--on" {
			t.Errorf("expected no --on flag when fix_formula empty; got args=%v", gc.Args)
		}
	}
}

// TestSlackInteractionsRigDispatchMissingSlingTarget — the rig record
// is present but SlingTarget is empty. Dispatch must surface the
// resolver's fix-it error verbatim as the ephemeral, and not invoke
// any subprocess.
func TestSlackInteractionsRigDispatchMissingSlingTarget(t *testing.T) {
	cityPath := t.TempDir()
	cfg := config{
		slackSigningKey: "secret", accountID: "T1",
		cityName: "test-city", cityPath: cityPath,
	}
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	if err := rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
		// SlingTarget intentionally empty → ResolveSlingTarget errors
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	records, restore := installStubExecCommand(t, "bd-x", 0)
	t.Cleanup(restore)

	body := []byte(url.Values{
		"team_id": {"T1"}, "channel_id": {"C1"},
		"command": {"/gc"}, "text": {"hi"}, "user_id": {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, chanReg, rigReg)(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no sling target") {
		t.Errorf("ephemeral should surface resolver fix-it: %s", rec.Body.String())
	}
	// Allow any spurious goroutine to surface.
	time.Sleep(100 * time.Millisecond)
	if len(*records) != 0 {
		t.Errorf("expected no exec invocations on resolver error; got %+v", *records)
	}
}

// TestSlackInteractionsRigDispatchMissingWorkdir — sling target is
// present but routes.jsonl has no entry for the rig. The ephemeral
// must mention the workdir lookup failure and no subprocess fires.
func TestSlackInteractionsRigDispatchMissingWorkdir(t *testing.T) {
	cityPath := t.TempDir()
	// no routes.jsonl seeded — rigWorkdir errors at file-open.

	cfg := config{
		slackSigningKey: "secret", accountID: "T1",
		cityName: "test-city", cityPath: cityPath,
	}
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	if err := rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs:  []string{"C1"},
		SlingTarget: "mission-control/polecat",
		CreatedAt:   now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	records, restore := installStubExecCommand(t, "bd-x", 0)
	t.Cleanup(restore)

	body := []byte(url.Values{
		"team_id": {"T1"}, "channel_id": {"C1"},
		"command": {"/gc"}, "text": {"hi"}, "user_id": {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, chanReg, rigReg)(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "rig workdir not found") {
		t.Errorf("ephemeral should surface workdir error: %s", rec.Body.String())
	}
	time.Sleep(100 * time.Millisecond)
	if len(*records) != 0 {
		t.Errorf("expected no exec invocations on workdir error; got %+v", *records)
	}
}

// TestSlackInteractionsRigDispatchSaturationDrop — when the dispatch
// semaphore is full at slot-acquire time, the ephemeral must say
// "saturated" and no subprocess fires.
func TestSlackInteractionsRigDispatchSaturationDrop(t *testing.T) {
	cityPath := t.TempDir()
	seedRoutesJSONL(t, cityPath, map[string]string{"alpha": "."})

	cfg := config{
		slackSigningKey: "secret", accountID: "T1",
		cityName: "test-city", cityPath: cityPath,
	}
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	if err := rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs:  []string{"C1"},
		SlingTarget: "mission-control/polecat",
		CreatedAt:   now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	restore := setDispatchSemaphoreForTest(1)
	t.Cleanup(restore)
	holdRelease, _, ok := acquireDispatchSlot()
	if !ok {
		t.Fatal("acquireDispatchSlot: failed to take initial slot")
	}
	t.Cleanup(holdRelease)

	records, restoreExec := installStubExecCommand(t, "bd-x", 0)
	t.Cleanup(restoreExec)

	body := []byte(url.Values{
		"team_id": {"T1"}, "channel_id": {"C1"},
		"command": {"/gc"}, "text": {"hi"}, "user_id": {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, chanReg, rigReg)(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "saturated") {
		t.Errorf("ephemeral should mention 'saturated': %s", rec.Body.String())
	}
	time.Sleep(100 * time.Millisecond)
	if len(*records) != 0 {
		t.Errorf("no exec invocations expected on saturation drop; got %+v", *records)
	}
}

// TestSlackInteractionsRigDispatchGcFailureClosesBead — when `gc sling`
// exits non-zero after `bd create` succeeded, the dispatcher invokes
// `bd close <id> -r dispatch_failed` as best-effort cleanup.
func TestSlackInteractionsRigDispatchGcFailureClosesBead(t *testing.T) {
	cityPath := t.TempDir()
	seedRoutesJSONL(t, cityPath, map[string]string{"alpha": "."})

	cfg := config{
		slackSigningKey: "secret", accountID: "T1",
		cityName: "test-city", cityPath: cityPath,
	}
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	if err := rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs:  []string{"C1"},
		SlingTarget: "mission-control/polecat",
		CreatedAt:   now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	records, restore := installStubExecCommand(t, "bd-77", 1) // gc exit non-zero
	t.Cleanup(restore)
	done := make(chan struct{})
	prevHook := dispatchTestCompletionHook
	dispatchTestCompletionHook = func() { close(done) }
	t.Cleanup(func() { dispatchTestCompletionHook = prevHook })

	body := []byte(url.Values{
		"team_id": {"T1"}, "channel_id": {"C1"},
		"command": {"/gc"}, "text": {"hi"}, "user_id": {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, chanReg, rigReg)(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch goroutine did not finish")
	}
	// Expect 3 invocations: bd create, gc sling, bd close
	if len(*records) < 3 {
		t.Fatalf("want >=3 exec records (bd create, gc sling, bd close), got %d: %+v",
			len(*records), *records)
	}
	last := (*records)[len(*records)-1]
	if last.Name != "bd" || len(last.Args) < 4 || last.Args[0] != "close" || last.Args[1] != "bd-77" {
		t.Errorf("expected `bd close bd-77 -r dispatch_failed`; got %+v", last)
	}
	foundReason := false
	for i, a := range last.Args {
		if a == "-r" && i+1 < len(last.Args) && last.Args[i+1] == "dispatch_failed" {
			foundReason = true
		}
	}
	if !foundReason {
		t.Errorf("expected -r dispatch_failed in close args: %v", last.Args)
	}
}

// TestSlackInteractionsRigDispatchBlockActionsHappyPath exercises the
// rig-target dispatch flow on a block_actions payload. The bead title
// must derive from the action.value (sanitized).
func TestSlackInteractionsRigDispatchBlockActionsHappyPath(t *testing.T) {
	cityPath := t.TempDir()
	seedRoutesJSONL(t, cityPath, map[string]string{"alpha": "."})

	cfg := config{
		slackSigningKey: "secret", accountID: "T1",
		cityName: "test-city", cityPath: cityPath,
	}
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	if err := rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs:  []string{"C-RIG"},
		SlingTarget: "mission-control/polecat",
		FixFormula:  "fix-it",
		CreatedAt:   now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	records, restore := installStubExecCommand(t, "bd-101", 0)
	t.Cleanup(restore)
	done := make(chan struct{})
	prevHook := dispatchTestCompletionHook
	dispatchTestCompletionHook = func() { close(done) }
	t.Cleanup(func() { dispatchTestCompletionHook = prevHook })

	payload := blockActionsPayloadJSON(t, "T1", "U1", "C-RIG", "", "",
		[]map[string]string{{"action_id": "approve", "value": "shipit", "type": "button"}})
	body := []byte(url.Values{"payload": {payload}}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, chanReg, rigReg)(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Routing") || !strings.Contains(rec.Body.String(), "alpha") {
		t.Errorf("ephemeral should mention Routing+rig: %s", rec.Body.String())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("block_actions dispatch did not finish")
	}
	if len(*records) < 2 {
		t.Fatalf("expected >=2 exec calls; got %+v", *records)
	}
	bd := (*records)[0]
	if bd.Name != "bd" || bd.Args[0] != "create" {
		t.Errorf("first call should be `bd create`; got %+v", bd)
	}
	titleSeen := false
	for _, a := range bd.Args {
		if strings.Contains(a, "shipit") {
			titleSeen = true
		}
	}
	if !titleSeen {
		t.Errorf("bd title should reflect action value 'shipit': %v", bd.Args)
	}
}
