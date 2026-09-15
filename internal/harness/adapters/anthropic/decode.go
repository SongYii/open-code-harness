package anthropic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
)

const (
	maxSSELineBytes  = 256 << 10
	maxSSEEventBytes = 1 << 20
	maxSSEDataLines  = 1024
	maxStreamBytes   = 8 << 20
)

// Never include provider-controlled content in decoder errors. The HTTP/engine
// integration must eventually classify failures without exposing wire bodies.
var errProtocol = errors.New("anthropic: invalid, incomplete, or unsupported Messages stream")

// decodeMessage has no background work and publishes nothing before a valid
// message_stop. The caller owns the reader, its cancellation, idle timeout and
// Close. It stops at the protocol terminal marker, not transport EOF.
func decodeMessage(reader io.Reader) (message, error) {
	return decodeMessageFor(reader, nativeMessages)
}

func decodeMessageFor(reader io.Reader, profile wireProfile) (message, error) {
	if reader == nil {
		return message{}, errProtocol
	}
	scanner := bufio.NewScanner(io.LimitReader(reader, maxStreamBytes+1))
	scanner.Buffer(make([]byte, 4096), maxSSELineBytes)
	wireBytes := 0
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		advance, token, err := bufio.ScanLines(data, atEOF)
		wireBytes += advance // Count CRLF and delimiters, not just decoded text.
		if wireBytes > maxStreamBytes {
			return 0, nil, errProtocol
		}
		return advance, token, err
	})
	state := accumulator{profile: profile}
	var data strings.Builder
	name, lines := "", 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() == 0 && name == "" {
				continue
			}
			if err := state.accept(name, []byte(data.String())); err != nil {
				return message{}, errProtocol
			}
			if state.complete {
				result := state.result()
				if result.validateFor(profile) != nil {
					return message{}, errProtocol
				}
				return result, nil
			}
			name, lines = "", 0
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			if name != "" {
				return message{}, errProtocol
			}
			name = value
		case "data":
			lines++
			if lines > maxSSEDataLines || data.Len()+len(value)+1 > maxSSEEventBytes {
				return message{}, errProtocol
			}
			data.WriteString(value)
			data.WriteByte('\n')
		case "id", "retry":
			// Transport metadata is not part of the assistant content.
		default:
			return message{}, errProtocol
		}
	}
	return message{}, errProtocol
}

type accumulator struct {
	// The SDK owns content accumulation. Only lifecycle, byte budgets, and raw
	// tool input are tracked independently: SDK refresh repairs malformed input.
	sdkMessage  sdk.Message
	profile     wireProfile
	blocks      []*blockGuard
	message     message
	started     bool
	open        int
	finishing   bool
	complete    bool
	contentSize int
	hiddenSize  int
}

type blockGuard struct {
	typ              string
	closed           bool
	inputDeltas      strings.Builder
	signatureStarted bool
	signaturePresent bool
}

func (state *accumulator) accept(name string, data []byte) error {
	if err := state.validateEvent(name, data); err != nil {
		return errProtocol
	}
	if name == "ping" {
		return nil
	}
	var event sdk.MessageStreamEventUnion
	if json.Unmarshal(data, &event) != nil || state.sdkMessage.Accumulate(event) != nil {
		return errProtocol
	}
	return nil
}

// Project only after message_stop, into adapter-owned values. SDK response
// objects (including raw wire and future SDK fields) never leave this boundary.
func (state *accumulator) result() message {
	result := state.message
	for index, block := range state.sdkMessage.Content {
		result.Content = append(result.Content, contentBlock{
			Type: block.Type, Text: block.Text, Thinking: block.Thinking,
			Signature: block.Signature, SignaturePresent: state.blocks[index].signaturePresent,
			Data: block.Data, ID: block.ID, Name: block.Name,
			Input: append(json.RawMessage(nil), block.Input...),
		})
	}
	return result
}

