package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/worker"
)

const validFencedReceiptJSON = `{"execution_id":"execution-1","operation_id":"operation-1","request_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","target":{"session_id":"session-1","session_name":"worker","configured_identity":"formula-worker","generation":"2","continuation_epoch":"3","instance_token":"instance","started_config_hash":"config"},"process":{"pid":22,"process_group_id":22,"start_identity":"start"},"attached":false}`

func TestFencedExecutionClientRoundTripAndResultBearingError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/resolve"):
			_, _ = w.Write([]byte(`{"ok":true,"receipt":` + validFencedReceiptJSON + `}`))
		case strings.HasSuffix(r.URL.Path, "/execute"):
			_, _ = w.Write([]byte(`{"ok":false,"result":{"receipt":` + validFencedReceiptJSON + `,"outcome":"completed"},"error":{"code":"unknown_external_state"}}`))
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			_, _ = w.Write([]byte(`{"ok":true,"receipt":{"receipt":` + validFencedReceiptJSON + `,"revoked":true,"stopped":true}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewCityScopedClient(server.URL, "city")
	input := worker.FencedExecutionInput{TargetSessionID: "session-1"}
	receipt, err := client.ResolveFencedExecution(context.Background(), input)
	if err != nil || receipt.ExecutionID != "execution-1" {
		t.Fatalf("ResolveFencedExecution = %+v, %v", receipt, err)
	}
	result, err := client.ExecuteFencedExecution(context.Background(), input)
	if result.Outcome != "completed" || !IsFencedExecutionApplicationError(err) {
		t.Fatalf("ExecuteFencedExecution = %+v, %v", result, err)
	}
	stopped, err := client.CancelFencedExecution(context.Background(), worker.FencedExecutionCancelInput{TargetSessionID: "session-1", ExecutionID: "execution-1"})
	if err != nil || !stopped.Revoked || !stopped.Stopped {
		t.Fatalf("CancelFencedExecution = %+v, %v", stopped, err)
	}
}

func TestFencedExecutionClientRejectsMismatchedReceiptIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"receipt":` + validFencedReceiptJSON + `}`))
	}))
	defer server.Close()
	client := NewCityScopedClient(server.URL, "city")
	_, err := client.ResolveFencedExecution(context.Background(), worker.FencedExecutionInput{TargetSessionID: "replacement-session"})
	if err == nil || ShouldFallback(client, err) {
		t.Fatalf("mismatched receipt error=%v fallback=%v", err, ShouldFallback(client, err))
	}
}

func TestFencedExecutionClientRejectsMalformedSuccessfulResolve(t *testing.T) {
	tests := map[string]string{
		"null":   `null`,
		"object": `{}`,
		"string": `"receipt"`,
		"number": `42`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			_, err := NewCityScopedClient(server.URL, "city").ResolveFencedExecution(context.Background(), worker.FencedExecutionInput{})
			if err == nil || ShouldFallback(NewCityScopedClient(server.URL, "city"), err) {
				t.Fatalf("malformed success error = %v, fallback=%v", err, ShouldFallback(NewCityScopedClient(server.URL, "city"), err))
			}
		})
	}
}

func TestFencedExecutionApplicationErrorsAreNotMutationFallbacks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"conflict"}}`))
	}))
	defer server.Close()
	client := NewCityScopedClient(server.URL, "city")
	_, err := client.ResolveFencedExecution(context.Background(), worker.FencedExecutionInput{})
	if !IsFencedExecutionApplicationError(err) || ShouldFallback(client, err) || errors.Is(err, context.Canceled) {
		t.Fatalf("application error=%v fallback=%v", err, ShouldFallback(client, err))
	}
}

func TestFencedExecutionWireConversionFailsClosed(t *testing.T) {
	var receipt genclient.FencedExecutionReceipt
	if _, err := fencedExecutionReceiptFromGen(receipt); err == nil {
		t.Fatal("zero receipt succeeded")
	}
	requireValid := func() genclient.FencedExecutionReceipt {
		var value genclient.FencedExecutionReceipt
		if err := json.Unmarshal([]byte(validFencedReceiptJSON), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	receipt = requireValid()
	receipt.Target.InstanceToken = ""
	if _, err := fencedExecutionReceiptFromGen(receipt); err == nil {
		t.Fatal("incomplete authority succeeded")
	}
	result := genclient.FencedExecutionResult{Receipt: requireValid()}
	if _, err := fencedExecutionResultFromGen(result); err == nil {
		t.Fatal("empty outcome succeeded")
	}
	if got := (&FencedExecutionApplicationError{Code: "conflict"}).Error(); !strings.Contains(got, "conflict") {
		t.Fatalf("application error=%q", got)
	}
}

func TestFencedExecutionErrorCodesAreClosed(t *testing.T) {
	for err, want := range map[error]string{
		runtime.ErrFencedExecutionUnsupported:    "unsupported",
		runtime.ErrFencedExecutionConflict:       "conflict",
		runtime.ErrFencedExecutionStaleAuthority: "stale_authority",
		runtime.ErrFencedExecutionRevoked:        "revoked",
		runtime.ErrFencedExecutionUnknown:        "unknown_external_state",
		errors.New("other"):                      "internal",
	} {
		if got := fencedExecutionErrorCode(err); got != want {
			t.Fatalf("code(%v)=%q want %q", err, got, want)
		}
	}
}
