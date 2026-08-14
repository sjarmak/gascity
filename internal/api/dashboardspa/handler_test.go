package dashboardspa

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

var (
	dashboardAssetReferenceRE = regexp.MustCompile(`/assets/[^"']+`)
	dashboardConflictMarkerRE = regexp.MustCompile(`(?m)^(<<<<<<< |\|{7} |=======\s*$|>>>>>>> )`)
)

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	h, err := NewStaticHandler()
	if err != nil {
		t.Fatalf("NewStaticHandler: %v", err)
	}
	return h
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestServesIndexAtRoot(t *testing.T) {
	rec := get(t, newHandler(t), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /: Content-Type = %q, want text/html", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("GET /: Cache-Control = %q, want no-store", cc)
	}
	if !strings.Contains(rec.Body.String(), `id="root"`) {
		t.Errorf("GET /: body missing SPA root element")
	}
}

func TestUnknownClientRouteFallsBackToIndex(t *testing.T) {
	rec := get(t, newHandler(t), "/city/example/agents")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /city/...: status = %d, want 200 (SPA fallback)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id="root"`) {
		t.Errorf("GET /city/...: expected SPA shell, got %q", rec.Body.String())
	}
}

func TestReservedPrefixes404(t *testing.T) {
	h := newHandler(t)
	for _, p := range []string{"/v0/cities", "/api/city/x/config", "/health", "/openapi.json", "/debug/pprof/"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404 (reserved, not SPA shell)", p, rec.Code)
		}
	}
}

func TestHashedAssetIsImmutablyCached(t *testing.T) {
	// Vite emits content-hashed files under dist/assets/; discover a real one
	// from the embedded FS and confirm it is served with the immutable header.
	entries, err := fs.ReadDir(distFS, "dist/assets")
	if err != nil || len(entries) == 0 {
		t.Skip("no assets in embedded bundle")
	}
	asset := "/assets/" + entries[0].Name()
	rec := get(t, newHandler(t), asset)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200", asset, rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("asset %s: Cache-Control = %q, want immutable", asset, cc)
	}
}

func TestEmbeddedDashboardBundleIntegrity(t *testing.T) {
	if err := validateEmbeddedDashboardBundle(distFS); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEmbeddedDashboardBundleRejectsBrokenArtifacts(t *testing.T) {
	t.Run("non-canonical asset reference", func(t *testing.T) {
		bundle := fstest.MapFS{
			"dist/index.html": {Data: []byte(`<script src="/assets/../index.html"></script>`)},
		}
		if err := validateEmbeddedDashboardBundle(bundle); err == nil || !strings.Contains(err.Error(), "non-canonical") {
			t.Fatalf("validateEmbeddedDashboardBundle() error = %v, want non-canonical asset reference", err)
		}
	})

	t.Run("asset reference is a directory", func(t *testing.T) {
		bundle := fstest.MapFS{
			"dist/index.html":    {Data: []byte(`<script src="/assets/chunks"></script>`)},
			"dist/assets/chunks": {Mode: fs.ModeDir},
		}
		if err := validateEmbeddedDashboardBundle(bundle); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("validateEmbeddedDashboardBundle() error = %v, want regular-file asset", err)
		}
	})

	t.Run("missing referenced asset", func(t *testing.T) {
		bundle := fstest.MapFS{
			"dist/index.html": {Data: []byte(`<script src="/assets/missing.js"></script>`)},
		}
		if err := validateEmbeddedDashboardBundle(bundle); err == nil || !strings.Contains(err.Error(), "missing.js") {
			t.Fatalf("validateEmbeddedDashboardBundle() error = %v, want missing asset", err)
		}
	})

	t.Run("conflict marker", func(t *testing.T) {
		bundle := fstest.MapFS{
			"dist/index.html":    {Data: []byte(`<script src="/assets/app.js"></script>`)},
			"dist/assets/app.js": {Data: []byte("<<<<<<< HEAD\nconst version = 1\n=======\nconst version = 2\n>>>>>>> branch\n")},
		}
		if err := validateEmbeddedDashboardBundle(bundle); err == nil || !strings.Contains(err.Error(), "conflict marker") {
			t.Fatalf("validateEmbeddedDashboardBundle() error = %v, want conflict marker", err)
		}
	})
}

func validateEmbeddedDashboardBundle(bundle fs.FS) error {
	const indexPath = "dist/index.html"
	index, err := fs.ReadFile(bundle, indexPath)
	if err != nil {
		return fmt.Errorf("read embedded dashboard index: %w", err)
	}

	references := dashboardAssetReferenceRE.FindAllString(string(index), -1)
	if len(references) == 0 {
		return fmt.Errorf("embedded dashboard index has no asset references")
	}
	for _, reference := range references {
		cleanReference := path.Clean(reference)
		if cleanReference != reference || !strings.HasPrefix(cleanReference, "/assets/") {
			return fmt.Errorf("embedded dashboard asset reference %q is non-canonical", reference)
		}
		assetPath := path.Join("dist", strings.TrimPrefix(cleanReference, "/"))
		info, err := fs.Stat(bundle, assetPath)
		if err != nil {
			return fmt.Errorf("embedded dashboard references %s: %w", reference, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("embedded dashboard reference %s is not a regular file", reference)
		}
	}

	return fs.WalkDir(bundle, "dist", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := fs.ReadFile(bundle, filePath)
		if err != nil {
			return fmt.Errorf("read embedded dashboard file %s: %w", filePath, err)
		}
		if dashboardConflictMarkerRE.Match(contents) {
			return fmt.Errorf("embedded dashboard file %s contains a conflict marker", filePath)
		}
		return nil
	})
}

func TestCSPPinsInlineScriptHash(t *testing.T) {
	rec := get(t, newHandler(t), "/")
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("missing Content-Security-Policy header")
	}
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP missing script-src 'self': %q", csp)
	}
	// index.html ships an inline theme-boot script, so script-src must pin a
	// sha256 hash for it.
	if !strings.Contains(csp, "'sha256-") {
		t.Errorf("CSP script-src does not pin an inline-script hash: %q", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP missing frame-ancestors 'none': %q", csp)
	}
}

func TestBuildCSPSkipsExternalScripts(t *testing.T) {
	// An external module script (with src=) must NOT contribute a hash; only
	// inline scripts do.
	idx := []byte(`<html><head>` +
		`<script>console.log("inline")</script>` +
		`<script type="module" src="/assets/app.js"></script>` +
		`</head><body></body></html>`)
	csp := buildCSP(idx)
	if got := strings.Count(csp, "'sha256-"); got != 1 {
		t.Errorf("buildCSP pinned %d hashes, want 1 (inline only): %q", got, csp)
	}
}
