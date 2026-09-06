package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

// hookCurrentSessionID is the session bead id every case in this file resolves;
// the absent-session case deliberately asks for a different one.
const hookCurrentSessionID = "mc-sess1"

// hookCurrentFrontDoor builds a session front door over an in-memory store
// holding one session bead carrying claim (empty claim ⇒ nothing stamped).
func hookCurrentFrontDoor(t *testing.T, claim string) *session.Store {
	t.Helper()
	sessionID := hookCurrentSessionID
	meta := map[string]string{"state": string(session.StateActive)}
	if claim != "" {
		meta[beadmeta.CurrentClaimBeadIDMetadataKey] = claim
	}
	bead := beads.Bead{
		ID:       sessionID,
		Type:     session.BeadType,
		Status:   "open",
		Title:    "session",
		Labels:   []string{session.LabelSession},
		Metadata: meta,
	}
	return session.NewStore(beads.SessionStore{Store: beads.NewMemStoreFrom(1, []beads.Bead{bead}, nil)})
}

// TestDoHookCurrentPrintsTheStampedClaim is the primary read-back test: a session
// whose bead carries the claim stamp prints it, and --id-only prints the bare id
// so `$(gc hook current --id-only)` substitutes cleanly into a shell derivation.
func TestDoHookCurrentPrintsTheStampedClaim(t *testing.T) {
	sessFront := hookCurrentFrontDoor(t, "gcg-42")

	var stdout, stderr bytes.Buffer
	if code := doHookCurrent(sessFront, "mc-sess1", true, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookCurrent(--id-only) = %d, want 0; stderr=%s", code, stderr.String())
	}
	if got := stdout.String(); got != "gcg-42\n" {
		t.Fatalf("--id-only stdout = %q, want exactly the bead id", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("--id-only stderr = %q, want empty", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := doHookCurrent(sessFront, "mc-sess1", false, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookCurrent = %d, want 0; stderr=%s", code, stderr.String())
	}
	got := stdout.String()
	if !strings.HasPrefix(got, "gcg-42") || !strings.Contains(got, "mc-sess1") {
		t.Fatalf("stdout = %q, want the bead id plus the session it was claimed by", got)
	}
}

// TestDoHookCurrentExitsOneWhenNothingIsStamped is the fail-loud half of the
// contract: an unstamped session must NOT print an empty line and exit 0, or a
// caller substituting the output would silently derive an empty bead id — the
// exact fail-open the back-channel exists to close.
func TestDoHookCurrentExitsOneWhenNothingIsStamped(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doHookCurrent(hookCurrentFrontDoor(t, ""), "mc-sess1", true, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("doHookCurrent (unstamped) = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing printed when there is no claim", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no current claim") {
		t.Errorf("stderr = %q, want a message naming the missing claim", stderr.String())
	}
}

// TestDoHookCurrentExitsOneOnAnUnreadableSession covers the store arm: an id the
// session store cannot resolve is an error, not "nothing claimed", and must not
// print a bead id.
func TestDoHookCurrentExitsOneOnAnUnreadableSession(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doHookCurrent(hookCurrentFrontDoor(t, "gcg-42"), "mc-missing", true, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("doHookCurrent (absent session) = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing printed", stdout.String())
	}
	if !strings.Contains(stderr.String(), "gc hook current:") {
		t.Errorf("stderr = %q, want a gc hook current diagnostic", stderr.String())
	}
}

// TestCmdHookCurrentRequiresASessionIdentity pins the env arm: outside a session
// there is no bead to read, so the command refuses instead of reporting an empty
// claim, and never opens a store.
func TestCmdHookCurrentRequiresASessionIdentity(t *testing.T) {
	t.Setenv("GC_SESSION_ID", "")
	opened := false
	restore := hookCurrentSessionFrontDoor
	t.Cleanup(func() { hookCurrentSessionFrontDoor = restore })
	hookCurrentSessionFrontDoor = func() (*session.Store, error) {
		opened = true
		return nil, errors.New("must not be reached")
	}

	var stdout, stderr bytes.Buffer
	if code := cmdHookCurrent(true, &stdout, &stderr); code != 1 {
		t.Fatalf("cmdHookCurrent (no GC_SESSION_ID) = %d, want 1", code)
	}
	if opened {
		t.Error("cmdHookCurrent opened the session store without a session identity")
	}
	if !strings.Contains(stderr.String(), "no session identity") {
		t.Errorf("stderr = %q, want the missing-identity message", stderr.String())
	}
}

// TestCmdHookCurrentReadsTheCallingSessionFromEnv proves the command resolves the
// CALLING session from GC_SESSION_ID — the same variable the claim path stamped
// under — rather than any other identity variable.
func TestCmdHookCurrentReadsTheCallingSessionFromEnv(t *testing.T) {
	t.Setenv("GC_SESSION_ID", "mc-sess1")
	restore := hookCurrentSessionFrontDoor
	t.Cleanup(func() { hookCurrentSessionFrontDoor = restore })
	hookCurrentSessionFrontDoor = func() (*session.Store, error) {
		return hookCurrentFrontDoor(t, "gcg-42"), nil
	}

	var stdout, stderr bytes.Buffer
	if code := cmdHookCurrent(true, &stdout, &stderr); code != 0 {
		t.Fatalf("cmdHookCurrent = %d, want 0; stderr=%s", code, stderr.String())
	}
	if got := stdout.String(); got != "gcg-42\n" {
		t.Fatalf("stdout = %q, want gcg-42", got)
	}
}

func TestCmdHookCurrentRootVarRejectsNonCanonicalSessionEnvBeforeOpeningStores(t *testing.T) {
	t.Setenv("GC_SESSION_ID", " "+hookCurrentSessionID)
	opened := false
	restore := hookCurrentSessionFrontDoor
	t.Cleanup(func() { hookCurrentSessionFrontDoor = restore })
	hookCurrentSessionFrontDoor = func() (*session.Store, error) {
		opened = true
		return nil, errors.New("must not be reached")
	}

	var stdout, stderr bytes.Buffer
	code := cmdHookCurrentWithOptions(hookCurrentOptions{JSON: true, RootVar: "test_command"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("cmdHookCurrentWithOptions(noncanonical session) = %d, want 1", code)
	}
	if opened {
		t.Fatal("qualified current-claim read normalized GC_SESSION_ID and opened the session store")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "non-canonical session identity") {
		t.Fatalf("output = (%q, %q), want empty stdout and non-canonical identity diagnostic", stdout.String(), stderr.String())
	}
}

// TestHookCurrentIsWiredIntoTheHookCommandFamily proves the subcommand is
// reachable as `gc hook current` with an --id-only flag; a helper that only the
// formula calls is worthless if the CLI never registers it.
func TestHookCurrentIsWiredIntoTheHookCommandFamily(t *testing.T) {
	var stdout, stderr bytes.Buffer
	hook := newHookCmd(&stdout, &stderr)
	sub, _, err := hook.Find([]string{"current"})
	if err != nil || sub == nil || sub.Name() != "current" {
		t.Fatalf("gc hook current not registered: sub=%v err=%v", sub, err)
	}
	if sub.Flags().Lookup("id-only") == nil {
		t.Error("gc hook current has no --id-only flag")
	}
	if sub.Flags().Lookup("root-var") == nil || sub.Flags().Lookup("json") == nil {
		t.Error("gc hook current has no --root-var/--json machine-data mode")
	}
	if sub.Args == nil {
		t.Error("gc hook current accepts arbitrary args; it is scoped to the calling session")
	}
}

func TestHookCurrentPublishesItsMachineResultSchema(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hook", "current", "--json-schema"}, &stdout, &stderr); code != 0 {
		t.Fatalf("gc hook current --json-schema = %d; stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var manifest struct {
		Command       []string                   `json:"command"`
		JSONSupported bool                       `json:"json_supported"`
		Schemas       map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("schema manifest: %v\n%s", err, stdout.String())
	}
	if strings.Join(manifest.Command, " ") != "hook current" || !manifest.JSONSupported || !json.Valid(manifest.Schemas["result"]) {
		t.Fatalf("schema manifest = %#v", manifest)
	}
}

func TestDoHookCurrentRootVarUsesOnlyTheQualifiedReceiptStore(t *testing.T) {
	sessionBead := beads.Bead{
		ID: hookCurrentSessionID, Type: session.BeadType, Status: "open",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"state":                                  string(session.StateActive),
			beadmeta.CurrentClaimBeadIDMetadataKey:   "gcg-step",
			beadmeta.CurrentClaimStoreRefMetadataKey: "class:gmnos",
		},
	}
	sessFront := session.NewStore(beads.SessionStore{Store: beads.NewMemStoreFrom(1, []beads.Bead{sessionBead}, nil)})
	command := "printf '%s\\n' \"quoted\"\ncat <<'GC_TEST_COMMAND'\nbody\nGC_TEST_COMMAND\n"
	rootRef := "city:test-city"
	selected := beads.NewMemStoreFrom(1, []beads.Bead{
		{
			ID: "gcg-step", Status: "in_progress", Assignee: hookCurrentSessionID,
			Metadata: map[string]string{
				beadmeta.SessionIDMetadataKey:    hookCurrentSessionID,
				beadmeta.RootBeadIDMetadataKey:   "gcg-root",
				beadmeta.RootStoreRefMetadataKey: rootRef,
			},
		},
		{
			ID: "gcg-root", Status: "open",
			Metadata: map[string]string{
				beadmeta.KindMetadataKey:            beadmeta.KindWorkflow,
				beadmeta.FormulaContractMetadataKey: beadmeta.FormulaContractGraphV2,
				beadmeta.RootStoreRefMetadataKey:    rootRef,
				beadmeta.RuntimeVarsMetadataKey:     `{"test_command":` + string(mustJSON(t, command)) + `}`,
			},
		},
	}, nil)
	unrelatedSameID := beads.NewMemStoreFrom(1, []beads.Bead{
		{ID: "gcg-step", Status: "in_progress", Assignee: hookCurrentSessionID},
		{ID: "gcg-root", Metadata: map[string]string{beadmeta.RuntimeVarsMetadataKey: `{"test_command":"must-not-win"}`}},
	}, nil)
	opened := []string{}
	resolve := func(storeRef string) (beads.Store, error) {
		opened = append(opened, storeRef)
		if storeRef != "class:gmnos" {
			if _, err := unrelatedSameID.Get("gcg-step"); err != nil {
				t.Fatalf("same-id wrong-store fixture: %v", err)
			}
			return nil, errors.New("unrelated store opened")
		}
		return selected, nil
	}

	var stdout, stderr bytes.Buffer
	if code := doHookCurrentRootVar(sessFront, hookCurrentSessionID, "test_command", resolve, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookCurrentRootVar = %d, want 0; stderr=%s", code, stderr.String())
	}
	if got := strings.Join(opened, ","); got != "class:gmnos" {
		t.Fatalf("opened refs = %q, want only class:gmnos", got)
	}
	var result hookCurrentRootVarJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("root-var stdout: %v\n%s", err, stdout.String())
	}
	decoded, err := base64.StdEncoding.DecodeString(result.RootVar.Value)
	if err != nil || string(decoded) != command {
		t.Fatalf("decoded command = (%q, %v), want exact %q", decoded, err, command)
	}
	if !result.RootVar.Present || result.RootVar.Encoding != "base64" || result.ClaimStoreRef != "class:gmnos" || result.RootStoreRef != rootRef {
		t.Fatalf("root-var result = %#v", result)
	}
}

func TestDoHookCurrentRootVarPreservesAbsentVersusExplicitEmpty(t *testing.T) {
	for _, tt := range []struct {
		name     string
		rawVars  string
		present  bool
		wantData string
	}{
		{name: "absent", rawVars: `{}`, present: false},
		{name: "explicit empty", rawVars: `{"test_command":""}`, present: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sessFront, claimStore := hookCurrentRootVarStores(t, func(_, root *beads.Bead) {
				root.Metadata[beadmeta.RuntimeVarsMetadataKey] = tt.rawVars
			})
			var stdout, stderr bytes.Buffer
			code := doHookCurrentRootVar(sessFront, hookCurrentSessionID, "test_command", func(string) (beads.Store, error) {
				return claimStore, nil
			}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("doHookCurrentRootVar = %d, want 0; stderr=%s", code, stderr.String())
			}
			var result hookCurrentRootVarJSONResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("decode result: %v", err)
			}
			decoded, err := base64.StdEncoding.DecodeString(result.RootVar.Value)
			if err != nil || string(decoded) != tt.wantData || result.RootVar.Present != tt.present {
				t.Fatalf("root var = {present:%v value:%q err:%v}, want {present:%v value:%q}",
					result.RootVar.Present, decoded, err, tt.present, tt.wantData)
			}
		})
	}
}

func TestDoHookCurrentRootVarAcceptsExactSessionAliasOwnership(t *testing.T) {
	sessionBead := beads.Bead{
		ID: hookCurrentSessionID, Type: session.BeadType, Status: "open",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"state":                                  string(session.StateActive),
			"alias":                                  "worker-one",
			beadmeta.CurrentClaimBeadIDMetadataKey:   "gcg-step",
			beadmeta.CurrentClaimStoreRefMetadataKey: "rig:alpha",
		},
	}
	sessFront := session.NewStore(beads.SessionStore{Store: beads.NewMemStoreFrom(1, []beads.Bead{sessionBead}, nil)})
	_, claimStore := hookCurrentRootVarStores(t, func(step, _ *beads.Bead) { step.Assignee = "worker-one" })

	var stdout, stderr bytes.Buffer
	if code := doHookCurrentRootVar(sessFront, hookCurrentSessionID, "test_command", func(string) (beads.Store, error) {
		return claimStore, nil
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookCurrentRootVar(alias owner) = %d, want 0; stderr=%s", code, stderr.String())
	}
}

func TestDoHookCurrentRootVarRejectsReceiptChangedDuringRead(t *testing.T) {
	rootRef := "rig:alpha"
	mem := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: hookCurrentSessionID, Type: session.BeadType, Status: "open",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"state":                                  string(session.StateActive),
			beadmeta.CurrentClaimBeadIDMetadataKey:   "gcg-step",
			beadmeta.CurrentClaimStoreRefMetadataKey: "rig:alpha",
		},
	}}, nil)
	sessFront := session.NewStore(beads.SessionStore{Store: mem})
	claimStore := beads.NewMemStoreFrom(1, []beads.Bead{
		{
			ID: "gcg-step", Status: "in_progress", Assignee: hookCurrentSessionID,
			Metadata: map[string]string{
				beadmeta.SessionIDMetadataKey:    hookCurrentSessionID,
				beadmeta.RootBeadIDMetadataKey:   "gcg-root",
				beadmeta.RootStoreRefMetadataKey: rootRef,
			},
		},
		{
			ID: "gcg-root", Status: "open",
			Metadata: map[string]string{
				beadmeta.KindMetadataKey:            beadmeta.KindWorkflow,
				beadmeta.FormulaContractMetadataKey: beadmeta.FormulaContractGraphV2,
				beadmeta.RootStoreRefMetadataKey:    rootRef,
				beadmeta.RuntimeVarsMetadataKey:     `{"test_command":"echo ok"}`,
			},
		},
	}, nil)
	resolve := func(string) (beads.Store, error) {
		if err := mem.Update(hookCurrentSessionID, beads.UpdateOpts{Metadata: map[string]string{
			beadmeta.CurrentClaimBeadIDMetadataKey: "gcg-successor",
		}}); err != nil {
			t.Fatalf("change receipt: %v", err)
		}
		return claimStore, nil
	}

	var stdout, stderr bytes.Buffer
	if code := doHookCurrentRootVar(sessFront, hookCurrentSessionID, "test_command", resolve, &stdout, &stderr); code != 1 {
		t.Fatalf("doHookCurrentRootVar changed receipt = %d, want 1", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "current claim changed") {
		t.Fatalf("changed receipt output = (%q, %q), want empty stdout and change diagnostic", stdout.String(), stderr.String())
	}
}

