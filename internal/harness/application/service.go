package application

import (
	"reflect"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

const (
	DefaultMaxAssistantBytes     = 1 << 20
	DefaultTerminalCommitTimeout = 5 * time.Second
)

type Config struct {
	MaxAssistantBytes     int
	TerminalCommitTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{
		MaxAssistantBytes:     DefaultMaxAssistantBytes,
		TerminalCommitTimeout: DefaultTerminalCommitTimeout,
	}
}

// Service is the single application command authority for Session and Turn
// use cases. Its dependencies and configuration are immutable after creation.
type Service struct {
	store  EventStore
	ids    IDGenerator
	runner *engine.TurnRunner
	config Config
}

func NewService(store EventStore, ids IDGenerator, runner *engine.TurnRunner, config Config) (*Service, error) {
	if isNilValue(store) || isNilValue(ids) || runner == nil || config.MaxAssistantBytes <= 0 || config.TerminalCommitTimeout <= 0 {
		return nil, applicationError(CategoryValidation, "invalid_configuration", false, nil)
	}
	return &Service{store: store, ids: ids, runner: runner, config: config}, nil
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
