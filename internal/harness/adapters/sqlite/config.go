package sqlite

import (
	"fmt"
	"time"
)

const (
	defaultBusyTimeout       = 5 * time.Second
	minBusyTimeout           = 100 * time.Millisecond
	maxBusyTimeout           = 60 * time.Second
	defaultMaxReadConnection = 4
	defaultWALAutoCheckpoint = 1000
)

// Config bounds every resource the adapter may consume. Zero values take the
// documented defaults; out-of-range values are rejected at Open.
type Config struct {
	// Path is the database file location. It must resolve to a local
	// filesystem; known network or synchronization locations listed in
	// DeniedPathPrefixes are refused at open with a diagnosis.
	Path string

	// BusyTimeout bounds how long a contended lock waits before failing.
	// Default 5s; allowed range 100ms to 60s.
	BusyTimeout time.Duration

	// MaxReadConnections bounds the read pool in addition to the single
	// dedicated writer connection. Default 4; minimum 1.
	MaxReadConnections int

	// WALAutoCheckpoint is the WAL size in pages that triggers an
	// automatic checkpoint. Default 1000.
	WALAutoCheckpoint int

	// DeniedPathPrefixes lists absolute path prefixes refused at open.
	DeniedPathPrefixes []string
}

func (config Config) withDefaults() Config {
	if config.BusyTimeout == 0 {
		config.BusyTimeout = defaultBusyTimeout
	}
	if config.MaxReadConnections == 0 {
		config.MaxReadConnections = defaultMaxReadConnection
	}
	if config.WALAutoCheckpoint == 0 {
		config.WALAutoCheckpoint = defaultWALAutoCheckpoint
	}
	return config
}

func (config Config) validate() error {
	if config.Path == "" {
		return fmt.Errorf("sqlite config: path is required")
	}
	if config.BusyTimeout < minBusyTimeout || config.BusyTimeout > maxBusyTimeout {
		return fmt.Errorf("sqlite config: busy timeout %s outside [%s, %s]", config.BusyTimeout, minBusyTimeout, maxBusyTimeout)
	}
	if config.MaxReadConnections < 1 {
		return fmt.Errorf("sqlite config: max read connections %d must be at least 1", config.MaxReadConnections)
	}
	if config.WALAutoCheckpoint < 1 {
		return fmt.Errorf("sqlite config: WAL autocheckpoint %d must be at least 1", config.WALAutoCheckpoint)
	}
	return nil
}