func TestDoHookCurrentRootVarRejectsStaleSessionStampAfterReassignment(t *testing.T) {
	sessFront, claimStore := hookCurrentRootVarStores(t, func(step, _ *beads.Bead) {
		step.Assignee = "mc-other-session"
	})

	var stdout, stderr bytes.Buffer
	code := doHookCurrentRootVar(sessFront, hookCurrentSessionID, "test_command", func(string) (beads.Store, error) {
		return claimStore, nil
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("doHookCurrentRootVar stale assignee = %d, want 1", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "no longer owned") {
		t.Fatalf("stale assignee output = (%q, %q), want empty stdout and ownership diagnostic", stdout.String(), stderr.String())
	}
}

func TestDoHookCurrentRootVarRejectsNonCanonicalAuthorityMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(step, root *beads.Bead)
	}{
		{"step status", func(step, _ *beads.Bead) { step.Status = " in_progress " }},
		{"step session id", func(step, _ *beads.Bead) { step.Metadata[beadmeta.SessionIDMetadataKey] = " " + hookCurrentSessionID }},
		{"step root id", func(step, _ *beads.Bead) { step.Metadata[beadmeta.RootBeadIDMetadataKey] = "gcg-root " }},
		{"step root ref", func(step, _ *beads.Bead) { step.Metadata[beadmeta.RootStoreRefMetadataKey] = " city:test-city" }},
		{"root root ref", func(_, root *beads.Bead) { root.Metadata[beadmeta.RootStoreRefMetadataKey] = "city:test-city " }},
		{"formula contract", func(_, root *beads.Bead) {
			root.Metadata[beadmeta.FormulaContractMetadataKey] = strings.ToUpper(beadmeta.FormulaContractGraphV2)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessFront, claimStore := hookCurrentRootVarStores(t, tt.mutate)
			var stdout, stderr bytes.Buffer
			code := doHookCurrentRootVar(sessFront, hookCurrentSessionID, "test_command", func(string) (beads.Store, error) {
				return claimStore, nil
			}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("doHookCurrentRootVar = %d, want 1", code)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want no machine data for non-canonical authority", stdout.String())
			}
		})
	}
}

