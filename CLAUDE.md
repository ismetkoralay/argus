# CLAUDE.md — Argus

This file orients **Claude Code** when working in this repository. Keep it at the repo root. Claude Code reads it automatically.

---

## What this project is
Argus is an AI code-review GitHub App written in **Go**. It receives PR webhooks, sends the diff to a pluggable LLM (local Ollama by default), and posts inline + summary review comments. See `PRD.md` for product scope and `TECH_DESIGN.md` for architecture.

## Tech & conventions
- **Language:** Go (latest stable). Module path `github.com/<you>/argus`.
- **Layout:** `cmd/argus` (main), `internal/github`, `internal/review`, `internal/llm`, `internal/config`, `internal/webhook`.
- **Style:** idiomatic Go. `gofmt`/`goimports` clean. Wrap errors with `%w`. No naked `panic` outside `main`. Context-first function signatures.
- **No global state** except wiring in `main`. Pass dependencies explicitly (constructor injection).
- **Interfaces at the consumer**, not the provider package.

## Commands
```bash
make run            # run server locally (expects Ollama + Redis up)
make test           # go test ./...
make lint           # golangci-lint run
docker compose up   # server + redis for local dev
ollama serve        # local LLM backend
```
*(If `make`/`docker compose` don't exist yet, create them as the first task.)*

## How I want you (Claude Code) to work here

1. **Work milestone by milestone** following `PRD.md` §8 (M0 → M4). Do not jump ahead. Finish and test one milestone before starting the next.
2. **Plan before coding.** For each milestone, first write/update a short `PLAN.md` section: files to touch, interfaces, and the test you'll write. Wait for my OK on anything that changes architecture.
3. **Test as you go.** Every new package gets table-driven tests. Use the `FakeProvider` for orchestration tests — never require a live LLM in unit tests.
4. **Small, reviewable commits.** One logical change per commit, conventional-commit messages (`feat:`, `fix:`, `test:`, `chore:`).
5. **Spike the risky bit first.** In M1, before building the orchestrator, prove inline-comment anchoring works against one real PR. Flag me if the GitHub API behaves unexpectedly.
6. **Secrets never committed.** App private key, webhook secret, and tokens come from env/k8s Secrets only. Never log them.

## Definition of done (per milestone)
- [ ] Code compiles, `make lint` clean.
- [ ] Tests added and `make test` green.
- [ ] `README.md` updated if behavior changed.
- [ ] Manual smoke test against the throwaway test repo passes.

## Good first prompts to give me
- "Scaffold M0: webhook server with HMAC verification + GitHub App auth, post a static comment on PR opened. Add Makefile, Dockerfile, docker-compose with Redis."
- "Implement the LLM Provider interface and OllamaProvider with JSON-structured output + one repair retry. Add a FakeProvider and unit tests."
- "Build the diff parser: fetch PR files, apply ignore globs, split into per-file/per-hunk units with context."
- "Wire the orchestrator: fan-out to provider (concurrency 4), aggregate, apply severity floor + comment cap, post a review."

## Things to avoid
- Don't add a database until M4 — the MVP is stateless apart from Redis.
- Don't pull in a heavyweight framework; stdlib + chi is enough.
- Don't expand scope into CI gating or auto-merge (explicit non-goals).
- Don't silently swallow LLM/JSON errors — log and skip the unit.

## Final step — generate the README (do this LAST, from real code)

Do NOT write the public README until the project is feature-complete. A README written
early describes code that doesn't exist yet and quietly lies — and it's the most-read file
in the repo. Keep the skeleton README updated as you build; replace it with the real one
only at the end.

When the project is done, run this exact request:

> Rewrite README.md based ONLY on what actually exists in this repository. Read the code,
> the Makefile, the compose file, and the k8s manifests first. Use this section order:
> 1. One-line description.
> 2. The problem it solves (2–3 sentences, no marketing language).
> 3. Architecture diagram (mermaid) reflecting the real components.
> 4. Quick start — VERIFY every command by running it. The GitHub App webhook flow can't be
>    fully exercised in CI, so document the `smee.io` + throwaway-test-repo setup instead of
>    claiming an automated end-to-end.
> 5. Key design decisions & trade-offs (leave a TODO marker for me to refine).
> 6. Local development.
> 7. Deploy to kind (include the `kind load docker-image` step — kind does not share the host Docker daemon).
> Rules: no emoji, no exhaustive feature list, no filler. Every command must work as written.
> If something in the skeleton README no longer matches the code, fix it to match reality.

After it runs:
- Manually verify the quick-start yourself once more (the highest-ROI check).
- Hand-write the "design decisions & trade-offs" section — that's the senior signal an
  interviewer probes, so it must reflect MY reasoning. For Argus the decisions worth
  defending are the diff-chunking strategy and inline-comment anchoring against the diff.
- Add a demo GIF near the top (a PR getting reviewed).
