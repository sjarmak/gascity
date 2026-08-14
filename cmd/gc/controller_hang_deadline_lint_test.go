package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// rawHangDeadlinePattern is ga-57b2dk's acceptance-check regex: the raw-
// literal-duration shapes that #4638/#4639 replaced with awaitClose (for a
// channel drain) or awaitCond (for a polled condition) everywhere else in
// this package's cmd/gc tests. The third alternative catches
// waitForNamedMode's two call-site literals, which are invisible to the
// first two alternatives because the raw duration is an argument rather than
// a direct time.After/time.Now().Add call.
var rawHangDeadlinePattern = regexp.MustCompile(`time\.After\([0-9]|time\.Now\(\)\.Add\([0-9]|waitForNamedMode\([^)]*,\s*[0-9]`)

const rawHangDeadlineExemptionPrefix = "// ga-57b2dk-exempt:"

// controllerTestHangDeadlineExemptions are the raw-literal sites in
// controller_test.go that are correct as they stand, per TESTING.md's "Test
// deadline rule", and must NOT be migrated. Each key is a stable marker ID
// placed immediately above its deadline so unrelated line movement cannot
// change the exemption's identity.
var controllerTestHangDeadlineExemptions = map[string]string{
	"fake-server-scenario-input":            "input the test feeds a fake server to define the scenario, not a hang detector",
	"unrelated-nested-file-negative-window": "negative-assertion window (asserts no watcher poke arrives)",
	"runtime-trace-negative-window":         "negative-assertion window (asserts no watcher poke arrives, loop body)",
	"circuit-reset-best-effort-probe":       "bounded best-effort probe with no assertion on either branch",
}

func controllerTestPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRootForLint(t), "cmd", "gc", "controller_test.go")
}

func controllerTestLines(t *testing.T) (path string, data []byte, lines []string) {
	t.Helper()
	path = controllerTestPath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return path, data, strings.Split(string(data), "\n")
}

func rawHangDeadlineExemptionID(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, rawHangDeadlineExemptionPrefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, rawHangDeadlineExemptionPrefix)), true
}

// rawHangDeadlineOffenders returns formatted offender strings for every line
// in [from, to] that matches rawHangDeadlinePattern and is not immediately
// preceded by one of controllerTestHangDeadlineExemptions.
func rawHangDeadlineOffenders(path string, lines []string, from, to int) []string {
	var offenders []string
	if from < 1 {
		from = 1
	}
	for i := from; i <= to && i <= len(lines); i++ {
		line := lines[i-1]
		if !rawHangDeadlinePattern.MatchString(line) {
			continue
		}
		if i > 1 {
			if markerID, marked := rawHangDeadlineExemptionID(lines[i-2]); marked {
				if _, excluded := controllerTestHangDeadlineExemptions[markerID]; excluded {
					continue
				}
			}
		}
		offenders = append(offenders, formatOffender(path, i, line))
	}
	return offenders
}

func rawHangDeadlineExemptionErrors(path string, lines []string) []string {
	var errors []string
	seen := make(map[string]int, len(controllerTestHangDeadlineExemptions))
	for index, line := range lines {
		markerID, marked := rawHangDeadlineExemptionID(line)
		if !marked {
			continue
		}

		lineNo := index + 1
		if _, known := controllerTestHangDeadlineExemptions[markerID]; !known {
			errors = append(errors, fmt.Sprintf("%s:%d: unknown raw hang deadline exemption marker %q", path, lineNo, markerID))
			continue
		}
		if firstLine, duplicate := seen[markerID]; duplicate {
			errors = append(errors, fmt.Sprintf("%s:%d: duplicate raw hang deadline exemption marker %q (first at line %d)",
				path, lineNo, markerID, firstLine))
		} else {
			seen[markerID] = lineNo
		}
		if index+1 >= len(lines) || !rawHangDeadlinePattern.MatchString(lines[index+1]) {
			errors = append(errors, fmt.Sprintf("%s:%d: stale raw hang deadline exemption marker %q; next line is not a raw-literal deadline",
				path, lineNo, markerID))
		}
	}

	expectedIDs := make([]string, 0, len(controllerTestHangDeadlineExemptions))
	for markerID := range controllerTestHangDeadlineExemptions {
		expectedIDs = append(expectedIDs, markerID)
	}
	sort.Strings(expectedIDs)
	for _, markerID := range expectedIDs {
		if _, found := seen[markerID]; !found {
			errors = append(errors, fmt.Sprintf("%s: missing raw hang deadline exemption marker %q (%s)",
				path, markerID, controllerTestHangDeadlineExemptions[markerID]))
		}
	}
	return errors
}

