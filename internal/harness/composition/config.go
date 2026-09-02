package composition

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
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

// Context tunes the Context Engine (design §21), which Open always
// constructs from Provider.ContextWindow/MaxOutput -- there is no Enabled
// switch here, since a working Context Engine is this milestone's baseline
// assembly behavior rather than an opt-in extra. Every field is a design
// §8 budget percentage or cap; a zero value receives that section's own
// default, and Validate rejects an out-of-range or inverted relationship
// before Open constructs any resource.
type Context struct {
	// TriggerPercent/TargetPercent/TailPercent derive contextengine.Budget
	// (design §8) from Provider.ContextWindow/MaxOutput: the fraction of
	// hardInput that triggers compaction, the fraction compaction targets,
	// and the fraction of hardInput protected as an uncompacted tail.
	TriggerPercent uint32
	TargetPercent  uint32
	TailPercent    uint32
	// MaxSummaryChunks bounds design §11.2's rolling, chunked
	// summarization: how many summarizer calls one compaction may use when
	// its covered source material does not fit in a single call.
	// Accepted and range-validated per design §8's table.
	MaxSummaryChunks uint32
	// MaxOverflowCompactionsPerTurn bounds design §15.3/§19's per-Turn
	// Provider overflow recovery count (application.ContextConfig's own
	// MaxOverflowRecoveriesPerTurn).
	MaxOverflowCompactionsPerTurn uint32
	// MaxPrunedToolResultsPerRequest bounds design §10's per-request Tool
	// Result pruning count. Accepted and range-validated per design §8's
	// table; Tool Result projection (contextengine.ProjectToolResult) is
	// not yet wired into Materialize's own pipeline (a disclosed,
	// pre-existing scope gap), so this field does not yet change behavior
	// either -- kept for the same forward-compatibility reason as
	// MaxSummaryChunks above.
	MaxPrunedToolResultsPerRequest uint32
	// CompactionTimeout bounds one summarizer call within a compaction
	// bracket (application.ContextConfig's own CompactionTimeout).
	CompactionTimeout time.Duration
}

// Context Engine defaults and valid ranges, design §8's own table.
const (
	DefaultTriggerPercent                 = 80
	MinTriggerPercent                     = 60
	MaxTriggerPercent                     = 95
	DefaultTargetPercent                  = 55
	MinTargetPercent                      = 30
	MaxTargetPercent                      = 80
	DefaultTailPercent                    = 25
	MinTailPercent                        = 10
	MaxTailPercent                        = 50
	DefaultMaxSummaryChunks               = 8
	MinMaxSummaryChunks                   = 1
	MaxMaxSummaryChunks                   = 16
	DefaultMaxOverflowCompactionsPerTurn  = 2
	MinMaxOverflowCompactionsPerTurn      = 1
	MaxMaxOverflowCompactionsPerTurn      = 3
	DefaultCompactionTimeout              = 2 * time.Minute
	MinCompactionTimeout                  = 5 * time.Second
	MaxCompactionTimeout                  = 10 * time.Minute
	DefaultMaxPrunedToolResultsPerRequest = 64
	MinMaxPrunedToolResultsPerRequest     = 1
	MaxMaxPrunedToolResultsPerRequest     = 64
)

func (context Context) withDefaults() Context {
	if context.TriggerPercent == 0 {
		context.TriggerPercent = DefaultTriggerPercent
	}
	if context.TargetPercent == 0 {
		context.TargetPercent = DefaultTargetPercent
	}
	if context.TailPercent == 0 {
		context.TailPercent = DefaultTailPercent
	}
	if context.MaxSummaryChunks == 0 {
		context.MaxSummaryChunks = DefaultMaxSummaryChunks
	}
	if context.MaxOverflowCompactionsPerTurn == 0 {
		context.MaxOverflowCompactionsPerTurn = DefaultMaxOverflowCompactionsPerTurn
	}
	if context.CompactionTimeout == 0 {
		context.CompactionTimeout = DefaultCompactionTimeout
	}
	if context.MaxPrunedToolResultsPerRequest == 0 {
		context.MaxPrunedToolResultsPerRequest = DefaultMaxPrunedToolResultsPerRequest
	}
	return context
}