func (state *accumulator) validateEvent(name string, data []byte) error {
	fields, err := object(data, "type", "message", "index", "content_block", "delta", "usage", "error")
	if err != nil {
		return errProtocol
	}
	typ, err := stringField(fields, "type")
	if err != nil || typ != name || state.complete {
		return errProtocol
	}
	// Closed event shapes prevent unrelated fields (notably fallback and input
	// transformations) being silently discarded as if replay remained exact.
	allowed := map[string][]string{
		"ping": {"type"}, "message_start": {"type", "message"},
		"content_block_start": {"type", "index", "content_block"},
		"content_block_delta": {"type", "index", "delta"},
		"content_block_stop":  {"type", "index"},
		"message_delta":       {"type", "delta", "usage"}, "message_stop": {"type"},
	}
	keys, ok := allowed[typ]
	if !ok {
		return errProtocol // Includes server errors and unsupported event types.
	}
	if _, err := object(data, keys...); err != nil {
		return errProtocol
	}
	if typ == "ping" {
		return nil
	}
	if typ == "message_start" {
		return state.start(fields["message"])
	}
	if !state.started {
		return errProtocol
	}
	switch typ {
	case "content_block_start", "content_block_delta", "content_block_stop":
		var index int
		raw := bytes.TrimSpace(fields["index"])
		if state.finishing || len(raw) == 0 || raw[0] == 'n' || json.Unmarshal(raw, &index) != nil || index < 0 {
			return errProtocol
		}
		switch typ {
		case "content_block_start":
			if index != len(state.blocks) || len(state.blocks) >= maxBlocks {
				return errProtocol
			}
			return state.startBlock(fields["content_block"])
		case "content_block_delta":
			if index >= len(state.blocks) || state.blocks[index].closed {
				return errProtocol
			}
			return state.delta(state.blocks[index], fields["delta"])
		default:
			if index >= len(state.blocks) || state.blocks[index].closed {
				return errProtocol
			}
			block := state.blocks[index]
			if block.typ == "tool_use" && block.inputDeltas.Len() > 0 {
				input := block.inputDeltas.String()
				// Validate BEFORE Accumulate can refresh/repair this block. Raw
				// fragments are authoritative: SDK's {} sentinel can also reset
				// a valid empty object when trailing whitespace arrives separately.
				if !validReplayJSON([]byte(input)) || !strings.HasPrefix(strings.TrimSpace(input), "{") {
					return errProtocol
				}
				state.sdkMessage.Content[index].Input = json.RawMessage(input)
			}
			block.closed = true
			block.inputDeltas.Reset()
			state.open--
			return nil
		}
	case "message_delta":
		if state.open != 0 || len(state.blocks) == 0 {
			return errProtocol
		}
		delta, err := object(fields["delta"], "stop_reason", "stop_sequence")
		if err != nil || !nullOrAbsent(delta, "stop_sequence") {
			return errProtocol
		}
		if raw, present := delta["stop_reason"]; present && string(bytes.TrimSpace(raw)) != "null" {
			reason, err := stringField(delta, "stop_reason")
			if err != nil || reason != "end_turn" && reason != "tool_use" || state.message.StopReason != "" && state.message.StopReason != reason {
				return errProtocol
			}
			state.message.StopReason = reason
		}
		state.finishing = true
		return state.updateUsage(fields["usage"], false)
	case "message_stop":
		if state.open != 0 || !state.finishing || state.message.StopReason == "" {
			return errProtocol
		}
		state.complete = true
		return nil
	default:
		return errProtocol
	}
}

func (state *accumulator) start(data []byte) error {
	if state.started {
		return errProtocol
	}
	fields, err := object(data, "id", "type", "role", "content", "model", "stop_reason", "stop_sequence", "usage")
	if err != nil {
		return errProtocol
	}
	typ, _ := stringField(fields, "type")
	role, _ := stringField(fields, "role")
	id, _ := stringField(fields, "id")
	model, _ := stringField(fields, "model")
	var content []json.RawMessage
	if typ != "message" || role != "assistant" || !identifier(id) || model == "" || len(model) > 256 || strings.TrimSpace(model) != model ||
		json.Unmarshal(fields["content"], &content) != nil || content == nil || len(content) != 0 ||
		!nullOrAbsent(fields, "stop_reason") || !nullOrAbsent(fields, "stop_sequence") {
		return errProtocol
	}
	state.message.ID, state.message.Model = id, model
	state.started = true
	return state.updateUsage(fields["usage"], true)
}

