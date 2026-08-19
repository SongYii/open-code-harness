package composition

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/policy"
)

// Provider names the model endpoint and where its credential comes from.
//
// APIKey is not a field. A credential passed as a literal ends up in test
// fixtures, shell history, and process listings; the key is read from the
// named environment variable at Open, and never stored on Config.
type Provider struct {
	BaseURL       string
	ModelID       string
	APIKeyEnv     string
	ContextWindow uint32
	MaxOutput     uint32
	// AllowInsecureLoopback permits a plain-HTTP base URL when it resolves to
	// loopback. It exists for a local fixture server and must stay false
	// against any real endpoint.
	AllowInsecureLoopback bool
}

// Limits forwards the bounds Application already owns. A zero field means the
// Application default; no field here may widen a bound another component has
// already fixed.
type Limits struct {
	MaxSteps            int
	MaxToolCallsPerStep int
	MaxAssistantBytes   int
	ApprovalTimeout     time.Duration
}

// Config describes one assembly. Every field is validated before any resource
// is constructed.
type Config struct {
	// WorkspaceRoot jails every filesystem tool and is the working directory
	// for exec. It must already exist.
	WorkspaceRoot string
	// DatabasePath is the canonical SQLite database. Its parent must exist.
	DatabasePath string
	// RuntimeID is this writer's identity for the fencing lease.
	RuntimeID string
	// AuditDirectory receives the JSONL audit replica. Empty disables the
	// exporter; export lag never blocks readiness either way.
	AuditDirectory string

	Provider Provider
	Policy   policy.Mode
	Limits   Limits

	// ShutdownTimeout bounds Close. Default 10s. This is the only bound this
	// package introduces rather than forwards.
	ShutdownTimeout time.Duration
}

// DefaultShutdownTimeout bounds how long Close waits for the host's loops
// before reporting a timeout instead of blocking forever.
const DefaultShutdownTimeout = 10 * time.Second

var errInvalidConfig = errors.New("composition: invalid config")

func (config Config) withDefaults() Config {
	if config.Policy == "" {
		config.Policy = policy.ModeDefault
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = DefaultShutdownTimeout
	}
	return config
}

// Validate is total and fail-closed: every field is checked, and an invalid
// Config constructs nothing. Errors name the field and are not wrapped in an
// adapter error type, because they are the caller's mistake rather than a
// component's failure.
func (config Config) Validate() error {
	config = config.withDefaults()

	if err := requireExistingDirectory("WorkspaceRoot", config.WorkspaceRoot); err != nil {
		return err
	}
	if config.DatabasePath == "" || !filepath.IsAbs(config.DatabasePath) {
		return fmt.Errorf("%w: DatabasePath must be an absolute path", errInvalidConfig)
	}
	if err := requireExistingDirectory("DatabasePath parent", filepath.Dir(config.DatabasePath)); err != nil {
		return err
	}
	if config.RuntimeID == "" {
		return fmt.Errorf("%w: RuntimeID is required", errInvalidConfig)
	}
	if config.AuditDirectory != "" {
		if err := requireExistingDirectory("AuditDirectory", config.AuditDirectory); err != nil {
			return err
		}
	}
	if config.Provider.BaseURL == "" {
		return fmt.Errorf("%w: Provider.BaseURL is required", errInvalidConfig)
	}
	if config.Provider.ModelID == "" {
		return fmt.Errorf("%w: Provider.ModelID is required", errInvalidConfig)
	}
	if config.Provider.APIKeyEnv == "" {
		return fmt.Errorf("%w: Provider.APIKeyEnv is required", errInvalidConfig)
	}
	if config.Provider.ContextWindow == 0 || config.Provider.MaxOutput == 0 {
		return fmt.Errorf("%w: Provider.ContextWindow and Provider.MaxOutput must be greater than zero", errInvalidConfig)
	}
	switch config.Policy {
	case policy.ModeDefault, policy.ModeReadOnly, policy.ModeAllowWrites, policy.ModeDenyAll:
	default:
		return fmt.Errorf("%w: Policy %q is not a known mode", errInvalidConfig, config.Policy)
	}
	if config.Limits.MaxSteps < 0 || config.Limits.MaxToolCallsPerStep < 0 || config.Limits.MaxAssistantBytes < 0 {
		return fmt.Errorf("%w: Limits must not be negative", errInvalidConfig)
	}
	if config.Limits.ApprovalTimeout < 0 || config.ShutdownTimeout <= 0 {
		return fmt.Errorf("%w: timeouts must be positive", errInvalidConfig)
	}
	return nil
}

func requireExistingDirectory(field, path string) error {
	if path == "" {
		return fmt.Errorf("%w: %s is required", errInvalidConfig, field)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: %s must be an absolute path", errInvalidConfig, field)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %s %q is not usable: %w", errInvalidConfig, field, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s %q is not a directory", errInvalidConfig, field, path)
	}
	return nil
}
