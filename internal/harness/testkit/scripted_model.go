package testkit

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

type ScriptedModelConfig struct {
	Steps                      []ScriptedStep
	StartupError               error
	ReturnStreamOnStartupError bool
	ReturnNilStream            bool
	CloseError                 error
}

type ScriptedStep struct {
	Event         engine.StreamEvent
	Err           error
	WaitForCancel bool
	Entered       chan<- struct{}
	Release       <-chan struct{}
}

// ScriptedModel is a deterministic Model fixture. Model.Stream is safe for
// concurrent call recording; every returned stream is independently consumed.
// Expected ModelRequest DeepEqual includes Messages and Tools (nil = Input-only).
// Configured steps may emit tool_call events.
type ScriptedModel struct {
	mu         sync.Mutex
	expected   engine.ModelRequest
	config     ScriptedModelConfig
	calls      []engine.ModelRequest
	nextCalls  int
	closeCalls int
}

func NewScriptedModel(expected engine.ModelRequest, config ScriptedModelConfig) (*ScriptedModel, error) {
	config.Steps = append([]ScriptedStep(nil), config.Steps...)
	return &ScriptedModel{expected: cloneModelRequest(expected), config: config}, nil
}

func (model *ScriptedModel) Stream(_ context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.calls = append(model.calls, cloneModelRequest(request))
	if !reflect.DeepEqual(request, model.expected) {
		return nil, &engine.Error{Code: engine.CodeInvalidRequest}
	}
	if model.config.ReturnNilStream {
		return nil, model.config.StartupError
	}
	stream := &scriptedStream{model: model, steps: append([]ScriptedStep(nil), model.config.Steps...), closeError: model.config.CloseError}
	if model.config.StartupError != nil && !model.config.ReturnStreamOnStartupError {
		return nil, model.config.StartupError
	}
	return stream, model.config.StartupError
}

func (model *ScriptedModel) Calls() []engine.ModelRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	cloned := make([]engine.ModelRequest, len(model.calls))
	for index, request := range model.calls {
		cloned[index] = cloneModelRequest(request)
	}
	return cloned
}

func cloneModelRequest(request engine.ModelRequest) engine.ModelRequest {
	cloned := request
	cloned.Messages = clonePromptMessages(request.Messages)
	cloned.Tools = cloneToolSchemas(request.Tools)
	return cloned
}

func clonePromptMessages(messages []domain.ModelPromptMessage) []domain.ModelPromptMessage {
	if messages == nil {
		return nil
	}
	cloned := make([]domain.ModelPromptMessage, len(messages))
	for index, message := range messages {
		cloned[index] = message
		if message.ToolCalls != nil {
			cloned[index].ToolCalls = append([]domain.ToolCallOffer(nil), message.ToolCalls...)
		}
	}
	return cloned
}

func cloneToolSchemas(schemas []domain.ToolSchema) []domain.ToolSchema {
	if schemas == nil {
		return nil
	}
	cloned := make([]domain.ToolSchema, len(schemas))
	for index, schema := range schemas {
		cloned[index] = schema
		if schema.InputSchema != nil {
			cloned[index].InputSchema = append([]byte(nil), schema.InputSchema...)
		}
	}
	return cloned
}
func (model *ScriptedModel) NextCalls() int {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.nextCalls
}
func (model *ScriptedModel) CloseCalls() int {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.closeCalls
}

type scriptedStream struct {
	model      *ScriptedModel
	steps      []ScriptedStep
	index      int
	closeError error
}

func (stream *scriptedStream) Next(ctx context.Context) (engine.StreamEvent, error) {
	stream.model.mu.Lock()
	stream.model.nextCalls++
	stream.model.mu.Unlock()
	if stream.index >= len(stream.steps) {
		return engine.StreamEvent{}, errors.New("script exhausted")
	}
	step := stream.steps[stream.index]
	stream.index++
	if step.Entered != nil {
		step.Entered <- struct{}{}
	}
	if step.Release != nil {
		select {
		case <-step.Release:
		case <-ctx.Done():
			return engine.StreamEvent{}, ctx.Err()
		}
	}
	if step.WaitForCancel {
		<-ctx.Done()
		return engine.StreamEvent{}, ctx.Err()
	}
	return step.Event, step.Err
}
func (stream *scriptedStream) Close() error {
	stream.model.mu.Lock()
	defer stream.model.mu.Unlock()
	stream.model.closeCalls++
	return stream.closeError
}