func TestDoHookCurrentRootVarRejectsChangedRelationAndMalformedVars(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(step, root *beads.Bead)
		want   string
	}{
		{
			name: "root relation changed",
			mutate: func(_, root *beads.Bead) {
				root.Metadata[beadmeta.RootStoreRefMetadataKey] = "rig:elsewhere"
			},
			want: "invalid or changed",
		},
		{
			name: "unknown root relation",
			mutate: func(step, root *beads.Bead) {
				step.Metadata[beadmeta.RootStoreRefMetadataKey] = "not-a-store-ref"
				root.Metadata[beadmeta.RootStoreRefMetadataKey] = "not-a-store-ref"
			},
			want: "unknown workflow-root store ref",
		},
		{
			name: "malformed graph vars",
			mutate: func(_, root *beads.Bead) {
				root.Metadata[beadmeta.RuntimeVarsMetadataKey] = `{"test_command":`
			},
			want: "decoding workflow root variables",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sessFront, claimStore := hookCurrentRootVarStores(t, tt.mutate)
			var stdout, stderr bytes.Buffer
			code := doHookCurrentRootVar(sessFront, hookCurrentSessionID, "test_command", func(string) (beads.Store, error) {
				return claimStore, nil
			}, &stdout, &stderr)
			if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("result = (code:%d stdout:%q stderr:%q), want code 1, empty stdout, diagnostic %q", code, stdout.String(), stderr.String(), tt.want)
			}
		})
	}
}

