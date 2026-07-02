package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ismetkoralay/argus/internal/review"
)

func newTestServer(t *testing.T, responses ...string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		if calls >= len(responses) {
			t.Fatalf("unexpected extra request, already made %d calls", calls)
		}
		resp := responses[calls]
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":    "test-model",
			"response": resp,
			"done":     true,
		})
	}))
	return ts, &calls
}

func TestOllamaProvider_Review(t *testing.T) {
	unit := review.DiffUnit{File: "main.go", Hunk: "@@ -1,1 +1,2 @@\n+bug"}
	cfg := review.Config{Persona: "concise senior engineer"}

	t.Run("valid JSON on first try", func(t *testing.T) {
		validJSON := `[{"file":"main.go","line":2,"severity":"warning","category":"style","message":"nit"}]`
		ts, calls := newTestServer(t, validJSON)
		defer ts.Close()

		p := NewOllamaProvider(ts.URL, "test-model", ts.Client(), nil)
		findings, err := p.Review(context.Background(), unit, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 1 || findings[0].Message != "nit" {
			t.Fatalf("got %+v, want one finding with message 'nit'", findings)
		}
		if *calls != 1 {
			t.Fatalf("got %d calls, want 1 (no repair should have been triggered)", *calls)
		}
	})

	t.Run("invalid JSON then valid on repair retry", func(t *testing.T) {
		validJSON := `[{"file":"main.go","line":2,"severity":"error","category":"bug","message":"real bug"}]`
		ts, calls := newTestServer(t, "not json", validJSON)
		defer ts.Close()

		p := NewOllamaProvider(ts.URL, "test-model", ts.Client(), nil)
		findings, err := p.Review(context.Background(), unit, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 1 || findings[0].Message != "real bug" {
			t.Fatalf("got %+v, want one finding with message 'real bug'", findings)
		}
		if *calls != 2 {
			t.Fatalf("got %d calls, want 2 (one repair retry)", *calls)
		}
	})

	t.Run("object-wrapped findings on first try (Ollama's json mode bias)", func(t *testing.T) {
		wrappedJSON := `{"findings":[{"file":"main.go","line":2,"severity":"warning","category":"style","message":"nit"}]}`
		ts, calls := newTestServer(t, wrappedJSON)
		defer ts.Close()

		p := NewOllamaProvider(ts.URL, "test-model", ts.Client(), nil)
		findings, err := p.Review(context.Background(), unit, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 1 || findings[0].Message != "nit" {
			t.Fatalf("got %+v, want one finding with message 'nit'", findings)
		}
		if *calls != 1 {
			t.Fatalf("got %d calls, want 1 (object-wrapped findings shouldn't trigger repair)", *calls)
		}
	})

	t.Run("single bare object (one finding, no array) on first try", func(t *testing.T) {
		singleJSON := `{"file":"main.go","line":2,"severity":"warning","category":"style","message":"nit"}`
		ts, calls := newTestServer(t, singleJSON)
		defer ts.Close()

		p := NewOllamaProvider(ts.URL, "test-model", ts.Client(), nil)
		findings, err := p.Review(context.Background(), unit, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 1 || findings[0].Message != "nit" {
			t.Fatalf("got %+v, want one finding with message 'nit'", findings)
		}
		if *calls != 1 {
			t.Fatalf("got %d calls, want 1 (bare single object shouldn't trigger repair)", *calls)
		}
	})

	t.Run("invalid JSON on both attempts returns error", func(t *testing.T) {
		ts, calls := newTestServer(t, "not json", "still not json")
		defer ts.Close()

		p := NewOllamaProvider(ts.URL, "test-model", ts.Client(), nil)
		findings, err := p.Review(context.Background(), unit, cfg)
		if err == nil {
			t.Fatal("expected error after exhausting repair retry, got nil")
		}
		if findings != nil {
			t.Fatalf("got findings %+v, want nil", findings)
		}
		if *calls != 2 {
			t.Fatalf("got %d calls, want 2 (initial + one repair retry)", *calls)
		}
	})

	t.Run("individually invalid findings are dropped, valid ones kept", func(t *testing.T) {
		mixedJSON := `[
			{"file":"main.go","line":2,"severity":"warning","category":"style","message":"ok finding"},
			{"file":"","line":2,"severity":"warning","category":"style","message":"missing file"},
			{"file":"main.go","line":0,"severity":"warning","category":"style","message":"bad line"},
			{"file":"main.go","line":2,"severity":"critical","category":"style","message":"bad severity"},
			{"file":"main.go","line":2,"severity":"warning","category":"vibes","message":"bad category"},
			{"file":"main.go","line":2,"severity":"warning","category":"style","message":""}
		]`
		ts, calls := newTestServer(t, mixedJSON)
		defer ts.Close()

		p := NewOllamaProvider(ts.URL, "test-model", ts.Client(), nil)
		findings, err := p.Review(context.Background(), unit, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 1 || findings[0].Message != "ok finding" {
			t.Fatalf("got %+v, want only the one valid finding", findings)
		}
		if *calls != 1 {
			t.Fatalf("got %d calls, want 1 (invalid findings don't trigger repair)", *calls)
		}
	})
}
