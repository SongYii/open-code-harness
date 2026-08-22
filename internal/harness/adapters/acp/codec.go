package acp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

const (
	jsonRPCVersion = "2.0"
	maxFrameBytes  = 1 << 20

	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (err rpcError) Error() string {
	return err.Message
}

type frameWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *frameWriter) write(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if bytes.Contains(payload, []byte{'\n'}) {
		return fmt.Errorf("acp: encoded frame contains a newline")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.writer.Write(append(payload, '\n'))
	return err
}

func (w *frameWriter) writeResult(id json.RawMessage, result any) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return w.write(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{JSONRPC: jsonRPCVersion, ID: id, Result: payload})
}

func (w *frameWriter) writeError(id json.RawMessage, code int, message string) error {
	return w.write(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id,omitempty"`
		Error   rpcError        `json:"error"`
	}{JSONRPC: jsonRPCVersion, ID: id, Error: rpcError{Code: code, Message: message}})
}

func (w *frameWriter) writeNotification(method string, params any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return w.write(struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{JSONRPC: jsonRPCVersion, Method: method, Params: payload})
}

func decodeFrames(reader io.Reader, emit func(rpcRequest) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var message rpcRequest
		if err := json.Unmarshal(line, &message); err != nil || message.JSONRPC != jsonRPCVersion {
			if err := emit(rpcRequest{Error: &rpcError{Code: codeParseError, Message: "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if err := emit(message); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func idKey(id json.RawMessage) string {
	return string(id)
}
