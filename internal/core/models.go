package core

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

// Model is a model supported by an agent runner backend.
type Model struct {
	ID     string `json:"id"`
	Tier   string `json:"tier"`           // family word in the id: haiku, sonnet, opus, ...
	Latest bool   `json:"latest"`         // newest id of its tier; what the tier alias resolves to
	Name   string `json:"name,omitempty"` // human-readable display name (e.g. Gemini 3.8 Flash (High))
}

// ModelList is the model catalogue. Source is "api", "setting", or "builtin".
type ModelList struct {
	Models []Model `json:"models"`
	Source string  `json:"source"`
	Runner string  `json:"runner,omitempty"`
}

// builtinClaudeModels is the fallback when no ANTHROPIC_API_KEY is set (or the API
// call fails), taken from the user's own account.
var builtinClaudeModels = []string{"claude-sonnet-5-5", "claude-opus-5-5", "claude-fable-5-1", "claude-haiku-5-5"}
var builtinModels = builtinClaudeModels

// builtinAgyModels is the fallback for the Antigravity runner when agy CLI detection fails.
var builtinAgyModels = []Model{
	{ID: "gemini-3.8-flash-high", Name: "Gemini 3.8 Flash (High)", Tier: "flash-high", Latest: true},
	{ID: "gemini-3.8-flash-medium", Name: "Gemini 3.8 Flash (Medium)", Tier: "flash", Latest: true},
	{ID: "gemini-3.8-flash-low", Name: "Gemini 3.8 Flash (Low)", Tier: "flash-low", Latest: true},
	{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6 (Thinking)", Tier: "sonnet", Latest: true},
	{ID: "claude-opus-4-6-thinking", Name: "Claude Opus 4.6 (Thinking)", Tier: "opus", Latest: true},
	{ID: "gpt-oss-120b-medium", Name: "GPT-OSS 120B (Medium)", Tier: "gpt-oss", Latest: true},
}

// detectAgyModelsCmd executes agy models (swappable in tests).
var detectAgyModelsCmd = func(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "agy", "models")
	return cmd.Output()
}

// RunnerModels returns the model catalog for the specified runner ("claude", "agy").
// If runner is empty, the default_runner setting (or "claude") is used.
func (s *Service) RunnerModels(runner string) ModelList {
	runner = strings.TrimSpace(runner)
	if runner == "" {
		if s != nil && s.DB != nil {
			runner = store.GetSetting(s.DB, "default_runner")
		}
		if runner == "" {
			runner = "claude"
		}
	}
	switch runner {
	case "agy":
		ml := s.agyModels()
		ml.Runner = "agy"
		return ml
	case "claude":
		ml := s.claudeModels()
		ml.Runner = "claude"
		return ml
	default:
		ml := s.claudeModels()
		ml.Runner = runner
		return ml
	}
}

// Models lists models for the default runner.
func (s *Service) Models() ModelList {
	return s.RunnerModels("")
}

func (s *Service) agyModels() ModelList {
	if s != nil && s.DB != nil {
		if custom := store.GetSetting(s.DB, "models_agy"); strings.TrimSpace(custom) != "" {
			return ModelList{Models: parseCustomModels(custom, "agy"), Source: "setting"}
		}
	}
	if models, err := detectAgyModels(); err == nil && len(models) > 0 {
		return ModelList{Models: models, Source: "api"}
	}
	return ModelList{Models: slices.Clone(builtinAgyModels), Source: "builtin"}
}

func detectAgyModels() ([]Model, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := detectAgyModelsCmd(ctx)
	if err != nil {
		return nil, err
	}
	return parseAgyModelsOutput(string(out)), nil
}

func parseAgyModelsOutput(out string) []Model {
	var models []Model
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Available") || strings.HasPrefix(line, "Fetching") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := fields[0]
		name := strings.TrimSpace(strings.TrimPrefix(line, id))
		if name == "" {
			name = id
		}
		models = append(models, Model{
			ID:     id,
			Name:   name,
			Tier:   agyModelTier(id),
			Latest: true,
		})
	}
	return models
}

func agyModelTier(id string) string {
	lower := strings.ToLower(id)
	switch {
	case strings.Contains(lower, "sonnet"):
		return "sonnet"
	case strings.Contains(lower, "opus"):
		return "opus"
	case strings.Contains(lower, "flash-high"):
		return "flash-high"
	case strings.Contains(lower, "flash-low"):
		return "flash-low"
	case strings.Contains(lower, "flash") || strings.Contains(lower, "haiku"):
		return "flash"
	case strings.Contains(lower, "gpt"):
		return "gpt-oss"
	default:
		return id
	}
}

func (s *Service) claudeModels() ModelList {
	if s != nil && s.DB != nil {
		if custom := store.GetSetting(s.DB, "models_claude"); strings.TrimSpace(custom) != "" {
			return ModelList{Models: parseCustomModels(custom, "claude"), Source: "setting"}
		}
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		if ids, err := apiModels(cmp.Or(os.Getenv("ANTHROPIC_BASE_URL"), "https://api.anthropic.com"), key); err == nil && len(ids) > 0 {
			return ModelList{Models: classify(ids), Source: "api"}
		}
	}
	return ModelList{Models: classify(builtinClaudeModels), Source: "builtin"}
}