func TestDoHookCurrentRootVarPropagatesExactLookupAndResolverFailures(t *testing.T) {
	tests := []struct {
		name    string
		resolve hookCurrentStoreResolver
		want    string
	}{
		{
			name: "unknown qualified ref",
			resolve: func(ref string) (beads.Store, error) {
				if ref != "class:gmnos" {
					t.Fatalf("resolver ref = %q, want exact class:gmnos", ref)
				}
				return nil, errors.New("binding no longer configured")
			},
			want: "resolving current claim store",
		},
		{
			name: "exact id collision",
			resolve: func(string) (beads.Store, error) {
				return hookCurrentGetStore{get: func(string) (beads.Bead, error) {
					return beads.Bead{}, beads.ErrIDCollision
				}}, nil
			},
			want: "reading current claim",
		},
		{
			name: "fuzzy wrong id",
			resolve: func(string) (beads.Store, error) {
				return hookCurrentGetStore{get: func(string) (beads.Bead, error) {
					return beads.Bead{ID: "gcg-step-longer"}, nil
				}}, nil
			},
			want: "want exact id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessFront, _ := hookCurrentRootVarStores(t, func(_, _ *beads.Bead) {})
			var stdout, stderr bytes.Buffer
			code := doHookCurrentRootVar(sessFront, hookCurrentSessionID, "test_command", tt.resolve, &stdout, &stderr)
			if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("result = (code:%d stdout:%q stderr:%q), want code 1, empty stdout, diagnostic %q", code, stdout.String(), stderr.String(), tt.want)
			}
		})
	}
}

