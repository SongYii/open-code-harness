package eval

import (
	"fmt"
	"strconv"
)

// NormalizedArgv derives och's exact CLI argv for launching an ACP
// subprocess writer under subject's own validated configuration (design
// §11/§16): every Provider/Policy/Limits/Context field BuildConfig maps
// for the in-process executor, expressed as och's own flags (cmd/och's
// bindAssemblyFlags). It carries no credential value — only the
// credential env var's name — no absolute Attempt-specific path
// (workspace/database/runtime ID/audit directory are Attempt facts a
// launcher assigns per-launch, design §16, never Subject identity), and
// no unrelated environment value: exactly what design §11's
// ACPSubprocessIdentity.NormalizedArgv records as part of Executor
// identity. It does not append "-acp" itself; a launcher combines this
// with that flag and its own Attempt-specific path flags.
//
// Subject.Context is always fully specified — SubjectContext.validate
// requires every field positive, unlike composition.Config's own
// convenience zero-means-default — so every Context flag is always
// emitted once subject.Validate has passed. Subject.Policy.Limits is the
// one place zero legitimately means "use the Application default" (design
// §10 mirrors composition.Limits' own convention there), so a zero Limits
// field is omitted rather than emitted as "0", preserving that convention
// on both routes: the CLI applies the exact same default an in-process
// eval.BuildConfig call would for the same zero Subject field.
func NormalizedArgv(subject Subject) ([]string, error) {
	if err := subject.Validate(); err != nil {
		return nil, fmt.Errorf("eval: normalized argv: %w", err)
	}

	argv := []string{
		"-provider-url", subject.Provider.NormalizedEndpoint,
		"-model", subject.Provider.ModelID,
		"-api-key-env", subject.Provider.CredentialEnvVar,
		"-context-window", formatUint32(subject.Provider.ContextWindow),
		"-max-output", formatUint32(subject.Provider.MaxOutput),
		"-policy", subject.Policy.Mode,
	}
	if subject.Provider.Lane == ProviderLaneFixture {
		argv = append(argv, "-provider-allow-insecure-loopback")
	}
	if subject.Policy.SandboxPolicy == SandboxPolicyUnsandboxedAllowed {
		argv = append(argv, "-allow-unsandboxed-exec")
	}

	limits := subject.Policy.Limits
	if limits.MaxSteps > 0 {
		argv = append(argv, "-max-steps", strconv.Itoa(limits.MaxSteps))
	}
	if limits.MaxToolCallsPerStep > 0 {
		argv = append(argv, "-max-tool-calls-per-step", strconv.Itoa(limits.MaxToolCallsPerStep))
	}
	if limits.MaxAssistantBytes > 0 {
		argv = append(argv, "-max-assistant-bytes", strconv.Itoa(limits.MaxAssistantBytes))
	}
	if limits.ApprovalTimeout > 0 {
		argv = append(argv, "-approval-timeout", limits.ApprovalTimeout.String())
	}

	// subject.Validate (above) already required every one of these
	// positive, so they are always emitted -- never conditionally, unlike
	// the Limits fields above.
	context := subject.Context
	argv = append(argv,
		"-context-trigger-percent", formatUint32(context.TriggerPercent),
		"-context-target-percent", formatUint32(context.TargetPercent),
		"-context-tail-percent", formatUint32(context.TailPercent),
		"-context-max-summary-chunks", formatUint32(context.MaxSummaryChunks),
		"-context-max-overflow-compactions-per-turn", formatUint32(context.MaxOverflowCompactionsPerTurn),
		"-context-max-pruned-tool-results-per-request", formatUint32(context.MaxPrunedToolResultsPerRequest),
		"-context-compaction-timeout", context.CompactionTimeout.String(),
	)
	return argv, nil
}

func formatUint32(value uint32) string {
	return strconv.FormatUint(uint64(value), 10)
}
