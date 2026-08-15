package application

import (
	"reflect"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

const (
	DefaultMaxAssistantBytes             = 1 << 20
	DefaultTerminalCommitTimeout         = 5 * time.Second
	DefaultAppendResolutionTimeout       = 5 * time.Second
	DefaultAppendResolutionMaxOperations = 4
)

type Config struct {
	MaxAssistantBytes             int
	TerminalCommitTimeout         time.Duration
	AppendResolutionTimeout       time.Duration
	AppendResolutionMaxOperations uint32
}

func DefaultConfig() Config {
	return Config{
		MaxAssistantBytes:             DefaultMaxAssistantBytes,
		TerminalCommitTimeout:         DefaultTerminalCommitTimeout,
		AppendResolutionTimeout:       DefaultAppendResolutionTimeout,
		AppendResolutionMaxOperations: DefaultAppendResolutionMaxOperations,
	}
}

// Service is the single application command authority for Session and Turn
// use cases. Its dependencies and configuration are immutable after creation.
type Service struct {
	store      EventStore
	ids        IDGenerator
	clock      Clock
	runner     *engine.TurnRunner
	authority  WriterAuthority
	config     Config
	executions *executionRegistry
}

func NewService(store EventStore, ids IDGenerator, clock Clock, runner *engine.TurnRunner, authority WriterAuthority, config Config) (*Service, error) {
	if config.AppendResolutionTimeout == 0 {
		config.AppendResolutionTimeout = DefaultAppendResolutionTimeout
	}
	if config.AppendResolutionMaxOperations == 0 {
		config.AppendResolutionMaxOperations = DefaultAppendResolutionMaxOperations
	}
	if isNilValue(store) || isNilValue(ids) || isNilValue(clock) || runner == nil || authority.Validate() != nil || config.MaxAssistantBytes <= 0 || config.TerminalCommitTimeout <= 0 || config.AppendResolutionTimeout <= 0 || config.AppendResolutionMaxOperations == 0 {
		return nil, applicationError(CategoryValidation, "invalid_configuration", false, nil)
	}
	return &Service{store: store, ids: ids, clock: clock, runner: runner, authority: authority, config: config, executions: newExecutionRegistry()}, nil
}

func (service *Service) appendResolutionConfig() AppendResolutionConfig {
	return AppendResolutionConfig{Timeout: service.config.AppendResolutionTimeout, MaxOperations: service.config.AppendResolutionMaxOperations}
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
