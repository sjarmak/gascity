package branchreconciler

import (
	"reflect"
	"testing"
)

func TestParseForEachRefStripsRemotePrefixAndSkipsHEAD(t *testing.T) {
	output := "fork/HEAD\nfork/fix/big-refactor\nfork/main\nfork/stale-worktree\n"

	got := ParseForEachRef(output, "fork")

	want := []string{"fix/big-refactor", "main", "stale-worktree"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseForEachRef() = %v, want %v", got, want)
	}
}

func TestParseForEachRefIgnoresBlankLines(t *testing.T) {
	output := "fork/one\n\n\nfork/two\n"

	got := ParseForEachRef(output, "fork")

	want := []string{"one", "two"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseForEachRef() = %v, want %v", got, want)
	}
}

func TestParseForEachRefEmptyOutput(t *testing.T) {
	got := ParseForEachRef("", "fork")
	if len(got) != 0 {
		t.Fatalf("ParseForEachRef(\"\") = %v, want empty", got)
	}
}

func TestParsePRHeadRefsFiltersByForkOwnerLogin(t *testing.T) {
	input := []byte(`[
		{"headRefName": "fix/shipped", "headRepositoryOwner": {"login": "sjarmak"}},
		{"headRefName": "fix/from-other-fork", "headRepositoryOwner": {"login": "someone-else"}},
		{"headRefName": "fix/shipped-again", "headRepositoryOwner": {"login": "sjarmak"}}
	]`)

	got, err := ParsePRHeadRefs(input, "sjarmak")
	if err != nil {
		t.Fatalf("ParsePRHeadRefs() error = %v", err)
	}

	want := map[string]bool{
		"fix/shipped":       true,
		"fix/shipped-again": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePRHeadRefs() = %v, want %v", got, want)
	}
}

func TestParsePRHeadRefsHandlesDeletedForkOwner(t *testing.T) {
	// gh reports a null headRepositoryOwner when the source fork was deleted.
	input := []byte(`[{"headRefName": "fix/orphaned", "headRepositoryOwner": null}]`)

	got, err := ParsePRHeadRefs(input, "sjarmak")
	if err != nil {
		t.Fatalf("ParsePRHeadRefs() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ParsePRHeadRefs() = %v, want empty (owner deleted, not a match)", got)
	}
}

func TestParsePRHeadRefsEmptyInput(t *testing.T) {
	got, err := ParsePRHeadRefs([]byte(`[]`), "sjarmak")
	if err != nil {
		t.Fatalf("ParsePRHeadRefs() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ParsePRHeadRefs() = %v, want empty", got)
	}
}

func TestParsePRHeadRefsInvalidJSON(t *testing.T) {
	_, err := ParsePRHeadRefs([]byte(`not json`), "sjarmak")
	if err == nil {
		t.Fatal("ParsePRHeadRefs() error = nil, want error on invalid JSON")
	}
}
