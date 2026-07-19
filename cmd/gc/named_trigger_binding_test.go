package main

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/session/sessiontest"
)

func fullTriggerSessionBead(id string, storeRef string) beads.Bead {
	return beads.Bead{
		ID:     id,
		Type:   session.BeadType,
		Status: "open",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			beadmeta.TriggerBeadIDMetadataKey:       "wb-trigger",
			beadmeta.TriggerBeadStoreRefMetadataKey: storeRef,
			beadmeta.BrainParentSIDMetadataKey:      "brain-old",
			beadmeta.WorkDirMetadataKey:             "/work/wb-trigger-parked-title",
			beadmeta.LegacyWorkDirMetadataKey:       "/work/wb-trigger-parked-title",
		},
	}
}

// TestComputePoolTriggerBindingPatchClearAlsoClearsWorkDir pins #4373 Carrier 2:
// the clear branch must drop the trigger-derived work dir with the stamp. The
// recorded cwd literally names the stale bead (triggerBeadPathSlug), so leaving
// it re-aims the next seat at the parked work with no Env stamp involved.
func TestComputePoolTriggerBindingPatchClearAlsoClearsWorkDir(t *testing.T) {
	info := sessiontest.SeedBead(t, fullTriggerSessionBead("s-clear", "rig:testrig"))
	patch := computePoolTriggerBindingPatch(info, SessionRequest{}, "")
	for _, key := range []string{
		beadmeta.TriggerBeadIDMetadataKey,
		beadmeta.TriggerBeadStoreRefMetadataKey,
		beadmeta.BrainParentSIDMetadataKey,
		beadmeta.WorkDirMetadataKey,
		beadmeta.LegacyWorkDirMetadataKey,
	} {
		got, ok := patch[key]
		if !ok || got != "" {
			t.Errorf("clear patch %s: got (%q, present=%v), want cleared", key, got, ok)
		}
	}
}

// TestComputePoolTriggerBindingPatchClearWithoutTriggerKeepsWorkDir pins the
// conservative boundary of the Carrier 2 fix: with no trigger stamp there is no
// evidence the recorded work dir is trigger-derived, so a no-work clear must
// not touch it (a manually configured work dir survives).
func TestComputePoolTriggerBindingPatchClearWithoutTriggerKeepsWorkDir(t *testing.T) {
	info := sessiontest.SeedBead(t, beads.Bead{
		ID:     "s-noworktrigger",
		Type:   session.BeadType,
		Status: "open",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			beadmeta.WorkDirMetadataKey:       "/work/configured",
			beadmeta.LegacyWorkDirMetadataKey: "/work/configured",
		},
	})
	patch := computePoolTriggerBindingPatch(info, SessionRequest{}, "")
	if len(patch) != 0 {
		t.Errorf("clear patch on trigger-less info: got %v, want empty", patch)
	}
}

