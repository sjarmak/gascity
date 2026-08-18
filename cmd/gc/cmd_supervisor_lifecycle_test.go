package main

import (
	"reflect"
	"testing"
)

// TestSupervisorProcessEnvMapSkipsEmptyKeys pins the fix for a regression the
// managed-Dolt adopt path's environ reader would otherwise reintroduce: a
// malformed /proc/<pid>/environ entry like "=value" (empty key) must be
// dropped, not surface as env[""].
func TestSupervisorProcessEnvMapSkipsEmptyKeys(t *testing.T) {
	data := []byte("FOO=bar\x00=value\x00BAR=\x00EMPTY\x00")
	got := supervisorProcessEnvMap(data)
	want := map[string]string{"FOO": "bar", "BAR": ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("supervisorProcessEnvMap(%q) = %#v, want %#v", data, got, want)
	}
	if _, ok := got[""]; ok {
		t.Fatalf("supervisorProcessEnvMap(%q) kept an empty-key entry: %#v", data, got)
	}
}
