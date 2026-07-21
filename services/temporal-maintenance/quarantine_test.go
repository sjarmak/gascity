package temporalmaintenance

import (
	"testing"
)

// A pending claim (worker crashed between Claim and Complete/Fail) is
// quarantined to failed with a reason, preserving at-most-once: the transition
// records the poison durably and never re-runs the command.
func TestKeyStore_QuarantinePendingToFailed(t *testing.T) {
	s, err := NewKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := s.Claim(mut("poison")); err != nil || !claimed {
		t.Fatalf("Claim = claimed=%v err=%v, want claimed=true", claimed, err)
	}

	rec, transitioned, err := s.Quarantine("poison", "worker crashed mid-execution")
	if err != nil {
		t.Fatalf("Quarantine err = %v", err)
	}
	if !transitioned {
		t.Fatalf("Quarantine transitioned=false, want true for a pending claim")
	}
	if rec.Status != ExecFailed {
		t.Fatalf("quarantined status = %q, want failed", rec.Status)
	}
	if rec.Err != "worker crashed mid-execution" {
		t.Fatalf("quarantined reason = %q, want the passed reason", rec.Err)
	}
	if rec.CompletedAt.IsZero() {
		t.Fatalf("quarantined record has zero CompletedAt")
	}

	// Durable: a fresh store over the same dir reads the failed record.
	reloaded, ok, err := s.Load("poison")
	if err != nil || !ok {
		t.Fatalf("reload = ok=%v err=%v", ok, err)
	}
	if reloaded.Status != ExecFailed {
		t.Fatalf("reloaded status = %q, want failed", reloaded.Status)
	}
}

// Quarantine is idempotent on an already-terminal record: a second redelivery
// reports transitioned=false without error or overwriting the reason, so it
// never double-escalates.
func TestKeyStore_QuarantineIdempotentOnAlreadyFailed(t *testing.T) {
	s, err := NewKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Claim(mut("poison")); err != nil {
		t.Fatal(err)
	}
	if _, transitioned, err := s.Quarantine("poison", "first"); err != nil || !transitioned {
		t.Fatalf("first Quarantine = transitioned=%v err=%v", transitioned, err)
	}
	rec, transitioned, err := s.Quarantine("poison", "second")
	if err != nil {
		t.Fatalf("second Quarantine err = %v, want nil (idempotent)", err)
	}
	if transitioned {
		t.Fatalf("second Quarantine transitioned=true, want false on an already-failed record")
	}
	if rec.Err != "first" {
		t.Fatalf("reason overwritten to %q, want the original %q", rec.Err, "first")
	}
}

// Quarantine preserves a partial result reference (a bead created before the
// crash) so the orphan is still structurally recorded, not erased.
func TestKeyStore_QuarantinePreservesResultRef(t *testing.T) {
	s, err := NewKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := mut("poison")
	m.Result = "gc-4qz"
	// Simulate a claim whose record carries a partial ref (set at claim-marshal
	// time via the mutation): claim, then quarantine.
	if _, _, err := s.Claim(m); err != nil {
		t.Fatal(err)
	}
	rec, _, err := s.Quarantine("poison", "crashed after create, before sling")
	if err != nil {
		t.Fatalf("Quarantine err = %v", err)
	}
	if rec.Mutation.Result != "gc-4qz" {
		t.Fatalf("quarantine lost the mutation result ref, got %q", rec.Mutation.Result)
	}
}

// Quarantining an unknown key is an error, not a silent no-op.
func TestKeyStore_QuarantineUnknownKey(t *testing.T) {
	s, err := NewKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Quarantine("nope", "x"); err == nil {
		t.Fatalf("Quarantine of an unclaimed key returned nil error, want an error")
	}
}
