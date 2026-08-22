// Package composition is the single place where concrete implementations are
// named and wired into a running assembly.
//
// It is the only package permitted to import anything under
// internal/harness/adapters, and the architecture guard enforces that: every
// other owned package is forbidden, and a package with no declared owner is
// forbidden too, so a new package cannot inherit the exception by omission.
//
// The package constructs; it does not decide. It contains no domain
// transition, no retry or admission policy, and no branch that exists only
// for tests. Every bound it applies is forwarded from the component that
// already owns it; the one bound it adds is how long Close may wait.
//
// It is a library rather than a main package so that assembly is asserted by
// tests rather than by launching a process. cmd/och is a thin binary over it.
package composition
