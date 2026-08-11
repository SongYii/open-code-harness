package domain

import "strings"

type SessionID string
type TurnID string
type CommandID string
type EventID string

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

func ParseCommandID(value string) (CommandID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return CommandID(value), nil
}

func ParseEventID(value string) (EventID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return EventID(value), nil
}

func validateID(value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return &DomainError{
			Code:    CodeInvalidID,
			Message: "identifier must not be blank or padded",
		}
	}
	return nil
}
