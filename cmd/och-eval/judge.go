package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/openaicompat"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

// judgeCommand runs one live quality judgement against an Attempt's
// already-committed evidence and prints the Score it appended.
//
// Its flag set deliberately carries no endpoint, model, prompt, or
// credential-value override. Every one of those comes from the frozen
// JudgeConfig the Attempt's own evidence proves it was entitled to use;
// a flag that could change any of them would produce a Score whose
// claimed configuration no reader could verify, which is the exact thing
// this command exists to prevent.
func judgeCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("och-eval judge", flag.ContinueOnError)
	flags.SetOutput(stderr)
	attemptPath := flags.String("attempt", "", "path to the Attempt's publication root")
	judgeConfigPath := flags.String("judge-config", "", "path to the frozen och.eval.judge-config document")
	priceTablePath := flags.String("price-table", "", "path to a frozen price table; required when the JudgeConfig names a priceTableDigest")
	live := flags.Bool("live", false, "confirm this is a live judge invocation (the dual-consent gate)")
	if err := flags.Parse(args); err != nil {
		return exitValidation
	}
	if *attemptPath == "" || *judgeConfigPath == "" {
		fmt.Fprintln(stderr, "och-eval judge: -attempt and -judge-config are both required")
		return exitValidation
	}

	config, err := loadJudgeConfig(*judgeConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge:", err)
		return exitValidation
	}
	priceTable, err := loadJudgePriceTable(config, *priceTablePath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge:", err)
		return exitValidation
	}

	// nil client and allowInsecureLoopback=false: production talks HTTPS
	// only. The adapter enforces that itself, so there is no path here
	// that could be talked into a plaintext endpoint.
	caller, err := newOpenAICompatibleJudgeCaller(config, nil, false)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge:", err)
		return exitValidation
	}

	return runJudgeAndReport(ctx, eval.AttemptRootDirectoriesFor(*attemptPath), config, *live, caller, priceTable, stdout, stderr)
}

// runJudgeAndReport is the half of judgeCommand a test can drive with its
// own caller, so the CLI's own exit mapping is exercised without a
// network.
func runJudgeAndReport(ctx context.Context, directories eval.AttemptRootDirectories, config eval.JudgeConfig, live bool, caller eval.JudgeCaller, priceTable *eval.PriceTable, stdout, stderr io.Writer) int {
	consent := eval.LiveConsent{Flag: live, Environment: os.Getenv("OCH_EVAL_LIVE_CONFIRM")}
	result, err := eval.EvaluateJudgeAttempt(ctx, directories, config, consent, caller, priceTable)
	if err != nil {
		// Everything EvaluateJudgeAttempt refuses — consent, frozen
		// binding, a legacy Attempt, a lane mismatch — happens before any
		// Score exists, so it is all one validation class.
		fmt.Fprintln(stderr, "och-eval judge:", err)
		return exitValidation
	}

	data, err := jsonEncode(result.Score)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge:", err)
		return exitInternal
	}
	if _, err := stdout.Write(data); err != nil {
		fmt.Fprintln(stderr, "och-eval judge:", err)
		return exitInternal
	}
	if result.PrerequisiteVerdict != eval.ScorePass {
		fmt.Fprintf(stderr, "och-eval judge: deterministic prerequisites returned %q; quality judging was not attempted\n",
			result.PrerequisiteVerdict)
	}

	// A published Score always exits zero. Live quality is advisory: it
	// reports on work that already passed its deterministic gates, so a
	// quality Fail is a finding to read, not a build to break. The gating
	// exit classes belong to `run` and `regrade`, which is where a
	// deterministic verifier's verdict is actually enforced.
	return exitOK
}

// newOpenAICompatibleJudgeCaller builds the real provider call from a
// frozen JudgeConfig. Every wire-shaping decision comes from that
// document; the two extra parameters exist only so a test can supply an
// httptest client and permit its loopback endpoint. Production passes
// (nil, false), which leaves the adapter's own HTTPS-only rule in force.
func newOpenAICompatibleJudgeCaller(config eval.JudgeConfig, client *http.Client, allowInsecureLoopback bool) (eval.JudgeCaller, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	model, err := openaicompat.New(openaicompat.Config{
		BaseURL: config.Provider.NormalizedEndpoint,
		ModelID: config.Provider.ModelID,
		APIKey:  openaicompat.EnvAPIKey{Name: config.Provider.CredentialEnvVar},
		Profile: openaicompat.ProfileTextOnly(config.Provider.ContextWindow, config.Provider.MaxOutput),
		Hints: openaicompat.WireHints{
			IncludeUsage:   config.Provider.IncludeUsage,
			MaxTokensField: config.Provider.MaxTokensField,
		},
		HTTPClient:            client,
		AllowInsecureLoopback: allowInsecureLoopback,
	})
	if err != nil {
		return nil, fmt.Errorf("build judge model for %q: %w", config.Provider.NormalizedEndpoint, err)
	}

	return func(ctx context.Context, systemPrompt, evidenceBundle string) (string, eval.ScorerUsage, error) {
		startedAt := time.Now()
		usage := eval.ScorerUsage{}
		stream, err := model.Stream(ctx, engine.ModelRequest{
			SessionID: domain.SessionID("och-eval-judge"),
			TurnID:    domain.TurnID("judge-turn"),
			ItemID:    domain.ItemID("judge-item"),
			Messages: []domain.ModelPromptMessage{
				{Role: "system", Text: systemPrompt},
				{Role: "user", Text: evidenceBundle},
			},
			Purpose: engine.ModelRequestPurposeQualityJudge,
		})
		if err != nil {
			return "", usage, err
		}
		defer func() { _ = stream.Close() }()

		var text strings.Builder
		for {
			event, err := stream.Next(ctx)
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return "", usage, err
			}
			text.WriteString(event.Text)
			if event.Usage != nil {
				usage.InputTokens = int64(event.Usage.InputTokens)
				usage.OutputTokens = int64(event.Usage.OutputTokens)
			}
			if event.Type == engine.StreamEventCompleted {
				break
			}
		}
		usage.DurationMillis = time.Since(startedAt).Milliseconds()
		return text.String(), usage, nil
	}, nil
}
