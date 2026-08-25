package bdflags

import (
	"errors"
	"sync"
	"testing"
)

// TestScanUnknownFlagsUnknownTokenFailsClosed drives ScanUnknownFlags — the
// actual argv scanner this package exists to protect — with discovered help
// text for a flag whose type token isValueToken does not recognize
// ("widget"). It proves the real shift-by-one consequence of misclassifying
// that flag as boolean: the token that is really --frobnicate's value gets
// scanned as if it were the next flag, so an unrelated unrecognized flag
// immediately after gets reported as a false-positive Finding.
//
// With the dr-n959f fix (unknown lowercase token -> value-taking),
// --frobnicate correctly consumes its value token and ScanUnknownFlags
// reports nothing. On pre-fix main (unknown lowercase token -> boolean),
// --frobnicate advances by 1 instead of 2, and the value token
// "--unrelated-flag" is scanned on its own and reported as unknown.
func TestScanUnknownFlagsUnknownTokenFailsClosed(t *testing.T) {
	parseDiscoveredOnce = sync.Map{} // clear any real "update" entry cached by an earlier, unmocked test
	orig := runBdHelpForSubcommand
	runBdHelpForSubcommand = func(sub string) ([]byte, error) {
		if sub != "update" {
			return nil, errors.New("unexpected subcommand")
		}
		return []byte(`Flags:
      --frobnicate widget   frobnicate the widget`), nil
	}
	defer func() {
		runBdHelpForSubcommand = orig
		parseDiscoveredOnce = sync.Map{}
	}()

	source := []byte("bd update abc-1 --frobnicate --unrelated-flag")
	findings := ScanUnknownFlags(source)
	if len(findings) != 0 {
		t.Fatalf("ScanUnknownFlags() = %v, want no findings (--frobnicate must consume its value token, not leave --unrelated-flag to be misread as a flag)", findings)
	}
}

// TestScanUnknownFlagsUnknownTokenPreFixWouldMisfire is the RED-on-main
// control for the case above: it drives isValueToken directly with the old
// fail-open default (unknown lowercase token -> boolean) and confirms that
// default really does cause classifyFlag to advance by 1 instead of 2,
// which is the mechanism TestScanUnknownFlagsUnknownTokenFailsClosed guards
// against end-to-end.
func TestScanUnknownFlagsUnknownTokenPreFixWouldMisfire(t *testing.T) {
	valueFlags := map[string]bool{}
	boolFlags := map[string]bool{"--frobnicate": true} // pre-fix classification of an unknown lowercase token

	_, advance, _ := classifyFlag("--frobnicate", valueFlags, boolFlags)
	if advance != 1 {
		t.Fatalf("classifyFlag with pre-fix boolean classification advance = %d, want 1 (sanity check on the bug mechanism)", advance)
	}

	valueFlags["--frobnicate"] = true
	delete(boolFlags, "--frobnicate")
	_, advance, _ = classifyFlag("--frobnicate", valueFlags, boolFlags)
	if advance != 2 {
		t.Fatalf("classifyFlag with fixed value classification advance = %d, want 2", advance)
	}
}
