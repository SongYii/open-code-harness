package composition

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/acp"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/anthropic"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/localexec"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/mcp"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/openaicompat"
	oteladapter "github.com/SongYii/open-code-harness/internal/harness/adapters/otel"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/system"
	"github.com/SongYii/open-code-harness/internal/harness/adapters/workspacefs"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/runtime"
	"github.com/SongYii/open-code-harness/internal/harness/telemetry"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// Assembly is a running harness: a Runtime Host owning the durable store, and
// an Application service wired to a provider, the workspace tools, and the
// policy engine. Accessors are read-only; the assembly owns every resource it
// returns and releases them in Close.
type Assembly struct {
	service   Service
	host      *runtime.Host
	store     application.EventStore
	approver  *tools.Slot
	workspace string
	catalog   *tools.Catalog
	mcp       mcpServers
	telemetry *oteladapter.Adapter
	commands  *localexec.Runner

	timeout   time.Duration
	closeErr  error
	closeOnce sync.Once
}

// Catalog is the single tool catalog this assembly built: the four builtin
// workspace tools plus every tool discovered from a configured MCP server.
// One catalog is the whole point of projecting external tools into
// domain.ToolSpec — they inherit the same Policy table, Approver slot, and
// audit trail rather than needing a second mechanism.
func (assembly *Assembly) Catalog() *tools.Catalog {
	if assembly == nil {
		return nil
	}
	return assembly.catalog
}

// Service is the command authority for Session and Turn use cases.
func (assembly *Assembly) Service() Service { return assembly.service }

// Ready reports whether the assembly currently admits work. It is an
// observation, not a reservation; Service and Store still admit each call.
func (assembly *Assembly) Ready() bool { return assembly.host.Ready() }

// Done closes when admission stops, including shutdown or lease loss. It
// signals cancellation, not completed teardown; Close must still be called.
// Only observation is exposed: callers cannot obtain the Host or its raw store.
func (assembly *Assembly) Done() <-chan struct{} { return assembly.host.WorkContext().Done() }

// Store is the canonical event stream.
func (assembly *Assembly) Store() application.EventStore {
	return &managedStore{host: assembly.host, inner: assembly.store}
}