// validate checks every range and relationship design §8's table
// documents, then confirms windowTokens/maxOutputTokens can even produce a
// positive contextengine budget -- reusing ComputeBudget itself rather
// than re-deriving its arithmetic, so this check and the one Open performs
// later can never silently disagree.
func (context Context) validate(windowTokens, maxOutputTokens uint32) error {
	if context.TriggerPercent < MinTriggerPercent || context.TriggerPercent > MaxTriggerPercent {
		return fmt.Errorf("%w: Context.TriggerPercent must be %d-%d", errInvalidConfig, MinTriggerPercent, MaxTriggerPercent)
	}
	if context.TargetPercent < MinTargetPercent || context.TargetPercent > MaxTargetPercent {
		return fmt.Errorf("%w: Context.TargetPercent must be %d-%d", errInvalidConfig, MinTargetPercent, MaxTargetPercent)
	}
	if context.TargetPercent >= context.TriggerPercent {
		return fmt.Errorf("%w: Context.TargetPercent must be less than TriggerPercent", errInvalidConfig)
	}
	if context.TailPercent < MinTailPercent || context.TailPercent > MaxTailPercent {
		return fmt.Errorf("%w: Context.TailPercent must be %d-%d", errInvalidConfig, MinTailPercent, MaxTailPercent)
	}
	if context.TailPercent >= context.TargetPercent {
		return fmt.Errorf("%w: Context.TailPercent must be less than TargetPercent", errInvalidConfig)
	}
	if context.MaxSummaryChunks < MinMaxSummaryChunks || context.MaxSummaryChunks > MaxMaxSummaryChunks {
		return fmt.Errorf("%w: Context.MaxSummaryChunks must be %d-%d", errInvalidConfig, MinMaxSummaryChunks, MaxMaxSummaryChunks)
	}
	if context.MaxOverflowCompactionsPerTurn < MinMaxOverflowCompactionsPerTurn || context.MaxOverflowCompactionsPerTurn > MaxMaxOverflowCompactionsPerTurn {
		return fmt.Errorf("%w: Context.MaxOverflowCompactionsPerTurn must be %d-%d", errInvalidConfig, MinMaxOverflowCompactionsPerTurn, MaxMaxOverflowCompactionsPerTurn)
	}
	if context.CompactionTimeout < MinCompactionTimeout || context.CompactionTimeout > MaxCompactionTimeout {
		return fmt.Errorf("%w: Context.CompactionTimeout must be %s-%s", errInvalidConfig, MinCompactionTimeout, MaxCompactionTimeout)
	}
	if context.MaxPrunedToolResultsPerRequest < MinMaxPrunedToolResultsPerRequest || context.MaxPrunedToolResultsPerRequest > MaxMaxPrunedToolResultsPerRequest {
		return fmt.Errorf("%w: Context.MaxPrunedToolResultsPerRequest must be %d-%d", errInvalidConfig, MinMaxPrunedToolResultsPerRequest, MaxMaxPrunedToolResultsPerRequest)
	}
	if _, err := contextengine.ComputeBudget(windowTokens, maxOutputTokens, contextengine.BudgetConfig{
		TriggerPercent: context.TriggerPercent, TargetPercent: context.TargetPercent, TailPercent: context.TailPercent,
	}); err != nil {
		return fmt.Errorf("%w: Provider.ContextWindow/MaxOutput cannot produce a positive Context Engine budget: %w", errInvalidConfig, err)
	}
	return nil
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
	// Context tunes the Context Engine Open always constructs (design
	// §21); see the Context type's own doc for why there is no separate
	// enable switch here.
	Context Context
	// Approver is optional. Unset becomes a deny slot so an ACP server can
	// attach later without reconstructing the Service.
	Approver tools.Approver

	// ShutdownTimeout bounds Close. Default 10s. This is the only bound this
	// package introduces rather than forwards.
	ShutdownTimeout time.Duration

	// AllowUnsandboxedExec permits Open to proceed when no OS-level exec
	// confinement backend is available on this host (missing bwrap or
	// sandbox-exec, WSL1, or any platform with neither — including
	// Windows, which has none in this slice: design doc §5). Off by
	// default, so a missing sandbox fails Open closed rather than
	// silently running commands unconfined. Setting it is a deliberate,
	// loud trade-off, not a convenience: Open logs exactly which
	// guarantee is absent.
	AllowUnsandboxedExec bool
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
	config.Context = config.Context.withDefaults()
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
	if err := config.Context.validate(config.Provider.ContextWindow, config.Provider.MaxOutput); err != nil {
		return err
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
