package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestMultiRegistryStartStopDestroy(t *testing.T) {
	store := beads.NewMemStore()
	reg := newMultiRegistry(store)

	// Start a new instance.
	mi, resumed, err := reg.start("researcher", "spike-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if resumed {
		t.Error("expected new instance, got resumed")
	}
	if mi.Template != "researcher" || mi.Name != "spike-1" || mi.State != "running" {
		t.Errorf("unexpected instance: %+v", mi)
	}

	// Starting the same instance again should fail.
	_, _, err = reg.start("researcher", "spike-1")
	if err == nil {
		t.Fatal("expected error starting already-running instance")
	}

	// Stop the instance.
	if err := reg.stop("researcher", "spike-1"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Verify it's stopped.
	found, err := reg.findInstance("researcher", "spike-1")
	if err != nil {
		t.Fatalf("findInstance: %v", err)
	}
	if found == nil || found.State != "stopped" {
		t.Errorf("expected stopped instance, got %+v", found)
	}

	// Stopping again should fail.
	if err := reg.stop("researcher", "spike-1"); err == nil {
		t.Fatal("expected error stopping already-stopped instance")
	}

	// Resume the stopped instance.
	mi, resumed, err = reg.start("researcher", "spike-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !resumed {
		t.Error("expected resumed, got new")
	}
	if mi.State != "running" {
		t.Errorf("expected running after resume, got %q", mi.State)
	}

	// Stop and destroy.
	if err := reg.stop("researcher", "spike-1"); err != nil {
		t.Fatalf("stop before destroy: %v", err)
	}
	if err := reg.destroy("researcher", "spike-1"); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	// After destroy, the instance should not be found.
	found, err = reg.findInstance("researcher", "spike-1")
	if err != nil {
		t.Fatalf("findInstance after destroy: %v", err)
	}
	if found != nil {
		t.Error("expected nil after destroy")
	}
}

func TestMultiRegistryDestroyRequiresStop(t *testing.T) {
	store := beads.NewMemStore()
	reg := newMultiRegistry(store)

	reg.start("researcher", "spike-1") //nolint:errcheck
	err := reg.destroy("researcher", "spike-1")
	if err == nil {
		t.Fatal("expected error destroying running instance")
	}
}

func TestMultiRegistryInstancesForTemplate(t *testing.T) {
	store := beads.NewMemStore()
	reg := newMultiRegistry(store)

	reg.start("researcher", "a") //nolint:errcheck
	reg.start("researcher", "b") //nolint:errcheck
	reg.start("other", "c")      //nolint:errcheck

	instances, err := reg.instancesForTemplate("researcher")
	if err != nil {
		t.Fatalf("instancesForTemplate: %v", err)
	}
	if len(instances) != 2 {
		t.Errorf("expected 2 researcher instances, got %d", len(instances))
	}

	// Stop one — should still appear in instancesForTemplate.
	reg.stop("researcher", "a") //nolint:errcheck
	instances, err = reg.instancesForTemplate("researcher")
	if err != nil {
		t.Fatalf("instancesForTemplate after stop: %v", err)
	}
	if len(instances) != 2 {
		t.Errorf("expected 2 instances (including stopped), got %d", len(instances))
	}
}

func TestMultiRegistryNextName(t *testing.T) {
	store := beads.NewMemStore()
	reg := newMultiRegistry(store)

	// First instance: researcher-1.
	name, err := reg.nextName("researcher")
	if err != nil {
		t.Fatalf("nextName: %v", err)
	}
	if name != "researcher-1" {
		t.Errorf("expected researcher-1, got %q", name)
	}

	// Create it, then get the next.
	reg.start("researcher", "researcher-1") //nolint:errcheck
	name, err = reg.nextName("researcher")
	if err != nil {
		t.Fatalf("nextName: %v", err)
	}
	if name != "researcher-2" {
		t.Errorf("expected researcher-2, got %q", name)
	}
}

func TestMultiRegistryNextNameSkipsDestroyed(t *testing.T) {
	store := beads.NewMemStore()
	reg := newMultiRegistry(store)

	// Create and destroy researcher-1.
	reg.start("researcher", "researcher-1")   //nolint:errcheck
	reg.stop("researcher", "researcher-1")    //nolint:errcheck
	reg.destroy("researcher", "researcher-1") //nolint:errcheck

	// Next name should be researcher-2 (never reuse destroyed names).
	name, err := reg.nextName("researcher")
	if err != nil {
		t.Fatalf("nextName: %v", err)
	}
	if name != "researcher-2" {
		t.Errorf("expected researcher-2 (skip destroyed), got %q", name)
	}
}

func TestMultiRegistryAllRunning(t *testing.T) {
	store := beads.NewMemStore()
	reg := newMultiRegistry(store)

	reg.start("researcher", "a") //nolint:errcheck
	reg.start("researcher", "b") //nolint:errcheck
	reg.start("other", "c")      //nolint:errcheck
	reg.stop("researcher", "b")  //nolint:errcheck

	running, err := reg.allRunning()
	if err != nil {
		t.Fatalf("allRunning: %v", err)
	}
	if len(running) != 2 {
		t.Errorf("expected 2 running, got %d", len(running))
	}
}

func TestMultiRegistryStopNotFound(t *testing.T) {
	store := beads.NewMemStore()
	reg := newMultiRegistry(store)

	err := reg.stop("researcher", "nonexistent")
	if err == nil {
		t.Fatal("expected error stopping nonexistent instance")
	}
}

func TestMultiRegistryDestroyNotFound(t *testing.T) {
	store := beads.NewMemStore()
	reg := newMultiRegistry(store)

	err := reg.destroy("researcher", "nonexistent")
	if err == nil {
		t.Fatal("expected error destroying nonexistent instance")
	}
}

func TestMultiRegistryNextNameRigScoped(t *testing.T) {
	store := beads.NewMemStore()
	reg := newMultiRegistry(store)

	// Rig-scoped template: "myrig/researcher".
	name, err := reg.nextName("myrig/researcher")
	if err != nil {
		t.Fatalf("nextName: %v", err)
	}
	if name != "researcher-1" {
		t.Errorf("expected researcher-1, got %q", name)
	}

	reg.start("myrig/researcher", "researcher-1") //nolint:errcheck
	name, err = reg.nextName("myrig/researcher")
	if err != nil {
		t.Fatalf("nextName: %v", err)
	}
	if name != "researcher-2" {
		t.Errorf("expected researcher-2, got %q", name)
	}
}
