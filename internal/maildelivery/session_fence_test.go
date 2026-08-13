package maildelivery

import (
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/session"
)

func TestActivationFenceFromSessionPreservesControllerProjection(t *testing.T) {
	source, err := session.IssueMailActivationFence(session.Info{
		ID: "session-1", ConfiguredNamedIdentity: "reviewer", MetadataState: "active",
		Generation: "4", ContinuationEpoch: "7", InstanceToken: "token",
	}, session.MailActivationFenceOptions{
		CityRef: "city:test-city", SeatRef: "seat:test-city/reviewer",
		ConfigSHA256: strings.Repeat("a", 64), IssuedByRef: "controller:test-city/session-reconciler",
		IssuedAt: time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("IssueMailActivationFence: %v", err)
	}
	fence, err := ActivationFenceFromSession(source)
	if err != nil {
		t.Fatalf("ActivationFenceFromSession: %v", err)
	}
	if fence.FenceID != source.FenceID || fence.AuthorityGeneration != source.AuthorityGeneration {
		t.Fatalf("fence = %#v, source = %#v", fence, source)
	}
}
