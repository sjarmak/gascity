package herdr

import "testing"

// TestBoundPaneIndexMapsPanesToSessionNames covers the sidecar half of the
// merged session view: every binding with both a name and a pane must be
// reachable by pane id, since that is the only place a raw session's gc name
// exists (herdr's agent registry never names its pane).
func TestBoundPaneIndexMapsPanesToSessionNames(t *testing.T) {
	p := New("s", t.TempDir(), t.TempDir(), 0, 0)
	if err := p.bindPlacement("alpha", agentInfo{PaneID: "w1:p1", TabID: "w1:t1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement alpha: %v", err)
	}
	if err := p.bindPlacement("beta", agentInfo{PaneID: "w2:p1", TabID: "w2:t1"}, bindModeAgent); err != nil {
		t.Fatalf("bindPlacement beta: %v", err)
	}

	idx := p.boundPaneIndex()
	if got, want := len(idx), 2; got != want {
		t.Fatalf("index size = %d, want %d (%v)", got, want, idx)
	}
	if got := idx["w1:p1"]; got != "alpha" {
		t.Errorf("idx[w1:p1] = %q, want alpha", got)
	}
	if got := idx["w2:p1"]; got != "beta" {
		t.Errorf("idx[w2:p1] = %q, want beta", got)
	}
}

// TestBoundPaneIndexSkipsPartialBindings guards the merge against sidecars an
// in-flight Start has only half-written: a name with no pane is not a
// placement, and indexing it under "" would attribute unrelated frames.
func TestBoundPaneIndexSkipsPartialBindings(t *testing.T) {
	p := New("s", t.TempDir(), t.TempDir(), 0, 0)
	if err := p.SetMeta("halfway", metaBoundName, "halfway"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if idx := p.boundPaneIndex(); len(idx) != 0 {
		t.Fatalf("partial binding indexed: %v", idx)
	}
	if names := p.boundSessionNames(); len(names) != 0 {
		t.Fatalf("partial binding named: %v", names)
	}
}

// TestBoundPaneIndexDropsClearedBinding pins the removal path the activity
// tracker depends on: Stop clears the sidecar, and a session that is in
// neither the registry nor the index must fall out of the tracked map rather
// than stamping forever.
func TestBoundPaneIndexDropsClearedBinding(t *testing.T) {
	p := New("s", t.TempDir(), t.TempDir(), 0, 0)
	if err := p.bindPlacement("gone", agentInfo{PaneID: "w1:p1", TabID: "w1:t1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}
	if len(p.boundPaneIndex()) != 1 {
		t.Fatal("binding not indexed before clear")
	}
	p.clearPaneBinding("gone")
	if idx := p.boundPaneIndex(); len(idx) != 0 {
		t.Fatalf("cleared binding still indexed: %v", idx)
	}
}
