// Package runtime hosts the single Runtime Host over the Application
// service and the SQLite canonical EventStore: startup reconciliation with
// deterministic recovery appends, the bounded heartbeat loop with fencing
// reactions, graceful shutdown, and ownership of the background audit
// exporter.
//
// The host owns policy only. The lease mechanism, fencing predicate, and
// session_heads projection come from the storage slice; the host adds no
// domain rules and no storage authority. Commands are unavailable until
// reconciliation completes; audit export lag never blocks readiness.
package runtime
