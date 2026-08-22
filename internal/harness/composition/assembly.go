package composition

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/acp"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/localexec"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/openaicompat"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/system"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/workspacefs"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/runtime"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// Assembly is a running harness: a Runtime Host owning the durable store, and
// an Application service wired to a provider, the workspace tools, and the
// policy engine. Accessors are read-only; the assembly owns every resource it
// returns and releases them in Close.
type Assembly struct {
	service   *application.Service
	host      *runtime.Host
	store     application.EventStore
	approver  *tools.Slot
	workspace string

	timeout  time.Duration
	closeErr error
	closed   bool
}

// Service is the command authority for Session and Turn use cases.
func (assembly *Assembly) Service() *application.Service { return assembly.service }

// Host owns lifecycle: readiness, heartbeat, and lease ownership.
func (assembly *Assembly) Host() *runtime.Host { return assembly.host }

// Store is the canonical event stream.
func (assembly *Assembly) Store() application.EventStore { return assembly.store }

// Open validates the configuration, constructs every component in dependency
// order, and returns a running assembly.
//
// Construction order is fixed and explicit: Runtime Host (which opens the
// SQLite store and completes startup reconciliation), then the provider model
// and turn runner, then the workspace filesystem and command runner, then the
// tool catalog, then the Application service.
//
// Open never returns a non-nil Assembly with a non-nil error, and never
// leaves a partially constructed assembly running: if any step fails, every
// resource already built is released before returning.
func Open(ctx context.Context, config Config) (*Assembly, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", errInvalidConfig)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	config = config.withDefaults()

	apiKey := os.Getenv(config.Provider.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: environment variable %s is empty", errInvalidConfig, config.Provider.APIKeyEnv)
	}

	host, err := runtime.Launch(ctx, runtime.Config{
		SQLite:         sqlite.Config{Path: config.DatabasePath, RuntimeID: config.RuntimeID},
		AuditDirectory: config.AuditDirectory,
	})
	if err != nil {
		return nil, fmt.Errorf("composition: launch runtime host: %w", err)
	}
	// From here on every failure path must release the host, which owns the
	// store, the lease, and the background loops.
	release := func(cause error) (*Assembly, error) {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if shutdownErr := host.Shutdown(shutdownCtx); shutdownErr != nil {
			return nil, errors.Join(cause, fmt.Errorf("composition: release after failure: %w", shutdownErr))
		}
		return nil, cause
	}

	// The concrete store is retained only long enough to read the writer
	// authority the lease assigned; everything downstream sees the port.
	sqliteStore, err := host.Store()
	if err != nil {
		return release(fmt.Errorf("composition: host store unavailable: %w", err))
	}
	var store application.EventStore = sqliteStore

	model, err := openaicompat.New(openaicompat.Config{
		BaseURL: config.Provider.BaseURL,
		ModelID: config.Provider.ModelID,
		APIKey:  openaicompat.StaticAPIKey{Value: apiKey},
		// The assembly always enables the workspace tool catalog, and
		// Application refuses a catalog whose provider profile does not
		// support native tools. Text-only would make every assembly invalid.
		Profile:               openaicompat.ProfileToolsSupported(config.Provider.ContextWindow, config.Provider.MaxOutput),
		AllowInsecureLoopback: config.Provider.AllowInsecureLoopback,
	})
	if err != nil {
		return release(fmt.Errorf("composition: provider adapter: %w", err))
	}
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		return release(fmt.Errorf("composition: turn runner: %w", err))
	}

	files, err := workspacefs.New(config.WorkspaceRoot)
	if err != nil {
		return release(fmt.Errorf("composition: workspace filesystem: %w", err))
	}
	commands, err := localexec.New(config.WorkspaceRoot)
	if err != nil {
		return release(fmt.Errorf("composition: command runner: %w", err))
	}
	catalog, err := tools.NewCatalog(tools.DefaultWorkspaceSpecs())
	if err != nil {
		return release(fmt.Errorf("composition: tool catalog: %w", err))
	}

	appConfig := application.DefaultConfig()
	appConfig.PolicyMode = config.Policy
	appConfig.Catalog = catalog
	appConfig.Files = files
	appConfig.Commands = commands
	approver := tools.NewSlot(config.Approver)
	appConfig.Approver = approver
	identity := model.Identity()
	appConfig.RequestIdentity = &identity
	if config.Limits.MaxSteps > 0 {
		appConfig.MaxSteps = config.Limits.MaxSteps
	}
	if config.Limits.MaxToolCallsPerStep > 0 {
		appConfig.MaxToolCallsPerStep = config.Limits.MaxToolCallsPerStep
	}
	if config.Limits.MaxAssistantBytes > 0 {
		appConfig.MaxAssistantBytes = config.Limits.MaxAssistantBytes
	}
	if config.Limits.ApprovalTimeout > 0 {
		appConfig.ApprovalTimeout = config.Limits.ApprovalTimeout
	}

	// Pass the store itself as the AuthoritySource: the Service then reads
	// the live fencing token per append, so an expired-takeover rotation is
	// picked up instead of wedging every append behind a stale snapshot.
	service, err := application.NewService(store, system.IDs{}, system.Clock{}, runner, sqliteStore, appConfig)
	if err != nil {
		return release(fmt.Errorf("composition: application service: %w", err))
	}

	return &Assembly{
		service:   service,
		host:      host,
		store:     store,
		approver:  approver,
		workspace: config.WorkspaceRoot,
		timeout:   config.ShutdownTimeout,
	}, nil
}

// ServeACP speaks ACP v1 JSON-RPC on in/out until in closes or ctx is done.
// The writer receives only ACP frames.
func (assembly *Assembly) ServeACP(ctx context.Context, in io.Reader, out io.Writer) error {
	if assembly == nil {
		return fmt.Errorf("composition: serve acp: assembly is nil")
	}
	return acp.Serve(ctx, acp.Config{
		Sessions:  assembly.service,
		History:   assembly.store,
		Workspace: assembly.workspace,
		Approver:  assembly.approver,
	}, in, out)
}

// Close stops admission, waits for the host's loops within the configured
// bound, releases the lease, and closes the store. It is idempotent: a second
// call returns the first result rather than shutting down again.
func (assembly *Assembly) Close() error {
	if assembly == nil {
		return nil
	}
	if assembly.closed {
		return assembly.closeErr
	}
	assembly.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), assembly.timeout)
	defer cancel()
	assembly.closeErr = assembly.host.Shutdown(ctx)
	return assembly.closeErr
}