func (state *accumulator) startBlock(data []byte) error {
	fields, err := object(data, "type", "text", "thinking", "signature", "data", "id", "name", "input")
	if err != nil {
		return errProtocol
	}
	typ, err := stringField(fields, "type")
	if err != nil {
		return errProtocol
	}
	var block contentBlock
	block.Type = typ
	var allowed []string
	switch typ {
	case "text":
		allowed = []string{"type", "text"}
		block.Text, err = stringField(fields, "text")
	case "thinking":
		allowed = []string{"type", "thinking", "signature"}
		block.Thinking, err = stringField(fields, "thinking")
		if _, present := fields["signature"]; present {
			var signatureErr error
			block.Signature, signatureErr = stringField(fields, "signature")
			if signatureErr != nil {
				err = signatureErr
			}
		}
	case "redacted_thinking":
		if state.profile == deepSeekMessages {
			return errProtocol
		}
		allowed = []string{"type", "data"}
		block.Data, err = stringField(fields, "data")
	case "tool_use":
		allowed = []string{"type", "id", "name", "input"}
		block.ID, err = stringField(fields, "id")
		block.Name, _ = stringField(fields, "name")
		// A non-empty input snapshot plus deltas is ambiguous. This streaming
		// subset starts tools with {} and assembles the subsequent fragments.
		input, inputErr := object(fields["input"])
		if inputErr != nil || len(input) != 0 || !identifier(block.ID) || !identifier(block.Name) {
			return errProtocol
		}
		block.Input = json.RawMessage(`{}`)
	default:
		return errProtocol
	}
	if err != nil {
		return errProtocol
	}
	if _, err := object(data, allowed...); err != nil {
		return errProtocol
	}
	state.contentSize += len(block.Text) + len(block.Thinking) + len(block.Signature) + len(block.Data) + len(block.ID) + len(block.Name) + len(block.Input)
	state.hiddenSize += len(block.Thinking) + len(block.Signature) + len(block.Data)
	if state.contentSize > maxContentBytes || state.hiddenSize > maxThinkingBytes {
		return errProtocol
	}
	_, signaturePresent := fields["signature"]
	state.blocks = append(state.blocks, &blockGuard{
		typ: typ, signaturePresent: signaturePresent, signatureStarted: block.Signature != "",
	})
	state.open++
	return nil
}

func (state *accumulator) delta(block *blockGuard, data []byte) error {
	fields, err := object(data, "type", "text", "thinking", "signature", "partial_json")
	if err != nil {
		return errProtocol
	}
	typ, _ := stringField(fields, "type")
	var field string
	switch {
	case typ == "text_delta" && block.typ == "text":
		field = "text"
	case typ == "thinking_delta" && block.typ == "thinking" && !block.signatureStarted:
		field = "thinking"
	case typ == "signature_delta" && block.typ == "thinking":
		field = "signature"
	case typ == "input_json_delta" && block.typ == "tool_use":
		field = "partial_json"
	default:
		return errProtocol
	}
	if _, err := object(data, "type", field); err != nil {
		return errProtocol
	}
	value, err := stringField(fields, field)
	if err != nil {
		return errProtocol
	}
	state.contentSize += len(value)
	if field == "thinking" || field == "signature" {
		state.hiddenSize += len(value)
	}
	if state.contentSize > maxContentBytes || state.hiddenSize > maxThinkingBytes {
		return errProtocol
	}
	switch field {
	case "signature":
		block.signatureStarted, block.signaturePresent = true, true
	case "partial_json":
		if block.inputDeltas.Len()+len(value) > maxToolInputBytes {
			return errProtocol
		}
		block.inputDeltas.WriteString(value)
	}
	return nil
}

func (state *accumulator) updateUsage(data []byte, initial bool) error {
	// Usage metadata may grow independently of replay content. Unknown counters
	// are tolerated, never guessed or added into known counters.
	if !validReplayJSON(data) || !strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
		return errProtocol
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return errProtocol
	}
	for name, destination := range map[string]*uint64{
		"input_tokens":                &state.message.Usage.InputTokens,
		"output_tokens":               &state.message.Usage.OutputTokens,
		"cache_read_input_tokens":     &state.message.Usage.CacheReadTokens,
		"cache_creation_input_tokens": &state.message.Usage.CacheCreationTokens,
	} {
		raw, present := fields[name]
		if !present {
			if name == "output_tokens" || initial && name == "input_tokens" {
				return errProtocol
			}
			continue
		}
		var count uint64
		if string(bytes.TrimSpace(raw)) == "null" || json.Unmarshal(raw, &count) != nil || count < *destination {
			return errProtocol
		}
		*destination = count // Cumulative, not a delta to add.
	}
	return nil
}

func nullOrAbsent(fields map[string]json.RawMessage, name string) bool {
	value, present := fields[name]
	return !present || string(bytes.TrimSpace(value)) == "null"
}
