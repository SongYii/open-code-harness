// Package application defines the use-case boundary around the deterministic
// domain model. It owns the ports implemented by persistence and runtime
// adapters; adapters must not decide domain transitions or retry commands.
//
// Application reconstructs authoritative state by replaying the complete event
// stream returned by EventStore.Load. This milestone intentionally has no
// snapshot port. A future snapshot may only be a discardable projection or
// cache and must never determine append acceptance, recorded sequence, or the
// authoritative stream version.
package application