type hookCurrentGetStore struct {
	beads.Store
	get func(string) (beads.Bead, error)
}

func (s hookCurrentGetStore) Get(id string) (beads.Bead, error) {
	return s.get(id)
}

func hookCurrentRootVarStores(t *testing.T, mutate func(step, root *beads.Bead)) (*session.Store, beads.Store) {
	t.Helper()
	sessionBead := beads.Bead{
		ID: hookCurrentSessionID, Type: session.BeadType, Status: "open",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"state":                                  string(session.StateActive),
			beadmeta.CurrentClaimBeadIDMetadataKey:   "gcg-step",
			beadmeta.CurrentClaimStoreRefMetadataKey: "class:gmnos",
		},
	}
	step := beads.Bead{
		ID: "gcg-step", Status: "in_progress", Assignee: hookCurrentSessionID,
		Metadata: map[string]string{
			beadmeta.SessionIDMetadataKey:    hookCurrentSessionID,
			beadmeta.RootBeadIDMetadataKey:   "gcg-root",
			beadmeta.RootStoreRefMetadataKey: "city:test-city",
		},
	}
	root := beads.Bead{
		ID: "gcg-root", Status: "open",
		Metadata: map[string]string{
			beadmeta.KindMetadataKey:            beadmeta.KindWorkflow,
			beadmeta.FormulaContractMetadataKey: beadmeta.FormulaContractGraphV2,
			beadmeta.RootStoreRefMetadataKey:    "city:test-city",
			beadmeta.RuntimeVarsMetadataKey:     `{"test_command":"echo ok"}`,
		},
	}
	mutate(&step, &root)
	return session.NewStore(beads.SessionStore{Store: beads.NewMemStoreFrom(1, []beads.Bead{sessionBead}, nil)}),
		beads.NewMemStoreFrom(1, []beads.Bead{step, root}, nil)
}

func mustJSON(t *testing.T, value string) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test string: %v", err)
	}
	return data
}
