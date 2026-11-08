package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func TestModelsBuiltinFallback(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	ml := (&Service{}).Models()
	if ml.Source != "builtin" {
		t.Fatalf("source %q", ml.Source)
	}
	var got []string
	for _, m := range ml.Models {
		got = append(got, m.ID+":"+m.Tier+map[bool]string{true: "*"}[m.Latest])
	}
	want := "claude-fable-5-1:fable* claude-haiku-5-5:haiku* claude-opus-5-5:opus* claude-sonnet-5-5:sonnet*"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v\nwant %s", got, want)
	}
	if r := ml.Resolve("opus"); r != "claude-opus-5-5" {
		t.Errorf("Resolve(opus) = %q", r)
	}
	if r := ml.Resolve("claude-fable-5-1[1m]"); r != "claude-fable-5-1" {
		t.Errorf("Resolve([1m]) = %q", r)
	}
	if r := ml.Resolve("claude-opus-9"); r != "" {
		t.Errorf("unknown resolved to %q", r)
	}
}

func TestClassifyLatestPerTier(t *testing.T) {
	ms := classify([]string{"claude-opus-5", "claude-opus-5-5", "claude-3-5-sonnet-20241022", "claude-haiku-4-5-20251001", "claude-haiku-5-5", "<junk>"})
	var got []string
	for _, m := range ms {
		got = append(got, m.ID+map[bool]string{true: "*"}[m.Latest])
	}
	want := "claude-haiku-5-5* claude-haiku-4-5-20251001 claude-opus-5-5* claude-opus-5 claude-3-5-sonnet-20241022*"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v\nwant %s", got, want)
	}
	if r := (ModelList{Models: ms}).Resolve("claude-haiku-4-5"); r != "claude-haiku-4-5-20251001" {
		t.Errorf("dateless id resolved to %q", r)
	}
}

func TestModelsFromAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "k" {
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":[{"id":"claude-sonnet-5-5","type":"model"},{"id":"claude-sonnet-5"}],"has_more":false}`))
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_API_KEY", "k")
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	ml := (&Service{}).Models()
	if ml.Source != "api" || len(ml.Models) != 2 || ml.Resolve("sonnet") != "claude-sonnet-5-5" {
		t.Fatalf("api: %+v", ml)
	}
	t.Setenv("ANTHROPIC_API_KEY", "bad") // API error: fall back to the builtin list
	if ml := (&Service{}).Models(); ml.Source != "builtin" {
		t.Fatalf("bad key: %+v", ml)
	}
}

func TestRunnerModelsAgy(t *testing.T) {
	// 1. Builtin agy models
	oldCmd := detectAgyModelsCmd
	defer func() { detectAgyModelsCmd = oldCmd }()
	detectAgyModelsCmd = func(_ context.Context) ([]byte, error) {
		return nil, fmt.Errorf("agy not available")
	}

	s := &Service{}
	ml := s.RunnerModels("agy")
	if ml.Source != "builtin" || ml.Runner != "agy" {
		t.Fatalf("expected builtin agy models, got %+v", ml)
	}
	if len(ml.Models) != len(builtinAgyModels) {
		t.Fatalf("got %d models, want %d", len(ml.Models), len(builtinAgyModels))
	}

	// Test Resolve on agy models
	if r := ml.Resolve("sonnet"); r != "claude-sonnet-4-6" {
		t.Errorf("Resolve(sonnet) = %q, want claude-sonnet-4-6", r)
	}
	if r := ml.Resolve("opus"); r != "claude-opus-4-6-thinking" {
		t.Errorf("Resolve(opus) = %q, want claude-opus-4-6-thinking", r)
	}
	if r := ml.Resolve("flash"); r != "gemini-3.8-flash-medium" {
		t.Errorf("Resolve(flash) = %q, want gemini-3.8-flash-medium", r)
	}
	if r := ml.Resolve("Gemini 3.8 Flash (High)"); r != "gemini-3.8-flash-high" {
		t.Errorf("Resolve(Gemini 3.8 Flash (High)) = %q, want gemini-3.8-flash-high", r)
	}
	if r := ml.Resolve("gemini-3.8-flash-low"); r != "gemini-3.8-flash-low" {
		t.Errorf("Resolve(gemini-3.8-flash-low) = %q, want gemini-3.8-flash-low", r)
	}

	// 2. Simulated agy models CLI detection
	detectAgyModelsCmd = func(_ context.Context) ([]byte, error) {
		return []byte("gemini-3.8-flash-medium   Gemini 3.8 Flash (Medium)\nclaude-sonnet-4-6         Claude Sonnet 4.6 (Thinking)\n"), nil
	}
	ml = s.RunnerModels("agy")
	if ml.Source != "api" || len(ml.Models) != 2 {
		t.Fatalf("expected api detected agy models, got %+v", ml)
	}
	if ml.Models[0].ID != "gemini-3.8-flash-medium" || ml.Models[0].Name != "Gemini 3.8 Flash (Medium)" {
		t.Errorf("detected model 0 = %+v", ml.Models[0])
	}
}

func TestSetRunnerModels(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Service{DB: db}

	// Custom agy models setting
	if err := s.SetRunnerModels("agy", []string{"custom-model-1", "custom-model-2:Custom Two"}); err != nil {
		t.Fatal(err)
	}
	ml := s.RunnerModels("agy")
	if ml.Source != "setting" || len(ml.Models) != 2 {
		t.Fatalf("expected setting source, got %+v", ml)
	}
	if ml.Models[0].ID != "custom-model-1" || ml.Models[1].Name != "Custom Two" {
		t.Errorf("models = %+v", ml.Models)
	}

	// Reset
	if err := s.ResetRunnerModels("agy"); err != nil {
		t.Fatal(err)
	}
	ml = s.RunnerModels("agy")
	if ml.Source == "setting" {
		t.Fatalf("expected non-setting source after reset, got %+v", ml)
	}
}
