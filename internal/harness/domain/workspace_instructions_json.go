package domain

import "encoding/json"

func validateWorkspaceInstructionsJSON(data json.RawMessage) error {
	if err := validateInstructionObjectArray(data, "discovered", []string{"path", "scope"}, nil); err != nil {
		return err
	}
	if err := validateInstructionObjectArray(
		data,
		"changes",
		[]string{"action", "path", "scope"},
		[]string{"priorDigest", "digest", "content"},
	); err != nil {
		return err
	}
	return validateInstructionObjectArray(data, "diagnostics", []string{"path", "class"}, nil)
}

func validateInstructionObjectArray(data json.RawMessage, key string, required, optional []string) error {
	fields, err := jsonObjectFields(data)
	if err != nil {
		return err
	}
	raw, ok := fields[key]
	if !ok {
		return nil
	}
	if !isJSONArray(raw) {
		return invalidEventError("JSON array is required")
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return invalidEventError("invalid event data")
	}
	for _, element := range elements {
		if err := validateJSONObjectKeys(element, required, optional); err != nil {
			return err
		}
	}
	return nil
}
