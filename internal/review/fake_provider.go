package review

import (
	"context"
	"sync"
)

// FakeProvider is a test double satisfying Provider. It returns Findings
// (optionally via FindingsFunc, for per-call behavior) and records every
// unit it was asked to review.
type FakeProvider struct {
	// Findings is returned for every call when FindingsFunc is nil.
	Findings []Finding
	// FindingsFunc, if set, is called instead of returning Findings,
	// letting tests vary the response or return an error per unit.
	FindingsFunc func(unit DiffUnit) ([]Finding, error)

	mu      sync.Mutex
	Calls   []DiffUnit
	Configs []Config
}

// Review implements Provider.
func (f *FakeProvider) Review(_ context.Context, unit DiffUnit, cfg Config) ([]Finding, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, unit)
	f.Configs = append(f.Configs, cfg)
	f.mu.Unlock()

	if f.FindingsFunc != nil {
		return f.FindingsFunc(unit)
	}
	return f.Findings, nil
}