func parseCustomModels(raw, runner string) []Model {
	var models []Model
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	})
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id := p
		name := ""
		if idx := strings.Index(p, ":"); idx != -1 {
			id = strings.TrimSpace(p[:idx])
			name = strings.TrimSpace(p[idx+1:])
		}
		tier := ""
		if runner == "agy" {
			tier = agyModelTier(id)
		} else {
			tier = modelTier(id)
		}
		models = append(models, Model{
			ID:     id,
			Name:   name,
			Tier:   tier,
			Latest: true,
		})
	}
	return models
}

// SetRunnerModels sets custom models for a runner backend (e.g. "claude", "agy").
func (s *Service) SetRunnerModels(runner string, models []string) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("no database")
	}
	runner = strings.TrimSpace(runner)
	if runner == "" {
		runner = store.GetSetting(s.DB, "default_runner")
		if runner == "" {
			runner = "claude"
		}
	}
	key := "models_" + runner
	return store.SetSetting(s.DB, key, strings.Join(models, ", "))
}

// ResetRunnerModels clears custom models for a runner backend.
func (s *Service) ResetRunnerModels(runner string) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("no database")
	}
	runner = strings.TrimSpace(runner)
	if runner == "" {
		runner = store.GetSetting(s.DB, "default_runner")
		if runner == "" {
			runner = "claude"
		}
	}
	key := "models_" + runner
	return store.SetSetting(s.DB, key, "")
}

// apiModels lists model ids from the Anthropic Models API.
func apiModels(base, key string) ([]string, error) {
	req, err := http.NewRequest("GET", strings.TrimRight(base, "/")+"/v1/models?limit=1000", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("models API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models API: %s", resp.Status)
	}
	return parseAPIModels(resp.Body)
}

func parseAPIModels(r io.Reader) ([]string, error) {
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, fmt.Errorf("models API: %w", err)
	}
	var ids []string
	for _, d := range body.Data {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

// modelTier is the first non-numeric segment after "claude-": claude-opus-5-5
// and claude-3-5-sonnet-20241022 give opus and sonnet.
func modelTier(id string) string {
	for _, p := range strings.Split(strings.TrimPrefix(id, "claude-"), "-") {
		if _, err := strconv.Atoi(p); err != nil {
			return p
		}
	}
	return ""
}

// versionKey orders ids by their version numbers, then by date suffix.
func versionKey(id string) []int {
	var v []int
	date := 0
	for _, p := range strings.Split(id, "-") {
		if n, err := strconv.Atoi(p); err == nil {
			if len(p) == 8 {
				date = n
			} else {
				v = append(v, n)
			}
		}
	}
	return append(v, date)
}

// classify sorts ids by tier then newest first and marks the latest per tier.
func classify(ids []string) []Model {
	var ms []Model
	for _, id := range ids {
		if t := modelTier(id); t != "" && strings.HasPrefix(id, "claude-") {
			ms = append(ms, Model{ID: id, Tier: t})
		}
	}
	slices.SortFunc(ms, func(a, b Model) int {
		return cmp.Or(cmp.Compare(a.Tier, b.Tier), slices.Compare(versionKey(b.ID), versionKey(a.ID)), cmp.Compare(a.ID, b.ID))
	})
	for i := range ms {
		ms[i].Latest = i == 0 || ms[i-1].Tier != ms[i].Tier
	}
	return ms
}

var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// Resolve maps a tier alias to its latest id; ids (with an optional [1m]
// suffix, or without their -YYYYMMDD date) resolve to the detected id.
// "" when m is unknown or detection failed.
func (ml ModelList) Resolve(m string) string {
	base := strings.TrimSpace(strings.TrimSuffix(m, "[1m]"))
	if base == "" {
		return ""
	}
	for _, x := range ml.Models {
		if strings.EqualFold(x.ID, base) || (x.Name != "" && strings.EqualFold(x.Name, base)) {
			return x.ID
		}
	}
	for _, x := range ml.Models {
		if dateSuffix.ReplaceAllString(x.ID, "") == base || (x.Latest && strings.EqualFold(x.Tier, base)) {
			return x.ID
		}
	}
	lower := strings.ToLower(base)
	switch lower {
	case "sonnet":
		for _, x := range ml.Models {
			if strings.Contains(strings.ToLower(x.ID), "sonnet") {
				return x.ID
			}
		}
	case "opus":
		for _, x := range ml.Models {
			if strings.Contains(strings.ToLower(x.ID), "opus") {
				return x.ID
			}
		}
	case "haiku":
		for _, x := range ml.Models {
			if strings.Contains(strings.ToLower(x.ID), "flash") || strings.Contains(strings.ToLower(x.ID), "haiku") {
				return x.ID
			}
		}
	case "flash":
		for _, x := range ml.Models {
			if strings.Contains(strings.ToLower(x.ID), "flash-medium") || strings.Contains(strings.ToLower(x.ID), "flash") {
				return x.ID
			}
		}
	}
	return ""
}
