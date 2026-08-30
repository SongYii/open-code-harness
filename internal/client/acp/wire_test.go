package acp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFrameWriterRoundTripsOneMessagePerLine(t *testing.T) {
	var buf bytes.Buffer
	w := newFrameWriter(&buf)
	if err := w.writeMessage(message{JSONRPC: jsonRPCVersion, Method: "session/update", Params: json.RawMessage(`{"a":1}`)}); err != nil {
		t.Fatalf("writeMessage() err = %v", err)
	}
	if err := w.writeMessage(message{JSONRPC: jsonRPCVersion, ID: json.RawMessage(`"1"`), Result: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("writeMessage() err = %v", err)
	}

	var got []message
	err := decodeFrames(&buf, func(m message) error {
		got = append(got, m)
		return nil
	})
	if err != nil {
		t.Fatalf("decodeFrames() err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("decodeFrames() emitted %d messages, want 2: %#v", len(got), got)
	}
	if !got[0].isNotification() || got[0].Method != "session/update" {
		t.Fatalf("first message = %#v, want a session/update notification", got[0])
	}
	if !got[1].isResponse() || string(got[1].Result) != "{}" {
		t.Fatalf("second message = %#v, want a response with result {}", got[1])
	}
}

func TestFrameWriterRejectsAnInvalidRawMessagePayload(t *testing.T) {
	var buf bytes.Buffer
	w := newFrameWriter(&buf)
	// A json.RawMessage containing a raw, unescaped newline inside a
	// string literal is invalid JSON; encoding/json.Marshal fails on it
	// rather than emitting a corrupt frame, so writeMessage never needs
	// its own newline-scanning guard on top of that.
	invalid := json.RawMessage("\"line1\nline2\"")
	err := w.writeMessage(message{JSONRPC: jsonRPCVersion, Method: "session/update", Params: invalid})
	if err == nil {
		t.Fatal("writeMessage() with an invalid RawMessage payload = nil, want an error")
	}
	if buf.Len() != 0 {
		t.Fatalf("writeMessage() wrote %q despite rejecting the frame", buf.String())
	}
}

func TestFrameWriterCompactsInsignificantWhitespaceInAValidPayload(t *testing.T) {
	var buf bytes.Buffer
	w := newFrameWriter(&buf)
	// A pretty-printed but otherwise valid nested object contains real
	// newline bytes as insignificant whitespace; Marshal compacts a
	// nested RawMessage when embedding it, so the emitted line still
	// contains exactly one NDJSON frame, never a literal newline.
	prettyPrinted := json.RawMessage("{\n\"a\": 1\n}")
	if err := w.writeMessage(message{JSONRPC: jsonRPCVersion, Method: "session/update", Params: prettyPrinted}); err != nil {
		t.Fatalf("writeMessage() err = %v", err)
	}
	line := strings.TrimRight(buf.String(), "\n")
	if strings.Contains(line, "\n") {
		t.Fatalf("emitted frame contains an embedded newline: %q", buf.String())
	}
	if strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("emitted %d newlines, want exactly one line terminator: %q", strings.Count(buf.String(), "\n"), buf.String())
	}
}

func TestFrameWriterRejectsAnOversizedPayload(t *testing.T) {
	var buf bytes.Buffer
	w := newFrameWriter(&buf)
	huge := json.RawMessage(`"` + strings.Repeat("x", maxFrameBytes) + `"`)
	if err := w.writeMessage(message{JSONRPC: jsonRPCVersion, Method: "session/update", Params: huge}); err == nil {
		t.Fatal("writeMessage() with an oversized payload = nil, want an error")
	}
	if buf.Len() != 0 {
		t.Fatalf("writeMessage() wrote %q despite rejecting the oversized frame", buf.String())
	}
}

func TestDecodeFramesFailsHardOnAMalformedLine(t *testing.T) {
	r := strings.NewReader("not json\n")
	err := decodeFrames(r, func(message) error {
		t.Fatal("emit called for a malformed line")
		return nil
	})
	if err == nil {
		t.Fatal("decodeFrames() err = nil, want a parse failure")
	}
}

func TestDecodeFramesSkipsBlankLines(t *testing.T) {
	r := strings.NewReader("\n\n" + `{"jsonrpc":"2.0","method":"session/update","params":{}}` + "\n\n")
	var count int
	if err := decodeFrames(r, func(message) error { count++; return nil }); err != nil {
		t.Fatalf("decodeFrames() err = %v", err)
	}
	if count != 1 {
		t.Fatalf("decodeFrames() emitted %d messages, want 1", count)
	}
}

func TestMessageClassification(t *testing.T) {
	request := message{ID: json.RawMessage(`1`), Method: "session/request_permission"}
	if !request.isRequest() || request.isResponse() || request.isNotification() {
		t.Fatalf("request classified wrong: %#v", request)
	}
	response := message{ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`)}
	if !response.isResponse() || response.isRequest() || response.isNotification() {
		t.Fatalf("response classified wrong: %#v", response)
	}
	notification := message{Method: "session/update"}
	if !notification.isNotification() || notification.isRequest() || notification.isResponse() {
		t.Fatalf("notification classified wrong: %#v", notification)
	}
}