// checkSandboxAvailability is a seam over localexec.Availability so a test
// can force "unavailable" without needing to actually break the host's
// bwrap or sandbox-exec. Production never reassigns it.
var checkSandboxAvailability = localexec.Availability

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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	config = config.withDefaults()
	if config.Diagnostics == nil {
		config.Diagnostics = os.Stderr
	}
	contextPolicy, policyIdentity, err := resolveContextPolicy(config)
	if err != nil {
		return nil, err
	}

	apiKey := os.Getenv(config.Provider.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: environment variable %s is empty", errInvalidConfig, config.Provider.APIKeyEnv)
	}

	// Fail closed ahead of any resource construction, exactly like the
	// credential check above: refusing later would mean a database file
	// and a lease existed before the assembly could possibly work.
	if available, reason := checkSandboxAvailability(); !available {
		if !config.AllowUnsandboxedExec {
			return nil, fmt.Errorf("%w: exec sandbox is unavailable and AllowUnsandboxedExec is false: %s", errInvalidConfig, reason)
		}
		fmt.Fprintf(config.Diagnostics, "composition: AllowUnsandboxedExec is true - proceeding without OS-level exec confinement: %s\n", reason)
	}

	var tracer telemetry.Tracer = telemetry.Noop()
	var traceAdapter *oteladapter.Adapter
	if config.Telemetry.OTLPTraceEndpoint != "" {
		candidate, err := oteladapter.New(ctx, oteladapter.Config{
			Endpoint: config.Telemetry.OTLPTraceEndpoint, SampleRatio: config.Telemetry.SampleRatio,
			AllowInsecureLoopback: config.Telemetry.AllowInsecureLoopback,
			InstanceID:            config.RuntimeID, Diagnostics: config.Diagnostics,
		})
		if err != nil {
			return nil, fmt.Errorf("composition: telemetry adapter: %w", err)
		}
		traceAdapter = candidate
		tracer = traceAdapter
	}

	host, err := runtime.Launch(ctx, runtime.Config{
		SQLite:         sqlite.Config{Path: config.DatabasePath, RuntimeID: config.RuntimeID},
		AuditDirectory: config.AuditDirectory,
		Telemetry:      tracer,
	})
	if err != nil {
		if traceAdapter != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
			traceAdapter.Shutdown(shutdownCtx)
			cancel()
		}
		return nil, fmt.Errorf("composition: launch runtime host: %w", err)
	}
	// From here on every failure path must release the host, which owns the
	// store, the lease, and the background loops.
	release := func(cause error) (*Assembly, error) {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		shutdownErr := host.Shutdown(shutdownCtx)
		if traceAdapter != nil {
			traceAdapter.Shutdown(shutdownCtx)
		}
		if shutdownErr != nil {
			return nil, errors.Join(cause, fmt.Errorf("composition: release after failure: %w", shutdownErr))
		}
		return nil, cause
	}
	abandon := func(cause error) (*Assembly, error) {
		host.Abandon()
		if traceAdapter != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
			traceAdapter.Shutdown(shutdownCtx)
			cancel()
		}
		return nil, fmt.Errorf("composition: startup teardown unproven; terminate process before restarting: %w", cause)
	}

	// The concrete store is retained only long enough to read the writer
	// authority the lease assigned; everything downstream sees the port.
	sqliteStore, err := host.Store()
	if err != nil {
		return release(fmt.Errorf("composition: host store unavailable: %w", err))
	}
	var store application.EventStore = sqliteStore

	protocol := ""
	if config.Provider.AdapterKind == "deepseek" {
		protocol = domain.DeepSeekThinkingV1
	}
	var model interface {
		engine.Model
		Identity() engine.RequestIdentity
	}
	if config.Provider.AdapterKind == "deepseek-messages" {
		model, err = anthropic.New(anthropic.Config{
			BaseURL: config.Provider.BaseURL, ModelID: config.Provider.ModelID, APIKey: apiKey,
			ContextWindow: config.Provider.ContextWindow, MaxOutput: config.Provider.MaxOutput,
			ReasoningEffort: engine.ReasoningEffort(config.Provider.ReasoningEffort), AllowInsecureLoopback: config.Provider.AllowInsecureLoopback,
		})
	} else {
		model, err = openaicompat.New(openaicompat.Config{
			Protocol: protocol,
			BaseURL:  config.Provider.BaseURL,
			ModelID:  config.Provider.ModelID,
			APIKey:   openaicompat.StaticAPIKey{Value: apiKey},
			// The assembly always enables the workspace tool catalog, and
			// Application refuses a catalog whose provider profile does not
			// support native tools. Text-only would make every assembly invalid.
			Profile: openaicompat.ProfileToolsSupported(config.Provider.ContextWindow, config.Provider.MaxOutput),
			Hints: openaicompat.WireHints{
				IncludeUsage:    config.Provider.IncludeUsage,
				MaxTokensField:  config.Provider.MaxTokensField,
				ThinkingMode:    config.Provider.ThinkingMode,
				ReasoningEffort: engine.ReasoningEffort(config.Provider.ReasoningEffort),
			},
			AllowInsecureLoopback: config.Provider.AllowInsecureLoopback,
		})
	}
	if err != nil {
		return release(fmt.Errorf("composition: provider adapter: %w", err))
	}
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		return release(fmt.Errorf("composition: turn runner: %w", err))
	}

	// Context meter/engine + summarizer (design §21's construction order):
	// the summarizer is built over the SAME runner/model the conversation
	// path uses, never a second Provider (design §18). budget was already
	// proven constructible by Config.Validate; ComputeBudget is called
	// again here (not carried through as a value) so this is the single
	// place that ever derives it, matching every other resource's own
	// build-not-thread-through convention in this function.
	contextMeter := contextengine.WireEstimateMeter{}
	contextBudget, err := contextengine.ComputeBudget(config.Provider.ContextWindow, config.Provider.MaxOutput, contextengine.BudgetConfig{
		TriggerPercent: config.Context.TriggerPercent, TargetPercent: config.Context.TargetPercent, TailPercent: config.Context.TailPercent,
	})
	if err != nil {
		return release(fmt.Errorf("composition: context budget: %w", err))
	}
	contextSummarizer, err := application.NewEngineContextSummarizerWithTelemetry(runner, engine.ReasoningEffort(config.Context.SummaryReasoningEffort), tracer)
	if err != nil {
		return release(fmt.Errorf("composition: context summarizer: %w", err))
	}

	files, err := workspacefs.New(config.WorkspaceRoot)
	if err != nil {
		return release(fmt.Errorf("composition: workspace filesystem: %w", err))
	}
	commands, err := localexec.New(config.WorkspaceRoot)
	if err != nil {
		return release(fmt.Errorf("composition: command runner: %w", err))
	}
	// MCP servers are connected before the catalog is built, because their
	// discovered tools join the same catalog the builtins do — one catalog,
	// one name-uniqueness check, one Policy table, one audit trail. A
	// configured server that cannot be reached fails Open; see
	// connectMCPServers for why there is no degraded mode.
	mcpSpecs, mcpConnected, err := connectMCPServers(ctx,
		config.MCPServers,
		confinedCommandFactory{runner: commands, workspace: config.WorkspaceRoot})
	if err != nil {
		if errors.Is(err, mcp.ErrTeardownUnproven) {
			return abandon(err)
		}
		return release(errors.Join(err, commands.Close()))
	}
	releaseWithMCP := func(cause error) (*Assembly, error) {
		if closeErr := mcpConnected.close(); closeErr != nil {
			return abandon(errors.Join(cause, closeErr))
		}
		return release(errors.Join(cause, commands.Close()))
	}

	catalog, err := tools.NewCatalog(append(tools.DefaultWorkspaceSpecs(), mcpSpecs...))
	if err != nil {
		return releaseWithMCP(fmt.Errorf("composition: tool catalog: %w", err))
	}

	appConfig := application.DefaultConfig()
	appConfig.PolicyMode = config.Policy
	appConfig.Catalog = catalog
	appConfig.Files = files
	appConfig.Commands = commands
	if len(mcpSpecs) > 0 {
		appConfig.ExternalTools = newExternalToolRouter(mcpConnected, mcpSpecs)
	}
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
	appConfig.Context = application.ContextConfig{
		Policy: contextPolicy, PolicyIdentity: policyIdentity,
		Enabled:                        true,
		Budget:                         contextBudget,
		Meter:                          contextMeter,
		Summarizer:                     contextSummarizer,
		CheckpointStore:                sqliteStore,
		MaxOverflowRecoveriesPerTurn:   config.Context.MaxOverflowCompactionsPerTurn,
		CompactionTimeout:              config.Context.CompactionTimeout,
		MaxSummaryChunks:               config.Context.MaxSummaryChunks,
		MaxPrunedToolResultsPerRequest: config.Context.MaxPrunedToolResultsPerRequest,
	}
	appConfig.Telemetry = tracer

	// Pass the store itself as the AuthoritySource: the Service then reads
	// the live fencing token per append, so an expired-takeover rotation is
	// picked up instead of wedging every append behind a stale snapshot.
	service, err := application.NewService(store, system.IDs{}, system.Clock{}, runner, sqliteStore, appConfig)
	if err != nil {
		return releaseWithMCP(fmt.Errorf("composition: application service: %w", err))
	}
	if !host.Ready() {
		return releaseWithMCP(fmt.Errorf("composition: lease lost during startup; restart required"))
	}
	if err := ctx.Err(); err != nil {
		return releaseWithMCP(err)
	}

	return &Assembly{
		service:   &managedService{host: host, inner: service},
		commands:  commands,
		host:      host,
		store:     store,
		approver:  approver,
		workspace: config.WorkspaceRoot,
		catalog:   catalog,
		mcp:       mcpConnected,
		telemetry: traceAdapter,
		timeout:   config.ShutdownTimeout,
	}, nil
}

