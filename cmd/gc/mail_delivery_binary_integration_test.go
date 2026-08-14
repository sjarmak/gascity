package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/mail/beadmail"
	"github.com/gastownhall/gascity/internal/maildelivery"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

func TestMailDeliveryExactBinaryRecoversCommittedEffectAcrossCWD(t *testing.T) {
	gcBinary := currentGCBinaryForTests(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	const (
		cityName    = "mail-binary-test"
		identity    = "reviewer"
		messageID   = "gc-mail-aaaaaaaaaaaaaaaaaaaaaaaa"
		subjectMark = "private-subject-mail-binary-marker"
		bodyMark    = "private-body-mail-binary-marker"
	)
	cityPath := t.TempDir()
	adapterDir := t.TempDir()
	adapterPath := filepath.Join(adapterDir, "stable-nudge-adapter")
	writeMailDeliveryBinaryCity(t, cityPath, cityName, identity, adapterPath)

	t.Setenv("GC_BEADS", "file")
	t.Setenv("GC_BEADS_SCOPE_ROOT", "")
	t.Setenv("GC_DOLT", "skip")
	t.Setenv("GC_HOME", t.TempDir())
	t.Setenv("GC_CITY", cityPath)
	t.Setenv("GC_SESSION", "exec:"+adapterPath)

	cfg, provenance, err := config.LoadWithIncludes(fsys.OSFS{}, filepath.Join(cityPath, "city.toml"))
	if err != nil {
		t.Fatalf("load isolated city config: %v", err)
	}
	spec, ok := session.FindNamedSessionSpec(cfg, cityName, identity)
	if !ok {
		t.Fatal("isolated named session is not resolvable")
	}
	store, err := openCityStoreAt(cityPath)
	if err != nil {
		t.Fatalf("open isolated seed store: %v", err)
	}
	innerStore, _, wrapped := unwrapBeadPolicyStore(store)
	if !wrapped {
		innerStore = store
	}
	fileStore, ok := innerStore.(*beads.FileStore)
	if !ok {
		t.Fatalf("isolated seed store = %T, want *beads.FileStore", store)
	}
	fileStore.HonorExplicitIDs = true
	mgr := newSessionManagerWithConfig(cityPath, store, runtime.NewFake(), cfg)
	info, err := mgr.CreateSession(ctx, session.CreateOptions{
		Alias: identity, ExplicitName: spec.SessionName,
		Template: identity, Title: "Mail binary fixture", Command: "true", WorkDir: t.TempDir(),
		Provider: "exec:" + adapterPath, Transport: "tmux",
		ExtraMeta: map[string]string{
			session.NamedSessionMetadataKey:      "true",
			session.NamedSessionIdentityMetadata: identity,
			session.NamedSessionModeMetadata:     "always",
		},
	})
	if err != nil {
		t.Fatalf("create isolated named session: %v", err)
	}
	seatRef := "seat:" + cityName + "/" + identity
	mailProvider := beadmail.New(store)
	_, delivery, err := mailProvider.SendDurable("fixture-sender", identity, subjectMark, bodyMark, beadmail.DurableSendIntent{
		MessageID: messageID, StoreRef: "city:" + cityName + "/messaging", SeatRef: seatRef,
		Policy: maildelivery.PolicyNotifyOnly, Attention: maildelivery.AttentionImmediate,
		ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed durable mail: %v", err)
	}
	resolver, err := mailDeliveryFenceResolverForSeat(sessionFrontDoor(store), cfg, cityName, seatRef,
		config.Revision(fsys.OSFS{}, provenance, cfg, cityPath))
	if err != nil {
		t.Fatalf("construct exact fence resolver: %v", err)
	}
	fence, err := resolver.ResolveMailActivationFence(ctx, seatRef)
	if err != nil {
		t.Fatalf("resolve exact activation fence: %v", err)
	}
	attemptID, err := maildelivery.AttemptID(delivery.ID, fence)
	if err != nil {
		t.Fatalf("derive attempt ID: %v", err)
	}
	nudgeID, err := maildelivery.NudgeID(attemptID, []string{delivery.ID})
	if err != nil {
		t.Fatalf("derive nudge ID: %v", err)
	}
	receipt, err := runtime.NewStableNudgeReceipt(nudgeID, spec.SessionName,
		runtime.TextContent("1 actionable mail delivery; run gc mail inbox"),
		"fixture-destination:"+nudgeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("build destination receipt: %v", err)
	}
	effectPath := filepath.Join(adapterDir, "effects")
	invocationPath := filepath.Join(adapterDir, "invocations")
	requestPath := filepath.Join(adapterDir, "request.json")
	receiptPath := filepath.Join(adapterDir, "receipt.json")
	lookupPath := filepath.Join(adapterDir, "lookup.json")
	writeMailDeliveryBinaryAdapter(t, adapterPath, info.ID, effectPath, invocationPath, requestPath, receiptPath, lookupPath, receipt)
	if err := closeBeadStoreHandle(store); err != nil {
		t.Fatalf("close isolated seed store: %v", err)
	}

	outsideCWD := t.TempDir()
	args := []string{
		"--city", cityPath, "mail", "delivery", "reconcile-seat", seatRef,
		"--limit", "1", "--expect-delivery-id", delivery.ID,
	}
	firstOut, firstErr := runMailDeliveryExactBinary(ctx, gcBinary, outsideCWD, cityPath, adapterPath, args)
	var exitErr *exec.ExitError
	if !errors.As(firstErr, &exitErr) {
		t.Fatalf("first exact gc did not lose its process after destination commit: err=%v stdout=%s", firstErr, firstOut)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("first exact gc exit = %v, want SIGKILL; output=%s", exitErr.ProcessState, firstOut)
	}
	requireSingleMailDeliveryBinaryEffect(t, effectPath, nudgeID)
	requireSingleMailDeliveryBinaryEffect(t, invocationPath, nudgeID)

	store, err = openCityStoreAt(cityPath)
	if err != nil {
		t.Fatalf("reopen isolated store after crash: %v", err)
	}
	deliveryStore := maildelivery.NewStore(store)
	invoking, err := deliveryStore.TransportAttempt(attemptID)
	if err != nil {
		t.Fatalf("read invoking attempt: %v", err)
	}
	if invoking.State != maildelivery.TransportInvoking || invoking.NudgeID != nudgeID || invoking.InvocationCount != 1 {
		t.Fatalf("attempt after exact gc kill = %#v", invoking)
	}
	invoking.InvocationLeaseUntil = time.Now().UTC().Add(-time.Second)
	invoking.InvocationStartedAt = invoking.InvocationLeaseUntil.Add(-maildelivery.TransportInvocationLeaseDuration)
	payload, err := json.Marshal(invoking)
	if err != nil {
		t.Fatalf("encode expired isolated attempt: %v", err)
	}
	if err := store.SetMetadata(attemptID, "mail.delivery.transport_attempt.v1", string(payload)); err != nil {
		t.Fatalf("expire isolated invocation lease: %v", err)
	}
	if err := closeBeadStoreHandle(store); err != nil {
		t.Fatalf("close isolated crashed store: %v", err)
	}

	secondOut, secondErr := runMailDeliveryExactBinary(ctx, gcBinary, outsideCWD, cityPath, adapterPath, args)
	if secondErr != nil {
		t.Fatalf("second exact gc failed: %v\n%s", secondErr, secondOut)
	}
	var report struct {
		SchemaVersion string `json:"schema_version"`
		SeatRef       string `json:"seat_ref"`
		Deliveries    []struct {
			DeliveryID string `json:"delivery_id"`
			Phase      string `json:"phase"`
			Outcome    string `json:"outcome"`
			Attempt    struct {
				AttemptID string `json:"attempt_id"`
				NudgeID   string `json:"nudge_id"`
				State     string `json:"state"`
			} `json:"attempt"`
		} `json:"deliveries"`
	}
	if err := json.Unmarshal(firstJSONLineForMailDeliveryBinaryTest(secondOut), &report); err != nil {
		t.Fatalf("decode second exact gc report: %v\n%s", err, secondOut)
	}
	if report.SchemaVersion != "mail-delivery-reconcile/v1" || report.SeatRef != seatRef || len(report.Deliveries) != 1 ||
		report.Deliveries[0].DeliveryID != delivery.ID || report.Deliveries[0].Phase != string(maildelivery.PhaseRuntimeNotified) ||
		report.Deliveries[0].Outcome != "committed" || report.Deliveries[0].Attempt.AttemptID != attemptID ||
		report.Deliveries[0].Attempt.NudgeID != nudgeID || report.Deliveries[0].Attempt.State != string(maildelivery.TransportCommitted) {
		t.Fatalf("second exact gc report = %#v", report)
	}
	for _, forbidden := range []string{subjectMark, bodyMark, messageID} {
		if strings.Contains(secondOut, forbidden) {
			t.Fatalf("exact gc report leaked %q: %s", forbidden, secondOut)
		}
	}
	requireSingleMailDeliveryBinaryEffect(t, effectPath, nudgeID)
	requireSingleMailDeliveryBinaryEffect(t, invocationPath, nudgeID)
	request, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read stable-nudge request: %v", err)
	}
	if !strings.Contains(string(request), nudgeID) {
		t.Fatalf("stable-nudge request lacks exact nudge ID: %s", request)
	}
	for _, forbidden := range []string{subjectMark, bodyMark, messageID, delivery.ID} {
		if strings.Contains(string(request), forbidden) {
			t.Fatalf("stable-nudge request leaked %q: %s", forbidden, request)
		}
	}

	store, err = openCityStoreAt(cityPath)
	if err != nil {
		t.Fatalf("reopen final isolated store: %v", err)
	}
	defer func() { _ = closeBeadStoreHandle(store) }()
	finalAttempt, err := maildelivery.NewStore(store).TransportAttempt(attemptID)
	if err != nil || finalAttempt.State != maildelivery.TransportCommitted || finalAttempt.NudgeID != nudgeID || finalAttempt.InvocationCount != 1 {
		t.Fatalf("final exact attempt = %#v, %v", finalAttempt, err)
	}
	finalDelivery, err := maildelivery.NewStore(store).Get(delivery.ID)
	if err != nil || finalDelivery.Phase != maildelivery.PhaseRuntimeNotified {
		t.Fatalf("final exact delivery = %#v, %v", finalDelivery, err)
	}
}

func writeMailDeliveryBinaryCity(t *testing.T, cityPath, cityName, identity, adapterPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(cityPath, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	cityTOML := fmt.Sprintf(`[workspace]
name = %q
prefix = "gc"

[beads]
provider = "file"
conditional_writes = "require"

[session]
provider = %q

[[agent]]
name = %q
provider = %q
start_command = "true"

[[named_session]]
name = %q
template = %q
scope = "city"
mode = "always"
`, cityName, "exec:"+adapterPath, identity, "exec:"+adapterPath, identity, identity)
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(cityTOML), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeMailDeliveryBinaryAdapter(t *testing.T, adapterPath, sessionID, effectPath, invocationPath, requestPath, receiptPath, lookupPath string, receipt runtime.StableNudgeReceipt) {
	t.Helper()
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	lookupJSON, err := json.Marshal(runtime.StableNudgeLookup{
		Version: 1, State: runtime.StableNudgeLookupCommitted, Receipt: receipt, ObservedAt: receipt.AcceptedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	unknownJSON := fmt.Sprintf(`{"version":1,"state":"unknown_external_state","observed_at":%q}`, time.Now().UTC().Format(time.RFC3339Nano))
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
  protocol) printf '%%s' '{"version":0,"capabilities":["effect.nudge-idempotent"]}' ;;
  is-running|process-alive) printf 'true' ;;
  get-meta)
    case "${3:-}" in
      GC_SESSION_ID) printf '%%s' %q ;;
      GC_CONTINUATION_EPOCH) printf '1' ;;
      *) printf '' ;;
    esac ;;
  nudge-stable)
    cat > %q
    printf '%%s\n' "$3" >> %q
    if test ! -f %q; then
      printf '%%s\n' "$3" >> %q
      printf '%%s' %q > %q
      printf '%%s' %q > %q
      kill -KILL "$PPID"
    fi
    cat %q ;;
  nudge-stable-status)
    cat > /dev/null
    if test -f %q; then cat %q; else printf '%%s' %q; fi ;;
  *) exit 2 ;;
