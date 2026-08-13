package session

import (
	"strings"
	"testing"
	"time"
)

func activeMailSession() Info {
	return Info{
		ID: "session-1", ConfiguredNamedIdentity: "reviewer",
		MetadataState: "active", Generation: "4", ContinuationEpoch: "7",
		InstanceToken: "instance-secret",
	}
}

func TestIssueMailActivationFenceDerivesControllerAuthority(t *testing.T) {
	issuedAt := time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC)
	fence, err := IssueMailActivationFence(activeMailSession(), MailActivationFenceOptions{
		CityRef: "city:test-city", SeatRef: "seat:test-city/reviewer",
		ConfigSHA256: strings.Repeat("a", 64), IssuedByRef: "controller:test-city/session-reconciler",
		IssuedAt: issuedAt,
	})
	if err != nil {
		t.Fatalf("IssueMailActivationFence: %v", err)
	}
	if err := validateMailActivationFence(fence); err != nil {
		t.Fatalf("fence validate: %v", err)
	}
	if fence.AuthorityKind != MailFenceAuthorityNamedSessionV1 ||
		fence.AuthorityGeneration != 4 || fence.ContinuationEpoch != 7 || fence.SessionRef != "session-1" {
		t.Fatalf("fence = %#v", fence)
	}
	if fence.InstanceTokenSHA256 == "instance-secret" || len(fence.InstanceTokenSHA256) != 64 {
		t.Fatalf("instance token digest = %q", fence.InstanceTokenSHA256)
	}

	again, err := IssueMailActivationFence(activeMailSession(), MailActivationFenceOptions{
		CityRef: "city:test-city", SeatRef: "seat:test-city/reviewer",
		ConfigSHA256: strings.Repeat("a", 64), IssuedByRef: "controller:test-city/session-reconciler",
		IssuedAt: issuedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("IssueMailActivationFence again: %v", err)
	}
	if again.FenceID != fence.FenceID || again.AuthorityIntentSHA256 != fence.AuthorityIntentSHA256 {
		t.Fatalf("equivalent authority changed stable identity: %#v vs %#v", again, fence)
	}
}

func TestIssueMailActivationFenceRejectsUnusableSession(t *testing.T) {
	valid := activeMailSession()
	options := MailActivationFenceOptions{
		CityRef: "city:test-city", SeatRef: "seat:test-city/reviewer",
		ConfigSHA256: strings.Repeat("a", 64), IssuedByRef: "controller:test-city/session-reconciler",
		IssuedAt: time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC),
	}
	tests := map[string]func(*Info, *MailActivationFenceOptions){
		"closed":           func(i *Info, _ *MailActivationFenceOptions) { i.Closed = true },
		"draining":         func(i *Info, _ *MailActivationFenceOptions) { i.MetadataState = "draining" },
		"wrong seat":       func(_ *Info, o *MailActivationFenceOptions) { o.SeatRef = "seat:test-city/other" },
		"missing token":    func(i *Info, _ *MailActivationFenceOptions) { i.InstanceToken = "" },
		"bad generation":   func(i *Info, _ *MailActivationFenceOptions) { i.Generation = "four" },
		"zero epoch":       func(i *Info, _ *MailActivationFenceOptions) { i.ContinuationEpoch = "0" },
		"untrusted config": func(_ *Info, o *MailActivationFenceOptions) { o.ConfigSHA256 = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			info, opts := valid, options
			mutate(&info, &opts)
			if _, err := IssueMailActivationFence(info, opts); err == nil {
				t.Fatal("IssueMailActivationFence succeeded")
			}
		})
	}
}
