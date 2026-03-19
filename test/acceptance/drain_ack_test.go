//go:build acceptance_a

// Static analysis test: every `exit` in example prompts and formulas
// must have a preceding `gc runtime drain-ack`.
//
// This is a regression test for the drain-ack audit performed on
// 2026-03-18, where bare `exit` calls were found in 14 files across
// gastown, maintenance, dolt, and swarm packs.
package acceptance_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDrainAckBeforeExit scans all .md.tmpl and .formula.toml files
// in examples/ for bare `exit` lines inside code blocks, and verifies
// each has `gc runtime drain-ack` on the preceding line.
func TestDrainAckBeforeExit(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "examples")

	var violations []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		name := info.Name()
		// Check prompt templates and formula TOML files.
		if !strings.HasSuffix(name, ".md.tmpl") && ext != ".toml" {
			return nil
		}
		// Skip non-formula TOML (pack.toml, order.toml, etc.)
		if ext == ".toml" && !strings.Contains(name, "formula") {
			return nil
		}

		v := checkFileForBareExit(t, path, root)
		violations = append(violations, v...)
		return nil
	})
	if err != nil {
		t.Fatalf("walking examples: %v", err)
	}

	if len(violations) > 0 {
		t.Errorf("found %d bare exit calls without drain-ack:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

func checkFileForBareExit(t *testing.T, path, root string) []string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()

	rel, _ := filepath.Rel(root, path)
	var violations []string
	var prevLine string
	lineNum := 0
	inCodeBlock := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Track code block boundaries.
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			prevLine = trimmed
			continue
		}

		// Only check inside code blocks.
		if !inCodeBlock {
			prevLine = trimmed
			continue
		}

		// Check for bare `exit` (exit alone on a line, not exit 0/1/2, not
		// "exit status", not in a comment, not in an if/then).
		if trimmed == "exit" {
			prevTrimmed := strings.TrimSpace(prevLine)
			if !strings.Contains(prevTrimmed, "drain-ack") {
				violations = append(violations,
					"  "+rel+":"+itoa(lineNum)+": bare exit without drain-ack (prev: "+prevTrimmed+")")
			}
		}

		prevLine = trimmed
	}

	return violations
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
