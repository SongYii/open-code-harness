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
	store      EventStoreV2
	ids        IDGenerator
	clock      Clock
	runner     *engine.TurnRunner
	authority  WriterAuthority
	config     Config
	executions *executionRegistry
}

func NewService(store EventStoreV2, ids IDGenerator, clock Clock, runner *engine.TurnRunner, authority WriterAuthority, config Config) (*Service, error) {
	if isNilValue(store) || isNilValue(ids) || isNilValue(clock) || runner == nil || authority.Validate() != nil || config.MaxAssistantBytes <= 0 || config.TerminalCommitTimeout <= 0 {
		return nil, applicationError(CategoryValidation, "invalid_configuration", false, nil)
	}
	return &Service{store: store, ids: ids, clock: clock, runner: runner, authority: authority, config: config, executions: newExecutionRegistry()}, nil
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
