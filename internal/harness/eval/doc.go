// Package eval is the milestone 10 evaluation subsystem: it runs real OCH
// Sessions, freezes experiment identity, preserves bounded evidence, and
// scores that evidence without requiring the Subject to run again. The
// accepted normative design is
// docs/superpowers/specs/2026-09-02-evaluation-design.md.
//
// This is the identity foundation slice only: the frozen Scenario, Subject,
// and Executor documents (design §7, §10, §11) and their canonical digests
// (design §6), including each Scenario action's stable ID coordinate, its
// restart mode, and the frozen approval script both executors will compile
// into their permission handling. It is not yet a runner — there is no
// EvalSet, Attempt store, executor, or CLI here. Those build on top of the
// identities this package defines and land as separate, independently
// reviewable pieces, per
// docs/superpowers/plans/2026-09-02-evaluation-system.md.
//
// Eval may import application, composition, and transcript types; it must
// never construct or import a concrete harness adapter directly. Composition
// remains the only adapter owner (design §5), and the architecture guard
// (internal/harness/architecture) enforces both directions: eval cannot reach
// adapters/testkit, and nothing under application, domain, engine, transcript,
// adapters, or composition may import eval.
package eval
