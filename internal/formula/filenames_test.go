package formula

import "testing"

func TestTrimTOMLFilenameStripsSupportedSuffixes(t *testing.T) {
	cases := []struct {
		name string
		file string
		want string
		ok   bool
	}{
		{name: "plain TOML", file: "build-review.toml", want: "build-review", ok: true},
		{name: "infixed TOML", file: "build-review.formula.toml", want: "build-review", ok: true},
		{name: "JSON is not a TOML filename", file: "build-review.formula.json"},
		{name: "not a formula file", file: "README.md"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := TrimTOMLFilename(tc.file)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Fatalf("name = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCanonicalNameCollapsesAcceptedSpellings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "bare name unchanged", in: "build-review", want: "build-review"},
		{name: "plain TOML", in: "build-review.toml", want: "build-review"},
		{name: "infixed TOML", in: "build-review.formula.toml", want: "build-review"},
		{name: "legacy JSON", in: "build-review.formula.json", want: "build-review"},
		{name: "idempotent", in: "build-review", want: "build-review"},
		{name: "unknown suffix kept", in: "build-review.md", want: "build-review.md"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanonicalName(tc.in); got != tc.want {
				t.Fatalf("CanonicalName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