func TestClearStaleNamedTriggerBinding(t *testing.T) {
	tests := []struct {
		name          string
		triggerStatus string // "" = trigger bead absent from the store
		storeRef      string
		rigStoreKey   string // rig store registered under this name; "" = none
		wantCleared   bool
	}{
		{name: "closed trigger clears", triggerStatus: "closed", storeRef: "rig:testrig", rigStoreKey: "testrig", wantCleared: true},
		{name: "blocked trigger clears", triggerStatus: "blocked", storeRef: "rig:testrig", rigStoreKey: "testrig", wantCleared: true},
		{name: "absent trigger clears", triggerStatus: "", storeRef: "rig:testrig", rigStoreKey: "testrig", wantCleared: true},
		{name: "open trigger keeps stamp", triggerStatus: "open", storeRef: "rig:testrig", rigStoreKey: "testrig", wantCleared: false},
		{name: "in-progress trigger keeps stamp", triggerStatus: "in_progress", storeRef: "rig:testrig", rigStoreKey: "testrig", wantCleared: false},
		{name: "closed trigger in city store clears", triggerStatus: "closed", storeRef: "", rigStoreKey: "", wantCleared: true},
		{name: "unresolvable store ref keeps stamp", triggerStatus: "closed", storeRef: "rig:otherrig", rigStoreKey: "testrig", wantCleared: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionBead := fullTriggerSessionBead("s-named", tt.storeRef)
			cityBeads := []beads.Bead{sessionBead}
			var rigBeads []beads.Bead
			appendTrigger := func(dst []beads.Bead) []beads.Bead {
				if tt.triggerStatus == "" {
					return dst
				}
				return append(dst, beads.Bead{ID: "wb-trigger", Type: "task", Status: tt.triggerStatus})
			}
			if tt.rigStoreKey != "" && tt.storeRef == "rig:"+tt.rigStoreKey {
				rigBeads = appendTrigger(rigBeads)
			} else if tt.storeRef == "" {
				cityBeads = appendTrigger(cityBeads)
			}
			cityStore := beads.NewMemStoreFrom(100, cityBeads, nil)
			rigStores := map[string]beads.Store{}
			if tt.rigStoreKey != "" {
				rigStores[tt.rigStoreKey] = beads.NewMemStoreFrom(200, rigBeads, nil)
			}
			info, err := sessionFrontDoor(cityStore).Get(sessionBead.ID)
			if err != nil {
				t.Fatalf("front-door Get(%q): %v", sessionBead.ID, err)
			}
			bp := &agentBuildParams{beadStore: cityStore}

			var stderr strings.Builder
			got := clearStaleNamedTriggerBinding(bp, rigStores, info, &stderr)

			gotBead, err := cityStore.Get(sessionBead.ID)
			if err != nil {
				t.Fatalf("Get(%q) after clear: %v", sessionBead.ID, err)
			}
			if tt.wantCleared {
				if strings.TrimSpace(got.TriggerBeadID) != "" {
					t.Errorf("returned info TriggerBeadID = %q, want cleared", got.TriggerBeadID)
				}
				for _, key := range []string{
					beadmeta.TriggerBeadIDMetadataKey,
					beadmeta.TriggerBeadStoreRefMetadataKey,
					beadmeta.BrainParentSIDMetadataKey,
					beadmeta.WorkDirMetadataKey,
					beadmeta.LegacyWorkDirMetadataKey,
				} {
					if v := strings.TrimSpace(gotBead.Metadata[key]); v != "" {
						t.Errorf("persisted %s = %q, want cleared", key, v)
					}
				}
			} else {
				if got.TriggerBeadID != "wb-trigger" {
					t.Errorf("returned info TriggerBeadID = %q, want kept", got.TriggerBeadID)
				}
				if v := gotBead.Metadata[beadmeta.TriggerBeadIDMetadataKey]; v != "wb-trigger" {
					t.Errorf("persisted trigger stamp = %q, want untouched", v)
				}
				if v := gotBead.Metadata[beadmeta.WorkDirMetadataKey]; v != "/work/wb-trigger-parked-title" {
					t.Errorf("persisted work dir = %q, want untouched", v)
				}
			}
		})
	}
}

// TestClearStaleNamedTriggerBindingNoStampIsNoOp guards the fast path: a named
// session with no trigger stamp must not touch the store at all.
func TestClearStaleNamedTriggerBindingNoStampIsNoOp(t *testing.T) {
	sessionBead := beads.Bead{
		ID:       "s-plain",
		Type:     session.BeadType,
		Status:   "open",
		Labels:   []string{session.LabelSession},
		Metadata: map[string]string{beadmeta.WorkDirMetadataKey: "/work/configured"},
	}
	cityStore := beads.NewMemStoreFrom(100, []beads.Bead{sessionBead}, nil)
	info, err := sessionFrontDoor(cityStore).Get(sessionBead.ID)
	if err != nil {
		t.Fatalf("front-door Get: %v", err)
	}
	bp := &agentBuildParams{beadStore: cityStore}
	var stderr strings.Builder
	got := clearStaleNamedTriggerBinding(bp, nil, info, &stderr)
	if got.WorkDirCanonical != info.WorkDirCanonical {
		t.Errorf("info changed on no-stamp path: %+v", got)
	}
	gotBead, err := cityStore.Get(sessionBead.ID)
	if err != nil {
		t.Fatalf("Get after no-op: %v", err)
	}
	if v := gotBead.Metadata[beadmeta.WorkDirMetadataKey]; v != "/work/configured" {
		t.Errorf("work dir = %q, want untouched", v)
	}
}
