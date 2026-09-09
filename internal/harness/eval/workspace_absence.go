package eval

const VerifierWorkspacePathsAbsent = "workspace-paths-absent-v1"

func verifyWorkspacePathsAbsent(reader *ArtifactReader, scenario Scenario) CriterionResult {
	checked := 0
	for _, action := range scenario.Actions {
		if action.Type != ActionCollect || action.Collect == nil ||
			action.Collect.ExpectedState != WorkspaceExpectedAbsent {
			continue
		}
		checked++
		var matches []ManifestEntry
		for _, entry := range reader.Entries("workspace") {
			if entry.ProducedBy == string(action.ID) {
				matches = append(matches, entry)
			}
		}
		if len(matches) != 1 || matches[0].State != EntryCollected {
			return CriterionResult{ID: VerifierWorkspacePathsAbsent, Status: ScoreIndeterminate}
		}
		data, err := reader.ReadEntry(matches[0].Path)
		if err != nil {
			return CriterionResult{ID: VerifierWorkspacePathsAbsent, Status: ScoreIndeterminate}
		}
		var observation workspacePathObservation
		if err := decodeStrict(data, &observation); err != nil ||
			observation.FormatVersion != FormatVersion ||
			observation.Schema != schemaWorkspacePathObservation ||
			observation.ActionID != action.ID ||
			observation.WorkspacePath != action.Collect.WorkspacePath {
			return CriterionResult{ID: VerifierWorkspacePathsAbsent, Status: ScoreIndeterminate}
		}
		switch observation.State {
		case WorkspaceExpectedAbsent:
		case WorkspaceExpectedPresent:
			return CriterionResult{ID: VerifierWorkspacePathsAbsent, Status: ScoreFail}
		default:
			return CriterionResult{ID: VerifierWorkspacePathsAbsent, Status: ScoreIndeterminate}
		}
	}
	if checked == 0 {
		return CriterionResult{ID: VerifierWorkspacePathsAbsent, Status: ScoreIndeterminate}
	}
	return CriterionResult{ID: VerifierWorkspacePathsAbsent, Status: ScorePass}
}
