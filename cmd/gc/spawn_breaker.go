package main

import (
	"encoding/json"

	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/resilience"
)

// newSpawnBreakerRegistry builds the per-city, in-memory registry of spawn
// breakers, one per agent template (internal/resilience.OpClassSpawn). A
// breaker trips after repeated spawn failures for its template — regardless
// of cause — and backs off exponentially instead of retrying at the tick
// cadence; a subsequent successful spawn closes it again. Every state
// transition is emitted as a typed events.PoolSpawnBackoff event via rec, so
// backoff is observable without reading session directories off disk.
func newSpawnBreakerRegistry(rec events.Recorder) *resilience.Registry {
	registry := resilience.NewRegistry(resilience.DefaultSettings())
	registry.SetOnStateChange(func(t resilience.Transition) {
		emitSpawnBackoffEvent(rec, t)
	})
	return registry
}

// emitSpawnBackoffEvent records a spawn breaker state transition as a typed
// pool.spawn_backoff event. Best-effort: a marshal failure or nil recorder
// drops the event rather than blocking the transition it describes.
func emitSpawnBackoffEvent(rec events.Recorder, t resilience.Transition) {
	if rec == nil {
		return
	}
	payload, err := json.Marshal(events.PoolSpawnBackoffPayload{
		Template:            t.Scope,
		FromState:           t.From.String(),
		ToState:             t.To.String(),
		ConsecutiveFailures: t.Failures,
		BackoffSeconds:      t.Backoff.Seconds(),
	})
	if err != nil {
		return
	}
	rec.Record(events.Event{
		Type:    events.PoolSpawnBackoff,
		Actor:   "gc",
		Subject: t.Scope,
		Payload: payload,
	})
}
