package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// EvaluationConfig bounds a cold, read-only evaluation open (implementation
// plan Task 4). Unlike Config, it carries no RuntimeID and no LeaseDuration:
// this path never acquires, waits for, releases, or heartbeats the runtime
// lease, and never migrates -- an older format is refused exactly like
// OpenReader, not silently upgraded.
type EvaluationConfig struct {
	Path               string
	BusyTimeout        time.Duration
	WALAutoCheckpoint  int
	DeniedPathPrefixes []string
}

func (config EvaluationConfig) withDefaults() EvaluationConfig {
	if config.BusyTimeout == 0 {
		config.BusyTimeout = defaultBusyTimeout
	}
	if config.WALAutoCheckpoint == 0 {
		config.WALAutoCheckpoint = defaultWALAutoCheckpoint
	}
	return config
}

func (config EvaluationConfig) validate() error {
	if config.Path == "" {
		return fmt.Errorf("sqlite evaluation config: path is required")
	}
	if config.BusyTimeout < minBusyTimeout || config.BusyTimeout > maxBusyTimeout {
		return fmt.Errorf("sqlite evaluation config: busy timeout %s outside [%s, %s]", config.BusyTimeout, minBusyTimeout, maxBusyTimeout)
	}
	if config.WALAutoCheckpoint < 1 {
		return fmt.Errorf("sqlite evaluation config: WAL autocheckpoint %d must be at least 1", config.WALAutoCheckpoint)
	}
	return nil
}

// ErrEvaluationLeaseLive reports that a Runtime Host lease is currently live
// on the database an evaluation open targeted. It carries only safe lease
// facts -- the owning runtime ID and when the lease expires -- never a
// fencing token or anything else a caller could use to act on the lease. A
// cold evaluation open never waits for, takes over, releases, heartbeats,
// or signals the owner; it only refuses, before creating any destination.
type ErrEvaluationLeaseLive struct {
	Owner     string
	ExpiresAt time.Time
}

func (err *ErrEvaluationLeaseLive) Error() string {
	return fmt.Sprintf("sqlite evaluation: runtime lease is live, held by %q until %s",
		err.Owner, err.ExpiresAt.UTC().Format(time.RFC3339))
}