// TestControllerTestHasNoUnmigratedRawHangDeadlines pins ga-57b2dk's primary
// acceptance check: every sub-10s raw-literal timer in controller_test.go
// that isn't one of the four documented exclusions must be migrated to
// awaitClose/awaitCond, exactly as #4638 already did for the rest of the
// package.
func TestControllerTestHasNoUnmigratedRawHangDeadlines(t *testing.T) {
	path, _, lines := controllerTestLines(t)

	violations := rawHangDeadlineOffenders(path, lines, 1, len(lines))
	violations = append(violations, rawHangDeadlineExemptionErrors(path, lines)...)
	if len(violations) > 0 {
		t.Fatalf("controller_test.go has %d raw-literal hang deadline or exemption violation(s); replace new hang detectors with awaitClose "+
			"(channel drain) or awaitCond (polled condition) per ga-57b2dk:\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

func TestControllerRawHangDeadlineExemptionsSurviveHarmlessLineInsertion(t *testing.T) {
	path, _, lines := controllerTestLines(t)
	shifted := make([]string, 0, len(lines)+1)
	shifted = append(shifted, "// harmless line inserted before the documented exclusions")
	shifted = append(shifted, lines...)

	violations := rawHangDeadlineOffenders(path, shifted, 1, len(shifted))
	violations = append(violations, rawHangDeadlineExemptionErrors(path, shifted)...)
	if len(violations) > 0 {
		t.Fatalf("harmless line insertion invalidated documented exclusions:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func TestControllerRawHangDeadlineExemptionMarkersRejectMutations(t *testing.T) {
	knownMarker := rawHangDeadlineExemptionPrefix + " fake-server-scenario-input"
	tests := []struct {
		name          string
		lines         []string
		wantOffenders int
		wantError     string
	}{
		{
			name:          "new raw deadline without marker",
			lines:         []string{"case <-time.After(20 * time.Millisecond):"},
			wantOffenders: 1,
		},
		{
			name:          "unknown marker does not exempt deadline",
			lines:         []string{rawHangDeadlineExemptionPrefix + " invented-exemption", "case <-time.After(20 * time.Millisecond):"},
			wantOffenders: 1,
			wantError:     "unknown raw hang deadline exemption marker",
		},
		{
			name:      "known marker without deadline is stale",
			lines:     []string{knownMarker, "select {}"},
			wantError: "stale raw hang deadline exemption marker",
		},
		{
			name:      "known marker cannot be duplicated",
			lines:     []string{knownMarker, "case <-time.After(20 * time.Millisecond):", knownMarker, "case <-time.After(30 * time.Millisecond):"},
			wantError: "duplicate raw hang deadline exemption marker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offenders := rawHangDeadlineOffenders("controller_test.go", tt.lines, 1, len(tt.lines))
			if len(offenders) != tt.wantOffenders {
				t.Fatalf("got %d raw deadline offenders, want %d: %v", len(offenders), tt.wantOffenders, offenders)
			}

			errors := rawHangDeadlineExemptionErrors("controller_test.go", tt.lines)
			if tt.wantError == "" {
				return
			}
			if joined := strings.Join(errors, "\n"); !strings.Contains(joined, tt.wantError) {
				t.Fatalf("exemption errors %q do not contain %q", joined, tt.wantError)
			}
		})
	}
}

// TestControllerTestNoFunctionMixesHangBudgetWithRawDeadline pins ga-57b2dk's
// second acceptance check: a test function that already uses hangBudget for
// some of its waits must not also carry an unmigrated raw-literal deadline —
// the exact same-function inconsistency #4638 left behind in four functions.
func TestControllerTestNoFunctionMixesHangBudgetWithRawDeadline(t *testing.T) {
	path, data, lines := controllerTestLines(t)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, data, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var violations []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		start := fset.Position(fn.Pos()).Line
		end := fset.Position(fn.End()).Line

		usesHangBudget := false
		for i := start; i <= end && i <= len(lines); i++ {
			if strings.Contains(lines[i-1], "hangBudget") {
				usesHangBudget = true
				break
			}
		}
		if !usesHangBudget {
			continue
		}
		if offenders := rawHangDeadlineOffenders(path, lines, start, end); len(offenders) > 0 {
			violations = append(violations, fmt.Sprintf("%s: %s", fn.Name.Name, strings.Join(offenders, "; ")))
		}
	}

	if len(violations) > 0 {
		t.Fatalf("functions mix hangBudget with a raw-literal hang deadline (ga-57b2dk):\n  %s",
			strings.Join(violations, "\n  "))
	}
}
