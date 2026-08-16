package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // pure-Go driver; CGO stays disabled
)

// FormatNewerError reports a database written by a newer storage format.
// It is an upgrade-direction refusal, never corruption.
type FormatNewerError struct {
	Have      int
	Supported int
}

func (err *FormatNewerError) Error() string {
	return fmt.Sprintf("sqlite database format %d is newer than supported %d; upgrade the binary before opening this database", err.Have, err.Supported)
}

// CorruptError reports a violated storage invariant. Mutation is fail-closed
// from this point.
type CorruptError struct {
	Detail string
}

func (err *CorruptError) Error() string {
	return "sqlite database corrupt: " + err.Detail
}

// Store is the SQLite canonical EventStore adapter. One dedicated writer
// connection owns every mutation transaction; reads share a bounded pool.
type Store struct {
	db      *sql.DB
	writer  *sql.Conn
	writeMu sync.Mutex
	config  Config
}

// Open prepares the database: deny-list diagnosis, pragmas, profile
// verification, SQLite version gate, format gate, migrations, and metadata
// verification.
func Open(ctx context.Context, config Config) (*Store, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}

	location, err := resolveLocation(config.Path)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: resolve %q: %w", config.Path, err)
	}
	for _, prefix := range config.DeniedPathPrefixes {
		denied, err := resolveLocation(prefix)
		if err != nil {
			return nil, fmt.Errorf("sqlite open: resolve denied prefix %q: %w", prefix, err)
		}
		if location == denied || strings.HasPrefix(location, denied+string(filepath.Separator)) {
			return nil, fmt.Errorf("sqlite open: database location %q is denied by configuration; live databases are supported only on a local filesystem", location)
		}
	}

	db, err := sql.Open("sqlite", dataSourceName(config.Path, config))
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	db.SetMaxOpenConns(1 + config.MaxReadConnections)
	db.SetMaxIdleConns(1 + config.MaxReadConnections)

	writer, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite open: writer connection: %w", err)
	}
	store := &Store{db: db, writer: writer, config: config}

	if err := store.verifyProfile(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := store.verifyMetadata(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the writer connection and the read pool.
func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	var firstErr error
	if store.writer != nil {
		firstErr = store.writer.Close()
		store.writer = nil
	}
	if err := store.db.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	store.db = nil
	return firstErr
}

func verifyJournalMode(mode string) error {
	if mode != "wal" {
		return fmt.Errorf("sqlite open: journal mode is %q, not wal; the location may be a network or synchronized filesystem, which is not supported for a live database", mode)
	}
	return nil
}

func (store *Store) verifyProfile(ctx context.Context) error {
	var mode string
	if err := store.writer.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("sqlite open: read journal mode: %w", err)
	}
	if err := verifyJournalMode(mode); err != nil {
		return err
	}
	var synchronous int
	if err := store.writer.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		return fmt.Errorf("sqlite open: read synchronous: %w", err)
	}
	if synchronous != 2 {
		return fmt.Errorf("sqlite open: synchronous = %d, want 2 (FULL)", synchronous)
	}
	var foreignKeys int
	if err := store.writer.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("sqlite open: read foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("sqlite open: foreign_keys = %d, want 1", foreignKeys)
	}
	var busyTimeoutMS int
	if err := store.writer.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeoutMS); err != nil {
		return fmt.Errorf("sqlite open: read busy_timeout: %w", err)
	}
	if busyTimeoutMS != int(store.config.BusyTimeout.Milliseconds()) {
		return fmt.Errorf("sqlite open: busy_timeout = %d ms, want %d", busyTimeoutMS, store.config.BusyTimeout.Milliseconds())
	}
	return store.verifySQLiteVersion(ctx)
}

func (store *Store) verifySQLiteVersion(ctx context.Context) error {
	var version string
	if err := store.writer.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return fmt.Errorf("sqlite open: read sqlite version: %w", err)
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); err != nil {
		return fmt.Errorf("sqlite open: parse sqlite version %q: %w", version, err)
	}
	if major < 3 || (major == 3 && minor < 42) {
		return fmt.Errorf("sqlite open: bundled sqlite %s predates unixepoch subsec support (need >= 3.42.0)", version)
	}
	var stamp float64
	if err := store.writer.QueryRowContext(ctx, "SELECT unixepoch('subsec')").Scan(&stamp); err != nil {
		return fmt.Errorf("sqlite open: unixepoch('subsec') unavailable: %w", err)
	}
	if stamp <= 0 {
		return fmt.Errorf("sqlite open: unixepoch('subsec') = %v, want positive", stamp)
	}
	return nil
}

func dataSourceName(path string, config Config) string {
	values := url.Values{}
	values.Add("_pragma", "journal_mode(WAL)")
	values.Add("_pragma", "synchronous(FULL)")
	values.Add("_pragma", "foreign_keys(1)")
	values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", config.BusyTimeout.Milliseconds()))
	values.Add("_pragma", fmt.Sprintf("wal_autocheckpoint(%d)", config.WALAutoCheckpoint))
	return "file:" + filepath.ToSlash(path) + "?" + values.Encode()
}

// resolveLocation returns the most resolved absolute form of path by
// resolving the deepest existing ancestor and rejoining the remainder, so a
// not-yet-created file and its directory prefix compare consistently.
func resolveLocation(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	current := absolute
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(append([]string{resolved}, tail...)...), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return absolute, nil
		}
		tail = append([]string{filepath.Base(current)}, tail...)
		current = parent
	}
}
