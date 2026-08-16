package sqlite

// migration1DDL creates the full target shape from the accepted runtime
// design sections 7.1-7.8. Uniqueness that encodes the contract lives here:
// (session_id, sequence) unique, event_id globally unique, append_id unique,
// commit_position unique, run_turn_request_id globally unique, and
// (session_id, identity_kind, identity_id) unique.
//
// Later-slice tables (export_outbox, transcript_entries, snapshots,
// export_checkpoints) and audit-chain columns exist from day one so no later
// migration must reshape committed rows; Slice 2 code paths do not maintain
// them.
const migration1DDL = `
CREATE TABLE store_metadata (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	storage_format_version INTEGER NOT NULL,
	head_commit_position INTEGER NOT NULL DEFAULT 0 CHECK (head_commit_position >= 0),
	head_audit_digest BLOB,
	created_at_unix REAL NOT NULL,
	last_migration_at_unix REAL NOT NULL
);

CREATE TABLE event_streams (
	session_id TEXT PRIMARY KEY,
	version INTEGER NOT NULL CHECK (version >= 0),
	created_at_commit_position INTEGER NOT NULL CHECK (created_at_commit_position > 0),
	last_append_commit_position INTEGER NOT NULL CHECK (last_append_commit_position > 0)
);

CREATE TABLE event_appends (
	append_id TEXT PRIMARY KEY,
	commit_position INTEGER NOT NULL UNIQUE CHECK (commit_position > 0),
	session_id TEXT NOT NULL REFERENCES event_streams (session_id),
	expected_version INTEGER NOT NULL CHECK (expected_version >= 0),
	first_sequence INTEGER NOT NULL CHECK (first_sequence >= 1),
	last_sequence INTEGER NOT NULL CHECK (last_sequence >= first_sequence),
	event_count INTEGER NOT NULL CHECK (event_count >= 1),
	command_id TEXT NOT NULL,
	request_digest BLOB NOT NULL CHECK (length(request_digest) = 32),
	writer_runtime_id TEXT NOT NULL,
	writer_fencing_token INTEGER NOT NULL CHECK (writer_fencing_token > 0),
	audit_format_version INTEGER NOT NULL DEFAULT 0,
	previous_audit_digest BLOB,
	batch_audit_digest BLOB,
	committed_at_unix REAL NOT NULL
);

CREATE TABLE events (
	session_id TEXT NOT NULL REFERENCES event_streams (session_id),
	sequence INTEGER NOT NULL CHECK (sequence >= 1),
	event_id TEXT NOT NULL UNIQUE,
	append_id TEXT NOT NULL REFERENCES event_appends (append_id),
	order_in_append INTEGER NOT NULL CHECK (order_in_append >= 1),
	command_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
	occurred_at TEXT NOT NULL,
	payload BLOB NOT NULL,
	payload_digest BLOB NOT NULL CHECK (length(payload_digest) = 32),
	PRIMARY KEY (session_id, sequence)
);

CREATE TABLE command_requests (
	run_turn_request_id TEXT PRIMARY KEY,
	request_digest BLOB NOT NULL CHECK (length(request_digest) = 32),
	session_id TEXT NOT NULL REFERENCES event_streams (session_id),
	command_id TEXT NOT NULL,
	turn_id TEXT NOT NULL,
	item_id TEXT NOT NULL,
	admission_append_id TEXT NOT NULL REFERENCES event_appends (append_id)
);

CREATE TABLE domain_identities (
	session_id TEXT NOT NULL,
	identity_kind TEXT NOT NULL,
	identity_id TEXT NOT NULL,
	introducing_event_id TEXT NOT NULL REFERENCES events (event_id),
	PRIMARY KEY (session_id, identity_kind, identity_id)
);

CREATE TABLE runtime_leases (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	runtime_id TEXT NOT NULL,
	fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
	lease_expires_at_unix REAL NOT NULL,
	last_heartbeat_at_unix REAL NOT NULL
);

CREATE TABLE export_outbox (
	commit_position INTEGER PRIMARY KEY REFERENCES event_appends (commit_position),
	append_id TEXT NOT NULL REFERENCES event_appends (append_id),
	audit_format_version INTEGER NOT NULL,
	envelope BLOB NOT NULL,
	envelope_digest BLOB NOT NULL,
	export_state TEXT NOT NULL
);

CREATE TABLE session_heads (
	session_id TEXT PRIMARY KEY REFERENCES event_streams (session_id),
	status TEXT NOT NULL,
	active_turn_id TEXT,
	active_item_id TEXT,
	updated_at_commit_position INTEGER NOT NULL CHECK (updated_at_commit_position > 0)
);

CREATE TABLE transcript_entries (
	session_id TEXT NOT NULL REFERENCES event_streams (session_id),
	sequence INTEGER NOT NULL,
	event_id TEXT NOT NULL REFERENCES events (event_id),
	kind TEXT NOT NULL,
	content BLOB NOT NULL,
	PRIMARY KEY (session_id, sequence)
);

CREATE TABLE snapshots (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL REFERENCES event_streams (session_id),
	version INTEGER NOT NULL CHECK (version >= 0),
	compact_state BLOB NOT NULL,
	created_at_unix REAL NOT NULL
);

CREATE TABLE export_checkpoints (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	last_exported_commit_position INTEGER NOT NULL DEFAULT 0,
	manifest_digest BLOB,
	updated_at_unix REAL NOT NULL
);
`

// migration2DDL adds the index used to cross-check a stored receipt against
// the events actually committed under its AppendID.
const migration2DDL = `
CREATE INDEX events_by_append ON events (append_id);
`

// migration3DDL adds the exporter lease table from the accepted runtime
// design section 7.8; the audit-chain backfill itself is the code step
// registered with migration 3.
const migration3DDL = `
CREATE TABLE export_leases (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	exporter_id TEXT NOT NULL,
	fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
	lease_expires_at_unix REAL NOT NULL,
	last_heartbeat_at_unix REAL NOT NULL
);
`
