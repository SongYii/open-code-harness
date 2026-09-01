package domain

import (
	"strings"
	"unicode/utf8"
)

type SessionID string
type TurnID string
type ItemID string
type CommandID string
type AppendID string
type RunTurnRequestID string
type EventID string
type ApprovalID string
type ContextCompactionID string
type ContextDecisionID string

func ParseSessionID(value string) (SessionID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return SessionID(value), nil
}

func ParseTurnID(value string) (TurnID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return TurnID(value), nil
}

func ParseItemID(value string) (ItemID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return ItemID(value), nil
}

func ParseCommandID(value string) (CommandID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return CommandID(value), nil
}

func ParseAppendID(value string) (AppendID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return AppendID(value), nil
}

func ParseRunTurnRequestID(value string) (RunTurnRequestID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return RunTurnRequestID(value), nil
}

func ParseEventID(value string) (EventID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return EventID(value), nil
}

func ParseApprovalID(value string) (ApprovalID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return ApprovalID(value), nil
}

func ParseContextCompactionID(value string) (ContextCompactionID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return ContextCompactionID(value), nil
}

func ParseContextDecisionID(value string) (ContextDecisionID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return ContextDecisionID(value), nil
}

func validateID(value string) error {
	if value == "" || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return &DomainError{
			Code:    CodeInvalidID,
			Message: "identifier must be valid UTF-8 and not blank or padded",
		}
	}
	return nil
}

func hasRequiredText(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != ""
}