// ServeACP speaks ACP v1 JSON-RPC on in/out until in closes or ctx is done.
// The writer receives only ACP frames.
//
// in is owned for the duration of the call and closed when ctx is cancelled,
// which is what lets a cancelled context unblock a read that is waiting for a
// frame that will never arrive.
func (assembly *Assembly) ServeACP(ctx context.Context, in io.ReadCloser, out io.Writer) error {
	if assembly == nil {
		return fmt.Errorf("composition: serve acp: assembly is nil")
	}
	work, finish, err := assembly.host.Admit(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return acp.Serve(work, acp.Config{
		Sessions:  assembly.service,
		History:   assembly.Store(),
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
	assembly.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), assembly.timeout)
		defer cancel()
		if err := assembly.host.Drain(ctx); err != nil {
			assembly.closeErr = err
			return // Resources still in use cannot safely be destroyed.
		}
		// One accounted-for teardown worker bounds the whole leaf phase,
		// including multiple MCP servers. On timeout it may still be reaping;
		// never release ownership or start a successor on that assumption.
		leaves := make(chan error, 1)
		go func() {
			if err := assembly.mcp.close(); err != nil {
				leaves <- err
				return
			}
			var err error
			if assembly.commands != nil {
				err = assembly.commands.Close()
			}
			leaves <- err
		}()
		select {
		case err := <-leaves:
			assembly.closeErr = err
		case <-ctx.Done():
			assembly.closeErr = fmt.Errorf("composition: resource teardown unproven: %w", ctx.Err())
		}
		if assembly.closeErr != nil {
			assembly.host.Abandon()
			return
		}
		assembly.closeErr = assembly.host.Shutdown(ctx)
		if assembly.telemetry != nil {
			assembly.telemetry.Shutdown(ctx)
		}
	})
	return assembly.closeErr
}
