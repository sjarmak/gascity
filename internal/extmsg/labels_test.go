package extmsg

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestExtmsgConstructorsNoDuplicateLabels(t *testing.T) {
	t.Helper()

	store := beads.NewMemStore()
	svc := NewServices(store)
	ctx := context.Background()
	ref := testConversationRef()

	entry, err := svc.Transcript.Append(ctx, AppendTranscriptInput{
		Caller:            testAdapterCaller(),
		Conversation:      ref,
		Kind:              TranscriptMessageInbound,
		Provenance:        TranscriptProvenanceLive,
		ProviderMessageID: "extmsg-dupe-check-1",
		Text:              "hello",
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	entryBead, err := store.Get(entry.ID)
	if err != nil {
		t.Fatalf("store.Get(append-bead): %v", err)
	}
	assertLabelSliceNoDuplicates(t, entryBead.Labels)

	membership, err := svc.Transcript.EnsureMembership(ctx, EnsureMembershipInput{
		Caller:       testControllerCaller(),
		Conversation: ref,
		SessionID:    "sess-membership",
		Owner:        MembershipOwnerManual,
	})
	if err != nil {
		t.Fatalf("EnsureMembership: %v", err)
	}
	membershipBead, err := store.Get(membership.ID)
	if err != nil {
		t.Fatalf("store.Get(membership-bead): %v", err)
	}
	assertLabelSliceNoDuplicates(t, membershipBead.Labels)

	state, err := svc.Transcript.State(ctx, testControllerCaller(), ref)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state == nil {
		t.Fatal("State = nil, want transcript state record")
	}
	stateBead, err := store.Get(state.ID)
	if err != nil {
		t.Fatalf("store.Get(state-bead): %v", err)
	}
	assertLabelSliceNoDuplicates(t, stateBead.Labels)

	binding, err := svc.Bindings.Bind(ctx, testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-binding",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	bindingBead, err := store.Get(binding.ID)
	if err != nil {
		t.Fatalf("store.Get(binding-bead): %v", err)
	}
	assertLabelSliceNoDuplicates(t, bindingBead.Labels)

	if err := svc.Delivery.Record(ctx, testControllerCaller(), DeliveryContextRecord{
		SessionID:         "sess-binding",
		Conversation:      ref,
		BindingGeneration: binding.BindingGeneration,
		LastPublishedAt:   testNow(),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	record, err := svc.Delivery.Resolve(ctx, "sess-binding", ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if record == nil {
		t.Fatal("Resolve = nil, want delivery context record")
	}
	deliveryBead, err := store.Get(record.ID)
	if err != nil {
		t.Fatalf("store.Get(delivery-bead): %v", err)
	}
	assertLabelSliceNoDuplicates(t, deliveryBead.Labels)

	group, err := svc.Groups.EnsureGroup(ctx, testControllerCaller(), EnsureGroupInput{
		RootConversation: ref,
		Mode:             GroupModeLauncher,
	})
	if err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}
	groupBead, err := store.Get(group.ID)
	if err != nil {
		t.Fatalf("store.Get(group-bead): %v", err)
	}
	assertLabelSliceNoDuplicates(t, groupBead.Labels)
}

func assertLabelSliceNoDuplicates(t *testing.T, labels []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		if _, ok := seen[label]; ok {
			t.Fatalf("labels contain duplicate %q", label)
		}
		seen[label] = struct{}{}
	}
}
