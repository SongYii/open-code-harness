// Package contextengine implements the pure projection, metering, planning,
// checkpoint validation, and materialization logic for the Context Engine
// (docs/superpowers/specs/2026-09-01-context-engine-design.md, CE-01).
//
// This package imports internal/harness/domain and the Go standard library
// only. It must never import internal/harness/application, any adapter
// package, SQLite, ACP, the filesystem, a clock, a randomness source, a
// logger, or a Provider SDK — those are internal/harness/application's
// concerns (CE-02: compaction lifecycle, Provider calls, CAS appends,
// cancellation, overflow retry).
//
// internal/harness/architecture's dependency-boundary test
// (TestProductionDependencyBoundaries, TestOnlyCompositionAndRuntimeMayNameAnAdapter)
// registers and enforces this boundary as an owned package once this
// package has a real caller (implementation plan Task 7); until then it is
// checked only under that test's more permissive "unowned package" rules,
// which already forbid an adapter, testkit, or transcript import.
package contextengine
