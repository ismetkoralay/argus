// Package llm implements review.Provider against locally-hosted LLM
// backends, starting with Ollama.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ismetkoralay/argus/internal/logging"
	"github.com/ismetkoralay/argus/internal/review"
)

var allowedSeverities = []string{"info", "warning", "error"}

var allowedCategories = []string{"bug", "security", "performance", "style", "maintainability"}

// OllamaProvider implements review.Provider against an Ollama server's
// /api/generate endpoint, asking for JSON-only output and repairing once
// on invalid JSON before giving up.
type OllamaProvider struct {
	baseURL    string
	model      string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewOllamaProvider builds an OllamaProvider. httpClient and logger default
// to http.DefaultClient and slog.Default() when nil.
func NewOllamaProvider(baseURL, model string, httpClient *http.Client, logger *slog.Logger) *OllamaProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &OllamaProvider{baseURL: baseURL, model: model, httpClient: httpClient, logger: logger}
}

type generateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Format string `json:"format"`
	Stream bool   `json:"stream"`
}

type generateResponse struct {
	Response string `json:"response"`
}

// Review implements review.Provider.
func (p *OllamaProvider) Review(ctx context.Context, unit review.DiffUnit, cfg review.Config) ([]review.Finding, error) {
	logger := logging.FromContext(ctx, p.logger)

	prompt := buildPrompt(unit, cfg)
	// Debug-only: the prompt embeds the full diff hunk, so this must never
	// log at default level. Suppressed unless LOG_LEVEL=debug.
	logger.Debug("ollama request", "file", unit.File, "prompt", prompt)

	raw, err := p.generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("ollama generate: %w", err)
	}
	logger.Debug("ollama response", "file", unit.File, "raw", raw)

	findings, parseErr := p.parseAndValidate(ctx, raw)
	if parseErr == nil {
		return findings, nil
	}
	logger.Warn("ollama returned invalid findings JSON, retrying with repair prompt", "err", parseErr)

	repairPrompt := buildRepairPrompt(unit, cfg, raw, parseErr)
	logger.Debug("ollama repair request", "file", unit.File, "prompt", repairPrompt)

	raw, err = p.generate(ctx, repairPrompt)
	if err != nil {
		return nil, fmt.Errorf("ollama generate (repair retry): %w", err)
	}
	logger.Debug("ollama repair response", "file", unit.File, "raw", raw)

	findings, parseErr = p.parseAndValidate(ctx, raw)
	if parseErr != nil {
		return nil, fmt.Errorf("parse ollama findings after repair retry: %w", parseErr)
	}
	return findings, nil
}

func (p *OllamaProvider) generate(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(generateRequest{
		Model:  p.model,
		Prompt: prompt,
		Format: "json",
		Stream: false,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, respBody)
	}

	var gr generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	return gr.Response, nil
}

// parseAndValidate parses raw as findings (see unmarshalFindings for the
// shapes accepted), dropping (and logging) any individual finding that
// fails validation. A JSON syntax/shape error is returned so the caller
// can trigger a repair retry; individual finding validation failures are
// not treated as parse errors.
func (p *OllamaProvider) parseAndValidate(ctx context.Context, raw string) ([]review.Finding, error) {
	candidates, err := unmarshalFindings(raw)
	if err != nil {
		return nil, err
	}

	logger := logging.FromContext(ctx, p.logger)
	findings := make([]review.Finding, 0, len(candidates))
	for _, f := range candidates {
		if reason, ok := validateFinding(f); !ok {
			logger.Warn("dropping invalid finding", "reason", reason, "finding", f)
			continue
		}
		findings = append(findings, f)
	}
	return findings, nil
}

