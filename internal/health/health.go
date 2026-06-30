// Package health provides the HTTP health-check handler.
package health

import (
	"encoding/json"
	"net/http"
)

// Handler responds with service health for liveness/readiness probes.
func Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
