# Argus — Product Requirements Document

> **One-liner:** An AI-powered GitHub App that performs an automated first-pass review on every pull request — inline comments plus a summary — using a pluggable LLM backend that runs fully on local models.

---

## 1. Background & Motivation

Pull request review is a bottleneck on almost every team. Reviews are slow, inconsistent between reviewers, and small teams (or solo OSS maintainers) often have no second reviewer at all. A lot of the feedback that *does* get written is mechanical: "this function is too long", "you swallowed this error", "missing null check", "this looks like a SQL injection".

Argus automates that mechanical first pass. It is **not** a replacement for human review — it is the reviewer that always shows up, never gets tired, and catches the obvious stuff before a human spends time on it.

This project is a strong portfolio piece because:
- It is a **real, installable GitHub App** (webhooks, App auth, the GitHub API) — recruiters recognize this immediately.
- It is **contained in scope** but touches event-driven architecture, third-party API integration, and LLM orchestration.
- It demonstrates **AI product sense**, not just "I called an LLM".

## 2. Goals & Non-Goals

### Goals
- Listen to PR lifecycle events and review the diff automatically.
- Post **inline review comments** anchored to specific lines, plus a **summary comment**.
- Re-review when new commits are pushed to an open PR.
- Be configurable per-repo via a committed `.argus.yml`.
- Run end-to-end on **local LLMs (Ollama)** with zero cloud cost, while keeping the provider pluggable.

### Non-Goals (v1)
- Replacing human reviewers or approving/merging PRs automatically.
- Acting as a CI gate that blocks merges (stretch goal, see §8).
- Multi-language deep static analysis — Argus reasons over diffs with an LLM, it is not a compiler/linter.
- A hosted SaaS with billing. This is a self-hostable app.

## 3. Target Users & Use Cases

| User | Use case |
|------|----------|
| Small product team (2–5 devs) | Gets a consistent first-pass review even when the one senior engineer is on vacation. |
| Solo OSS maintainer | Gets help triaging drive-by contributor PRs. |
| Individual learning | Self-reviews their own PRs before asking a human. |

**Primary scenario:** A developer opens a PR. Within ~30s Argus posts a summary ("3 issues found: 1 potential bug, 2 style") and 3 inline comments. The developer fixes two, pushes. Argus re-reviews and resolves its stance.

## 4. Functional Requirements

### 4.1 Installation & Auth
- FR-1: Argus is installable as a GitHub App on selected repositories.
- FR-2: It authenticates as a GitHub App (JWT) and exchanges for short-lived installation tokens to act on each repo.

### 4.2 Event Handling
- FR-3: On `pull_request` `opened` and `synchronize`, Argus fetches the PR diff.
- FR-4: On `issue_comment` containing the command `/argus review`, Argus re-runs a review on demand.
- FR-5: Webhook payloads are signature-verified (HMAC) and deduplicated by delivery ID.

### 4.3 Review Generation
- FR-6: The diff is split into per-file (and, for large files, per-hunk) units before being sent to the LLM.
- FR-7: The LLM returns **structured** findings: `{file, line, severity, category, message, suggestion?}`.
- FR-8: Findings are posted as inline review comments anchored to the correct line in the diff.
- FR-9: A single summary comment aggregates counts by severity/category and gives an overall verdict (e.g. "looks good", "needs attention").
- FR-10: Categories include at minimum: `bug`, `security`, `performance`, `style`, `maintainability`.

### 4.4 Configuration
- FR-11: A repo may include `.argus.yml` to set: ignored paths/globs, minimum severity to post, enabled categories, max files reviewed, and the review "persona"/tone.
- FR-12: Sensible defaults apply when no config is present.

### 4.5 Idempotency & Noise Control
- FR-13: Re-reviewing a PR must not duplicate previously posted comments for unchanged lines.
- FR-14: A configurable cap limits total comments per PR to avoid spamming.

## 5. Non-Functional Requirements
- **Latency:** First feedback on a typical (<300 line) PR within ~30s of the webhook.
- **Cost:** $0 to run locally on Ollama. Provider swap must not require code changes beyond config.
- **Reliability:** A failed LLM call retries with backoff; a hard failure posts a graceful "review unavailable" status rather than crashing.
- **Security:** Webhook secret verification mandatory. Installation tokens never logged. No source code is persisted beyond the review request unless review-history is explicitly enabled.
- **Observability:** Structured logs + basic metrics (reviews processed, latency, findings per PR, LLM errors).

## 6. User Experience

**Summary comment (example shape):**
> 🛡️ **Argus review** — 3 findings (1 bug, 2 style) across 4 files. Overall: needs attention.
> Reviewed 4/4 changed files. Configure me in `.argus.yml`.

**Inline comment (example shape):**
> ⚠️ **[bug]** This error is assigned but never checked. If `db.Query` fails, `rows` is nil and the loop below panics.
> *Suggestion:* return early on `err != nil`.

## 7. Success Metrics
- % of posted findings a human marks resolved/useful (manual eval on a sample of 20 PRs).
- Median webhook→first-comment latency.
- False-positive rate (findings dismissed as wrong) kept under a target threshold.

## 8. Milestones

| Milestone | Scope |
|-----------|-------|
| **M0 – Skeleton** | Webhook server, signature verification, GitHub App auth, "hello PR" comment on `opened`. |
| **M1 – Core review (MVP)** | Diff fetch + parse, LLM provider (Ollama), structured findings, inline + summary comments. |
| **M2 – Config & polish** | `.argus.yml`, severity filtering, dedup on re-review, comment caps, `/argus review` command. |
| **M3 – Ops** | Dockerfile, k8s manifests, kind deploy, metrics + structured logs, README with demo gif. |
| **M4 – Stretch** | Optional Postgres review history; CI "check run" status; provider fallback (Bedrock/OpenAI adapter); per-finding 👍/👎 feedback loop. |

## 9. Open Questions
- Inline-comment anchoring against the diff position can be fiddly with the GitHub API — validate the approach early in M1.
- Should very large PRs be summarized at a higher level instead of per-line? (Likely yes, gate behind a line-count threshold.)