// unmarshalFindings accepts the findings shapes Ollama's format:"json" mode
// actually produces: the requested {"findings": [...]} object (its
// structural bias when asked for JSON), a bare array (in case a model
// returns one anyway), or a single bare finding object (when a model
// collapses a one-element result). It tries each in turn and returns the
// bare-array error if none match, since that's the schema we ultimately
// need.
func unmarshalFindings(raw string) ([]review.Finding, error) {
	var wrapper struct {
		Findings []review.Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err == nil && wrapper.Findings != nil {
		return wrapper.Findings, nil
	}

	var arr []review.Finding
	arrErr := json.Unmarshal([]byte(raw), &arr)
	if arrErr == nil {
		return arr, nil
	}

	var single review.Finding
	if err := json.Unmarshal([]byte(raw), &single); err == nil {
		return []review.Finding{single}, nil
	}

	return nil, arrErr
}

func validateFinding(f review.Finding) (string, bool) {
	switch {
	case f.File == "":
		return "empty file", false
	case f.Line < 1:
		return "line < 1", false
	case !slices.Contains(allowedSeverities, f.Severity):
		return "invalid severity", false
	case !slices.Contains(allowedCategories, f.Category):
		return "invalid category", false
	case f.Message == "":
		return "empty message", false
	default:
		return "", true
	}
}

// hunkHeaderRe matches a unified diff hunk header and captures the new
// (right-side) file's starting line number, e.g. "@@ -57,5 +66,5 @@" -> 66.
var hunkHeaderRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// annotateHunk prefixes each line of a unified diff hunk with its exact
// line number in the current (new) version of the file, so the model
// copies a number instead of computing one from the diff arithmetic
// itself — small models are unreliable at that arithmetic. The original
// "@@ -a,b +c,d @@" header is replaced entirely (not just supplemented):
// leaving the old-file number "a" visible was enough for models to latch
// onto it instead of reading the per-line annotations.
func annotateHunk(hunk string) string {
	lines := strings.Split(hunk, "\n")
	out := make([]string, 0, len(lines))
	newLine := 0

	for _, line := range lines {
		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			newLine, _ = strconv.Atoi(m[1])
			out = append(out, fmt.Sprintf("--- hunk, starting at file line %d ---", newLine))
			continue
		}

		switch {
		case strings.HasPrefix(line, "+"):
			out = append(out, fmt.Sprintf("[%d] %s", newLine, line))
			newLine++
		case strings.HasPrefix(line, "-"):
			out = append(out, fmt.Sprintf("[removed, not in current file] %s", line))
		default:
			out = append(out, fmt.Sprintf("[%d] %s", newLine, line))
			newLine++
		}
	}
	return strings.Join(out, "\n")
}

func buildPrompt(unit review.DiffUnit, cfg review.Config) string {
	var b strings.Builder
	fmt.Fprint(&b, "You are an automated code reviewer")
	if cfg.Persona != "" {
		fmt.Fprintf(&b, " with this persona: %s", cfg.Persona)
	}
	fmt.Fprintf(&b, ".\n\n")
	fmt.Fprintf(&b, "Review the following diff hunk from file %q and report findings.\n", unit.File)
	fmt.Fprint(&b, "Each line is prefixed with its exact line number in the CURRENT version of the file, in square brackets. ")
	fmt.Fprint(&b, "Use that EXACT number for your finding's \"line\" field — do not compute or guess a line number yourself. ")
	fmt.Fprint(&b, "Lines marked \"[removed, not in current file]\" no longer exist; if a finding is about a removed line, attribute it to the nearest line that still exists (e.g. the line that replaced it).\n")
	fmt.Fprint(&b, "Respond with ONLY a JSON object (no prose, no markdown fences) matching this schema:\n")
	fmt.Fprint(&b, `{"findings": [{"file": string, "line": number, "severity": "info"|"warning"|"error", "category": "bug"|"security"|"performance"|"style"|"maintainability", "message": string, "suggestion": string (optional)}]}`+"\n")
	fmt.Fprint(&b, `If there are no findings, respond with {"findings": []}`+"\n\n")
	fmt.Fprint(&b, "Diff:\n")
	fmt.Fprint(&b, annotateHunk(unit.Hunk))
	return b.String()
}

func buildRepairPrompt(unit review.DiffUnit, cfg review.Config, invalidOutput string, parseErr error) string {
	var b strings.Builder
	fmt.Fprint(&b, buildPrompt(unit, cfg))
	fmt.Fprint(&b, "\n\nYour previous response was not valid JSON matching the schema above.\n")
	fmt.Fprintf(&b, "Previous response:\n%s\n", invalidOutput)
	fmt.Fprintf(&b, "Parse error: %s\n", parseErr.Error())
	fmt.Fprint(&b, "Respond again with ONLY a corrected JSON array matching the schema.")
	return b.String()
}
