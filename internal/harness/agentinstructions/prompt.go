package agentinstructions

import (
	_ "embed"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

const (
	PromptID      = "och_coding_agent_v1"
	PromptVersion = "1.0.0"
	PromptDigest  = "sha256:8060287d0ddc132ebce66e955b8749804a06d1d1b494f77b23afbe49c2f0fd2d"
)

//go:embed prompts/och_coding_agent_v1.md
var promptText string

func SystemPromptMessage() domain.ModelPromptMessage {
	return domain.ModelPromptMessage{
		Role: domain.PromptRoleSystem,
		Text: promptText,
	}
}
