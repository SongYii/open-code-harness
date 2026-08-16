// Package sqlite implements the durable canonical EventStore adapter behind
// the application.EventStore port.
//
// SQLite is the sole live commit authority. One dedicated writer connection
// owns every BEGIN IMMEDIATE mutation transaction; reads use a bounded pool
// with explicit read transactions. The operating profile is verified on every
// open: journal_mode must actually be wal, synchronous is FULL, foreign keys
// are enforced, and waits are bounded. A database written by a newer storage
// format is refused with an upgrade-direction error, never reported as
// corrupt.
//
// Slice 2 scope: the schema is created once at its full target shape.
// export_outbox, transcript_entries, snapshots, and export_checkpoints exist
// from migration 1 but no Slice 2 code path maintains them.
package sqlite
