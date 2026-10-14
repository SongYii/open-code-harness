package application

import (
	"errors"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func classifyUnprovenCommand(err error) (string, string) {
	switch {
	case errors.Is(err, tools.ErrCommandTeardownUnproven):
		return CodeExecTeardownUnproven, ToolTextExecTeardownUnproven
	case errors.Is(err, tools.ErrCommandOutputIncomplete):
		return CodeExecOutputIncomplete, ToolTextExecOutputIncomplete
	case errors.Is(err, tools.ErrCommandWaitUnproven):
		return CodeExecWaitUnproven, ToolTextExecWaitUnproven
	default:
		return "", ""
	}
}

func isUnprovenCommandCode(code string) bool {
	return code == CodeExecTeardownUnproven || code == CodeExecOutputIncomplete || code == CodeExecWaitUnproven
}