// openEvaluationDB opens config.Path cold: no migration, no lease
// acquisition, query_only pragma set -- the same profile OpenReader uses,
// via the same dataSourceNamePragmas/verifyOperatingProfile/
// verifyExactFormatVersion/verifyMetadata sequence, so a cold evaluation
// open and a Reader open can never quietly diverge in what they accept.
func openEvaluationDB(ctx context.Context, config EvaluationConfig) (*sql.DB, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	if err := rejectDeniedPath(config.Path, config.DeniedPathPrefixes); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dataSourceNamePragmas(config.Path, config.BusyTimeout, config.WALAutoCheckpoint, true))
	if err != nil {
		return nil, fmt.Errorf("sqlite evaluation: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := verifyOperatingProfile(ctx, db, config.BusyTimeout, true); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := verifyExactFormatVersion(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := verifyMetadata(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// checkNoLiveLease refuses if a Runtime Host lease is currently live. It
// never acquires, waits for, releases, or heartbeats the lease: one
// read-only SELECT against runtime_leases, judged by the exact
// expiresAt-vs-now predicate AcquireLease itself uses (lease.go), computed
// by SQLite's own clock so a cold reader and a live writer never disagree
// about "now" because of host clock skew.
func checkNoLiveLease(ctx context.Context, db *sql.DB) error {
	var runtimeID string
	var expiresAt float64
	var now float64
	err := db.QueryRowContext(ctx,
		"SELECT runtime_id, lease_expires_at_unix, unixepoch('subsec') FROM runtime_leases WHERE id = 1").
		Scan(&runtimeID, &expiresAt, &now)
	switch {
	case isNoRows(err):
		return nil // no lease has ever been acquired on this database
	case err != nil:
		return mapStorageError(err, "")
	case expiresAt < now:
		return nil // expired; not live
	default:
		return &ErrEvaluationLeaseLive{Owner: runtimeID, ExpiresAt: time.Unix(int64(expiresAt), 0)}
	}
}

// SessionTerminalFacts are the bounded Session/Turn/compaction terminal
// facts InspectEvaluationStore returns for one Session (design §14).
type SessionTerminalFacts struct {
	Status           string
	Open             bool
	Running          bool
	ActiveTurnID     string
	CompactionActive bool
}

// EvaluationInspection is the pinned identity ExportEvaluationEvidence later
// re-verifies before regenerating evidence (design §14: "reopen cold,
// verify the pinned identity/heads are unchanged"). SessionHeadSequence and
// StoreHeadCommitPosition are different coordinate systems -- one counts
// this Session's own events, the other counts every committed append across
// the whole database -- and are never compared to one another numerically;
// SessionHeadAppendCommitPosition is what actually links them, naming the
// exact append that introduced the Session's current head event.
type EvaluationInspection struct {
	DatabasePath                    string
	SessionID                       string
	StoreHeadCommitPosition         uint64
	SessionHeadSequence             uint64
	SessionHeadAppendCommitPosition uint64
	Terminal                        SessionTerminalFacts
}

// InspectEvaluationStore opens databasePath cold, verifies sessionID's
// canonical event chain agrees with the maintained session_heads
// projection (the same comparison RebuildAndVerifySessionHeads performs,
// scoped to one session), pins its identity, and returns bounded terminal
// facts. It refuses with *ErrEvaluationLeaseLive if a Runtime Host lease is
// currently live. It creates no destination and mutates nothing; the
// database is closed before this function returns either way.
func InspectEvaluationStore(ctx context.Context, databasePath string, sessionID domain.SessionID) (EvaluationInspection, error) {
	if _, err := domain.ParseSessionID(string(sessionID)); err != nil {
		return EvaluationInspection{}, fmt.Errorf("sqlite evaluation: %w", err)
	}
	db, err := openEvaluationDB(ctx, EvaluationConfig{Path: databasePath})
	if err != nil {
		return EvaluationInspection{}, err
	}
	defer db.Close()

	if err := checkNoLiveLease(ctx, db); err != nil {
		return EvaluationInspection{}, err
	}
	return inspectEvaluationSession(ctx, db, databasePath, sessionID)
}

// inspectEvaluationSession is the shared core InspectEvaluationStore and the
// export path's re-verification step both call, against an already-open,
// already-lease-checked cold connection.
func inspectEvaluationSession(ctx context.Context, db *sql.DB, databasePath string, sessionID domain.SessionID) (EvaluationInspection, error) {
	var storeHead uint64
	if err := db.QueryRowContext(ctx, "SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&storeHead); err != nil {
		return EvaluationInspection{}, mapStorageError(err, sessionID)
	}

	// version is the head sequence (this session's own event count);
	// last_append_commit_position is the *global* commit position of the
	// event_appends row that introduced it -- design §14's "different
	// coordinate systems," kept in the same row precisely so they are never
	// conflated. sessionHeadState.position (below) is a commit position, so
	// projectSessionHead must be given last_append_commit_position here, not
	// headSequence -- passing the wrong one silently miscompares against
	// session_heads and manufactures corruption on every valid database.
	var headSequence, headAppendPosition uint64
	err := db.QueryRowContext(ctx,
		"SELECT version, last_append_commit_position FROM event_streams WHERE session_id = ?",
		string(sessionID)).Scan(&headSequence, &headAppendPosition)
	switch {
	case isNoRows(err):
		return EvaluationInspection{}, fmt.Errorf("sqlite evaluation: session %q has no stream", sessionID)
	case err != nil:
		return EvaluationInspection{}, mapStorageError(err, sessionID)
	}

	rows, err := db.QueryContext(ctx, "SELECT payload FROM events WHERE session_id = ? ORDER BY sequence", string(sessionID))
	if err != nil {
		return EvaluationInspection{}, mapStorageError(err, sessionID)
	}
	var records []domain.RecordedEvent
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return EvaluationInspection{}, mapStorageError(err, sessionID)
		}
		record, err := domain.UnmarshalRecordedEvent(payload)
		if err != nil {
			rows.Close()
			return EvaluationInspection{}, newStoreError(application.StoreCodeCorrupt, sessionID, wrapDetail("unreadable canonical event payload", err))
		}
		if record.SessionID != sessionID {
			rows.Close()
			return EvaluationInspection{}, newStoreError(application.StoreCodeCorrupt, sessionID, wrapDetail("canonical event belongs to another session", nil))
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return EvaluationInspection{}, mapStorageError(err, sessionID)
	}
	rows.Close()

	// Canonical event chain verification: the same replay-and-compare
	// RebuildAndVerifySessionHeads performs, scoped to this one session.
	// Disagreement is corruption, not silently reported terminal facts.
	expected, err := projectSessionHead(records, headAppendPosition)
	if err != nil {
		return EvaluationInspection{}, newStoreError(application.StoreCodeCorrupt, sessionID, wrapDetail("invalid canonical stream", err))
	}
	var stored sessionHeadState
	err = db.QueryRowContext(ctx,
		"SELECT workspace_root, status, active_turn_id, active_item_id, updated_at_commit_position FROM session_heads WHERE session_id = ?",
		string(sessionID)).Scan(&stored.workspaceRoot, &stored.status, &stored.turn, &stored.item, &stored.position)
	switch {
	case isNoRows(err):
		return EvaluationInspection{}, newStoreError(application.StoreCodeCorrupt, sessionID, wrapDetail("session_heads row missing for existing stream", nil))
	case err != nil:
		return EvaluationInspection{}, mapStorageError(err, sessionID)
	}
	if stored != expected {
		return EvaluationInspection{}, newStoreError(application.StoreCodeCorrupt, sessionID, wrapDetail("session_heads disagrees with canonical stream replay", nil))
	}

	// domain.Replay's full Session state carries ContextCompaction, which
	// sessionHeadState (a narrower read model) does not track.
	state, err := domain.Replay(records)
	if err != nil {
		return EvaluationInspection{}, newStoreError(application.StoreCodeCorrupt, sessionID, wrapDetail("canonical stream fails domain replay", err))
	}

	terminal := SessionTerminalFacts{
		Status:           expected.status,
		Open:             state.Status == domain.SessionStatusActive,
		Running:          state.ActiveTurn != nil,
		CompactionActive: state.ContextCompaction != nil,
	}
	if state.ActiveTurn != nil {
		terminal.ActiveTurnID = string(state.ActiveTurn.ID)
	}

	return EvaluationInspection{
		DatabasePath:                    databasePath,
		SessionID:                       string(sessionID),
		StoreHeadCommitPosition:         storeHead,
		SessionHeadSequence:             headSequence,
		SessionHeadAppendCommitPosition: headAppendPosition,
		Terminal:                        terminal,
	}, nil
}

// EvaluationExportSession is a cold, verified, read-only connection opened
// by OpenEvaluationExport for one export operation. It implements
// ReadStream (so a caller in another package can hand it to
// transcript.WriteSession, exactly the way composition.ExportSession
// already uses *Reader) and exposes RegenerateAudit for the audit side, so
// a transcript and an audit replica exported through the same
// EvaluationExportSession always reflect the identity OpenEvaluationExport
// pinned and re-verified -- never two independently reopened, potentially
// racing snapshots.
type EvaluationExportSession struct {
	db         *sql.DB
	inspection EvaluationInspection
}

// OpenEvaluationExport reopens pinned.DatabasePath cold, refuses if a
// Runtime Host lease is currently live, and refuses if the database's
// current identity/heads disagree with pinned (design §14: a database that
// changed since InspectEvaluationStore ran is refused, never silently
// exported against a moving target). It creates no destination itself;
// RegenerateAudit and the caller's own transcript write are what create
// files, and both require an empty destination.
func OpenEvaluationExport(ctx context.Context, pinned EvaluationInspection) (*EvaluationExportSession, error) {
	sessionID, err := domain.ParseSessionID(pinned.SessionID)
	if err != nil {
		return nil, fmt.Errorf("sqlite evaluation: %w", err)
	}
	db, err := openEvaluationDB(ctx, EvaluationConfig{Path: pinned.DatabasePath})
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = db.Close()
		}
	}()

	if err := checkNoLiveLease(ctx, db); err != nil {
		return nil, err
	}
	current, err := inspectEvaluationSession(ctx, db, pinned.DatabasePath, sessionID)
	if err != nil {
		return nil, err
	}
	if current.StoreHeadCommitPosition != pinned.StoreHeadCommitPosition ||
		current.SessionHeadSequence != pinned.SessionHeadSequence ||
		current.SessionHeadAppendCommitPosition != pinned.SessionHeadAppendCommitPosition {
		return nil, fmt.Errorf(
			"sqlite evaluation: database changed since inspection: store head %d->%d, session head %d->%d, session-head append %d->%d",
			pinned.StoreHeadCommitPosition, current.StoreHeadCommitPosition,
			pinned.SessionHeadSequence, current.SessionHeadSequence,
			pinned.SessionHeadAppendCommitPosition, current.SessionHeadAppendCommitPosition)
	}

	closeOnError = false
	return &EvaluationExportSession{db: db, inspection: current}, nil
}

