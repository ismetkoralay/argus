---
name: add-endpoint
description: Scaffold a new HTTP endpoint in this Go service following repo conventions (handler in internal/, route registration in main, table-driven test). Use when adding an API route.
---
# Add an HTTP endpoint

Follow these steps exactly; match existing style in `internal/health`.

1. **Handler**: create `internal/<feature>/<feature>.go` with an exported `func Handler(w http.ResponseWriter, r *http.Request)` (or a constructor returning `http.Handler` if it needs dependencies). Validate input, set `Content-Type`, write the status explicitly, encode JSON.
2. **Wire the route** in `cmd/service/main.go` using the method-prefixed pattern, e.g. `mux.HandleFunc("POST /v1/<thing>", feature.Handler)`.
3. **Test**: add `internal/<feature>/<feature>_test.go` with a table-driven test using `httptest`. Cover the happy path, a validation failure, and any error branch.
4. **Errors**: never swallow errors; wrap with `%w`. Return the right status code (400 for bad input, 500 for internal).
5. **Run** `make test` and `make lint` before declaring done.

Output a short summary of files touched and the new route.
