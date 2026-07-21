# Argus — Technical Design

> Companion to `PRD.md`. Describes architecture, components, data flow, and how to run it locally with zero cloud spend.

---

## 1. Tech Stack

| Concern | Choice | Why |
|--------|--------|-----|
| Language | **Go** | Great for a small, fast webhook server; strong GitHub ecosystem (`go-github`); single static binary for k8s. |
| HTTP router | `chi` (or stdlib `net/http`) | Lightweight, idiomatic. |
| GitHub client | `google/go-github` + `bradleyfalzon/ghinstallation` | App auth + installation tokens handled for you. |
| LLM (local) | **Ollama** (e.g. `qwen2.5-coder`, `llama3.1`) | Runs free on localhost; good at code. |
| Persistence (optional) | **Postgres** | Only if review-history/feedback is enabled (M4). |
| Cache / dedup | **Redis** | Delivery-ID dedup, simple rate limiting. |
| Deploy | Docker + k8s manifests, **kind** | No cloud cost; lighter than minikube (runs nodes as containers). |
| Local webhook tunnel | `smee.io` client or `cloudflared` | Expose localhost to GitHub during dev. |

## 2. High-Level Architecture

```
                 ┌──────────────┐   pull_request / issue_comment
   GitHub  ─────▶│ Webhook HTTP │◀── (HMAC-signed)
                 │   server     │
                 └──────┬───────┘
                        │ verify sig + dedup (Redis)
                        ▼
                 ┌──────────────┐     ┌────────────────┐
                 │  Review      │────▶│ GitHub API     │ fetch diff, post comments
                 │  Orchestrator│◀────│ (installation  │
                 └──────┬───────┘     │  token auth)   │
                        │             └────────────────┘
                        │ chunked diff + prompt
                        ▼
                 ┌──────────────┐
                 │ LLM Provider │  (interface)
                 │  └ Ollama    │  ── pluggable: OpenAI / Bedrock adapters
                 └──────────────┘
```

## 3. Components

### 3.1 Webhook Server
- Single endpoint `POST /webhooks/github`.
- Verifies `X-Hub-Signature-256` (HMAC-SHA256 with the app webhook secret).
- Dedupes on `X-GitHub-Delivery` via Redis `SETNX` with TTL.
- Parses event type from `X-GitHub-Event`; routes `pull_request` and `issue_comment` to handlers, ACKs everything else with 200.
- **Responds 200 immediately**, then processes asynchronously (goroutine + bounded worker pool) so GitHub doesn't time out.

### 3.2 GitHub Client Layer
- App-level JWT signs requests to mint **installation tokens** per repo install.
- Wraps: get PR, get PR diff/files, create a **review** with inline comments, create/update an **issue comment** (the summary).
- Inline comments use the GitHub "review comment" API which anchors to a `path` + `line` (or `position` in the diff). **Validate anchoring early** — it's the trickiest integration detail.

### 3.3 Diff Parser
- Pulls changed files + unified diff.
- Filters out paths matching `.argus.yml` ignore globs and binary/lockfiles.
- Splits into units: one per file; if a file's hunks exceed a token budget, split per hunk.
- Each unit carries enough context (surrounding lines + file path) for the LLM to reason.

### 3.4 LLM Provider (interface)
```go
type Provider interface {
    Review(ctx context.Context, unit DiffUnit, cfg ReviewConfig) ([]Finding, error)
}
```
- `OllamaProvider` is the default. `OpenAIProvider` / `BedrockProvider` implement the same interface (M4).
- Prompt asks for **JSON-only** structured output; the response is parsed and validated. Invalid JSON triggers one repair retry, then the unit is skipped (logged).

```go
type Finding struct {
    File       string `json:"file"`
    Line       int    `json:"line"`
    Severity   string `json:"severity"`   // info | warning | error
    Category   string `json:"category"`   // bug | security | performance | style | maintainability
    Message    string `json:"message"`
    Suggestion string `json:"suggestion,omitempty"`
}
```

### 3.5 Review Orchestrator
- Fan-out the diff units to the provider with a bounded concurrency (e.g. 4) to keep local Ollama responsive.
- Aggregate findings, apply severity floor + per-PR comment cap from config.
- Dedup against existing Argus comments (match on file+line+message hash) so re-reviews don't repeat.
- Post inline comments as a single GitHub *review*, then upsert the summary comment.

### 3.6 Config Loader
- Reads `.argus.yml` from the PR's head ref (falls back to defaults).
```yaml
# .argus.yml
min_severity: warning
categories: [bug, security, performance, style, maintainability]
ignore:
  - "**/*.lock"
  - "vendor/**"
  - "**/*_generated.go"
max_files: 25
max_comments: 15
persona: "concise senior engineer"
```

## 4. Data Model (only if M4 history enabled)
```
reviews(id, repo, pr_number, head_sha, created_at, findings_count, latency_ms)
findings(id, review_id, file, line, severity, category, message, feedback) -- feedback: 👍/👎
```
Default build keeps Argus **stateless** apart from Redis dedup keys.

## 5. Local Development Flow
1. `ollama pull qwen2.5-coder` and run `ollama serve`.
2. Register a GitHub App (dev): set webhook URL to your `smee.io` channel, grant `pull_requests: write`, `contents: read`, `issues: write`. Subscribe to `pull_request` + `issue_comment`.
3. Run the smee client to forward to `localhost:8080/webhooks/github`.
4. `docker compose up` (server + Redis), install the App on a throwaway test repo, open a PR, watch comments appear.

## 6. Deployment (kind)
- Multi-stage `Dockerfile` → tiny distroless image.
- `k8s/`: `Deployment`, `Service`, `Secret` (app id, private key, webhook secret), optional `Ingress`.
- Flow: `kind create cluster --name dev` → `docker build -t argus:latest .` → `kind load docker-image argus:latest --name dev` → `kubectl apply -k k8s/`.
- **Note:** kind does not share the host Docker daemon, so images must be explicitly loaded with `kind load docker-image` after each rebuild (then `kubectl rollout restart` to pick them up). Manifests use `imagePullPolicy: IfNotPresent` so the loaded local image is used.
- For live GitHub delivery, `kubectl port-forward` the service and point the smee/cloudflared tunnel at that local port.

## 7. Observability
- Structured JSON logs (zerolog/slog) with delivery ID + repo + PR as fields.
- Metrics (Prometheus): `argus_reviews_total`, `argus_findings_total{category}`, `argus_llm_errors_total`, review latency histogram.

## 8. Testing Strategy
- **Unit:** signature verification, diff parsing, finding dedup, config merge.
- **Provider:** a `FakeProvider` returns canned findings so orchestration is testable without an LLM.
- **Integration:** record/replay GitHub API responses (golden fixtures) for the comment-posting path.
- **Manual eval:** the 20-PR usefulness sample described in the PRD.

## 9. Risks & Mitigations
| Risk | Mitigation |
|------|-----------|
| Inline-comment anchoring wrong | Spike it in M1 against a real PR before building the rest. |
| Local LLM too slow on big PRs | Bounded concurrency + per-PR file cap + hunk splitting. |
| Noisy/low-quality findings | Severity floor, comment cap, prompt tuning, persona config. |
| Webhook timeouts | ACK 200 immediately, process async. |
