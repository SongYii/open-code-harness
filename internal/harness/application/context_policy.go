package application

import (
	"context"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
)

func planContext(ctx context.Context, deps ContextOrchestratorDeps, previous *contextengine.ContextCheckpoint, trigger string, input contextengine.PlanInput) (contextengine.PlanResult, error) {
	var through, tokens uint64
	if previous != nil {
		through = previous.Coverage.ThroughSequence
		tokens = previous.CheckpointTokens
	}
	return contextengine.Plan(ctx, deps.Policy, input, trigger, through, tokens)
}