// Close releases the underlying cold connection.
func (session *EvaluationExportSession) Close() error {
	if session == nil || session.db == nil {
		return nil
	}
	err := session.db.Close()
	session.db = nil
	return err
}

// Inspection returns the re-verified identity OpenEvaluationExport pinned.
func (session *EvaluationExportSession) Inspection() EvaluationInspection {
	return session.inspection
}

// ReadStream lets this session serve transcript.WriteSession the same way
// *Reader already does.
func (session *EvaluationExportSession) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	return readStream(ctx, session.db, request)
}

// RegenerateAudit regenerates a canonical audit replica directly from this
// database's append records into directory via ExportConsistent, fixed to
// the pinned store head, then verifies the generated replica with
// VerifyAuditReplica before returning -- design §14's "regenerate canonical
// audit JSONL from database append records" and "verify the generated
// snapshot before returning." It never copies an already-exported live
// replica and never touches the live exporter's checkpoint or outbox
// (ExportConsistent's own contract). directory must not yet exist or must
// be empty, matching ExportConsistent's own requirement.
//
// The returned VerifiedAuditReplica's HeadCommitPosition must be at least
// this session's SessionHeadAppendCommitPosition for the export to be
// usable evidence -- design §14's inclusion proof that the generated audit
// reaches the append that introduced the pinned Session head. Verifying
// that proof is the caller's responsibility (composition/evaluation.go),
// since ExportConsistent itself has no notion of "session" at all.
func (session *EvaluationExportSession) RegenerateAudit(ctx context.Context, directory string) (VerifiedAuditReplica, error) {
	store := &Store{db: session.db}
	if err := store.ExportConsistent(ctx, session.inspection.StoreHeadCommitPosition, ExportConfig{Directory: directory}); err != nil {
		return VerifiedAuditReplica{}, fmt.Errorf("sqlite evaluation: regenerate audit replica: %w", err)
	}
	verified, err := VerifyAuditReplica(directory)
	if err != nil {
		return VerifiedAuditReplica{}, fmt.Errorf("sqlite evaluation: verify regenerated audit replica: %w", err)
	}
	return verified, nil
}