esac
`, sessionID, requestPath, invocationPath, receiptPath, effectPath, string(receiptJSON), receiptPath,
		string(lookupJSON), lookupPath, receiptPath, receiptPath, lookupPath, unknownJSON)
	if err := os.WriteFile(adapterPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func runMailDeliveryExactBinary(ctx context.Context, binary, cwd, cityPath, adapterPath string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = cwd
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(cityPath, ".home"), "GC_HOME=" + filepath.Join(cityPath, ".gc-home"),
		"GC_CITY=" + cityPath, "GC_BEADS=file", "GC_BEADS_SCOPE_ROOT=", "GC_DOLT=skip", "GC_SESSION=exec:" + adapterPath,
		testFileStoreHonorExplicitIDsEnv + "=1",
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func requireSingleMailDeliveryBinaryEffect(t *testing.T, path, nudgeID string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read exact effect ledger: %v", err)
	}
	lines := strings.Fields(string(payload))
	if len(lines) != 1 {
		t.Fatalf("effect ledger = %q, want one effect", payload)
	}
	for _, line := range lines {
		if line != nudgeID {
			t.Fatalf("effect ledger ID = %q, want %q", line, nudgeID)
		}
	}
}

func firstJSONLineForMailDeliveryBinaryTest(output string) []byte {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			return []byte(strings.TrimSpace(line))
		}
	}
	return nil
}
