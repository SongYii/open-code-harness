package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func frame(typ, fields string) string {
	if fields != "" {
		fields = "," + fields
	}
	return "event: " + typ + "\ndata: {\"type\":\"" + typ + "\"" + fields + "}\n\n"
}

func start() string {
	return frame("message_start", `"message":{"id":"msg_fixture","type":"message","role":"assistant","model":"fixture-model","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}`)
}

func stop(reason string) string {
	return frame("message_delta", `"delta":{"stop_reason":"`+reason+`","stop_sequence":null},"usage":{"output_tokens":17}`) + frame("message_stop", "")
}

func blockStart(index int, block string) string {
	return frame("content_block_start", fmt.Sprintf(`"index":%d,"content_block":%s`, index, block))
}

func blockStop(index int) string {
	return frame("content_block_stop", fmt.Sprintf(`"index":%d`, index))
}

func delta(index int, typ, key, value string) string {
	encoded, _ := json.Marshal(value)
	return frame("content_block_delta", fmt.Sprintf(`"index":%d,"delta":{"type":%q,%q:%s}`, index, typ, key, encoded))
}

func textBlock(index int, text string) string {
	return blockStart(index, `{"type":"text","text":""}`) + delta(index, "text_delta", "text", text) + blockStop(index)
}

func thinkingBlock(index int, thinking, signature string) string {
	return blockStart(index, `{"type":"thinking","thinking":""}`) + delta(index, "thinking_delta", "thinking", thinking) + delta(index, "signature_delta", "signature", signature) + blockStop(index)
}

func toolBlock(index int, id, input string) string {
	return blockStart(index, `{"type":"tool_use","id":"`+id+`","name":"read_file","input":{}}`) + delta(index, "input_json_delta", "partial_json", input) + blockStop(index)
}

func TestDecodePreservesOrderedBlocksAndSignatures(t *testing.T) {
	wire := start() + thinkingBlock(0, "private fixture", "signature-one") +
		textBlock(1, "before ") + toolBlock(2, "tool_1", `{"path":"a","n":9007199254740993}`) +
		// Omitted thinking has no thinking_delta at all, not an invented one.
		blockStart(3, `{"type":"thinking","thinking":""}`) +
		delta(3, "signature_delta", "signature", "signature-") + delta(3, "signature_delta", "signature", "two") + blockStop(3) +
		blockStart(4, `{"type":"redacted_thinking","data":"encrypted-fixture"}`) + blockStop(4) +
		toolBlock(5, "tool_2", `{}`) + textBlock(6, "after") + stop("tool_use")
	got, err := decodeMessage(strings.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	if got.visibleText() != "before after" || got.StopReason != "tool_use" || got.Model != "fixture-model" || got.ID != "msg_fixture" {
		t.Fatal("visible projection or message identity changed")
	}
	replay, err := got.replayContent()
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"thinking","thinking":"private fixture","signature":"signature-one"},{"type":"text","text":"before "},{"type":"tool_use","id":"tool_1","name":"read_file","input":{"path":"a","n":9007199254740993}},{"type":"thinking","thinking":"","signature":"signature-two"},{"type":"redacted_thinking","data":"encrypted-fixture"},{"type":"tool_use","id":"tool_2","name":"read_file","input":{}},{"type":"text","text":"after"}]`
	if string(replay) != want {
		t.Fatalf("replay block order/content changed: %s", replay)
	}
	if got.Usage != (usage{InputTokens: 12, OutputTokens: 17, CacheReadTokens: 3, CacheCreationTokens: 4}) {
		t.Fatalf("usage must retain separate counters and replace cumulative output: %+v", got.Usage)
	}
	// A replay byte slice is detached; mutating it cannot modify decoded state.
	replay[0] = '!'
	again, err := got.replayContent()
	if err != nil || string(again) != want {
		t.Fatal("replay output aliases decoded state")
	}
}

func TestDecodeAcceptsNativeVariations(t *testing.T) {
	for name, wire := range nativeVariationFixtures() {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeMessage(strings.NewReader(wire)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func nativeVariationFixtures() map[string]string {
	plain := start() + textBlock(0, "你好 👋") + stop("end_turn")
	return map[string]string{
		"adaptive skipped thinking": plain,
		"crlf":                      strings.ReplaceAll(plain, "\n", "\r\n"),
		"comment and ping":          ": keepalive\n\n" + frame("ping", "") + plain,
		"multiline data":            strings.Replace(plain, `,"message":`, ",\ndata: \"message\":", 1),
		"usage updates":             strings.Replace(plain, frame("message_stop", ""), frame("message_delta", `"delta":{},"usage":{"output_tokens":18}`)+frame("message_stop", ""), 1),
		"fragmented arguments":      start() + blockStart(0, `{"type":"tool_use","id":"tool_1","name":"read_file","input":{}}`) + delta(0, "input_json_delta", "partial_json", `{"path":`) + delta(0, "input_json_delta", "partial_json", `"a"}`) + blockStop(0) + stop("tool_use"),
		"omitted thinking":          start() + blockStart(0, `{"type":"thinking","thinking":""}`) + delta(0, "signature_delta", "signature", "opaque") + blockStop(0) + textBlock(1, "ok") + stop("end_turn"),
	}
}

func TestDecodeFailsClosedWithoutPublishingPartialMessage(t *testing.T) {
	valid := start() + thinkingBlock(0, "hidden fixture", "sig") + textBlock(1, "ok") + stop("end_turn")
	if _, err := decodeMessage(strings.NewReader(valid)); err != nil {
		t.Fatalf("positive control: %v", err)
	}
	for name, wire := range rejectedFixtures() {
		t.Run(name, func(t *testing.T) { assertRejected(t, wire) })
	}
}

func rejectedFixtures() map[string]string {
	valid := start() + thinkingBlock(0, "hidden fixture", "sig") + textBlock(1, "ok") + stop("end_turn")
	return map[string]string{
		"missing message start":                strings.TrimPrefix(valid, start()),
		"duplicate message start":              start() + valid,
		"missing message stop":                 strings.TrimSuffix(valid, frame("message_stop", "")),
		"EOF inside frame":                     strings.TrimSuffix(valid, "\n\n"),
		"wrong event name":                     strings.Replace(valid, "event: message_start", "event: message_delta", 1),
		"unknown event":                        start() + frame("new_event", "") + textBlock(0, "ok") + stop("end_turn"),
		"provider error":                       start() + frame("error", `"error":{"type":"overloaded_error","message":"private"}`),
		"open block at finish":                 start() + blockStart(0, `{"type":"text","text":"ok"}`) + stop("end_turn"),
		"duplicate block stop":                 start() + textBlock(0, "ok") + blockStop(0) + stop("end_turn"),
		"missing index":                        strings.Replace(valid, `"index":0,`, "", 1),
		"null index":                           strings.Replace(valid, `"index":0`, `"index":null`, 1),
		"negative index":                       strings.Replace(valid, `"index":0`, `"index":-1`, 1),
		"out of order index":                   strings.Replace(valid, `"index":0`, `"index":1`, 1),
		"mixed delta":                          strings.Replace(valid, `"thinking_delta","thinking"`, `"text_delta","text"`, 1),
		"missing signature":                    strings.Replace(valid, delta(0, "signature_delta", "signature", "sig"), "", 1),
		"empty signature":                      strings.Replace(valid, `"signature":"sig"`, `"signature":""`, 1),
		"thinking after signature":             strings.Replace(valid, blockStop(0), delta(0, "thinking_delta", "thinking", "late")+blockStop(0), 1),
		"thinking after empty signature delta": strings.Replace(valid, delta(0, "signature_delta", "signature", "sig"), delta(0, "signature_delta", "signature", "")+delta(0, "thinking_delta", "thinking", "late")+delta(0, "signature_delta", "signature", "sig"), 1),
		"length termination":                   strings.Replace(valid, `"stop_reason":"end_turn"`, `"stop_reason":"max_tokens"`, 1),
		"refusal":                              strings.Replace(valid, `"stop_reason":"end_turn"`, `"stop_reason":"refusal"`, 1),
		"tool finish without tools":            strings.Replace(valid, `"stop_reason":"end_turn"`, `"stop_reason":"tool_use"`, 1),
		"tools without tool finish":            start() + toolBlock(0, "tool_1", `{}`) + stop("end_turn"),
		"duplicate tool IDs":                   start() + toolBlock(0, "tool_1", `{}`) + toolBlock(1, "tool_1", `{}`) + stop("tool_use"),
		"nonobject tool input":                 start() + toolBlock(0, "tool_1", `[]`) + stop("tool_use"),
		"truncated tool input":                 start() + toolBlock(0, "tool_1", `{"path":`) + stop("tool_use"),
		"duplicate tool input key":             start() + toolBlock(0, "tool_1", `{"path":"a","path":"b"}`) + stop("tool_use"),
		"lossy tool input":                     start() + toolBlock(0, "tool_1", `{"path":"\ud800"}`) + stop("tool_use"),
		"null content":                         strings.Replace(valid, `"content":[]`, `"content":null`, 1),
		"start with content snapshot":          strings.Replace(valid, `"content":[]`, `"content":[{"type":"text","text":"snapshot"}]`, 1),
		"nonempty input snapshot":              strings.Replace(start()+toolBlock(0, "tool_1", `{}`)+stop("tool_use"), `"input":{}`, `"input":{"path":"snapshot"}`, 1),
		"duplicate fields":                     strings.Replace(valid, `"thinking":"hidden fixture"`, `"thinking":"hidden fixture","thinking":"changed"`, 1),
		"unknown block field":                  strings.Replace(valid, `"thinking":"hidden fixture"`, `"thinking":"hidden fixture","extra":"state"`, 1),
		"unpaired surrogate":                   strings.Replace(valid, "hidden fixture", `\ud800`, 1),
		"invalid utf8":                         strings.Replace(valid, "hidden fixture", string([]byte{0xff}), 1),
		"null usage":                           strings.Replace(valid, `"usage":{"output_tokens":17}`, `"usage":null`, 1),
		"negative usage":                       strings.Replace(valid, `"output_tokens":17`, `"output_tokens":-1`, 1),
		"overflow usage":                       strings.Replace(valid, `"output_tokens":17`, `"output_tokens":18446744073709551616`, 1),
		"decreasing usage":                     strings.Replace(valid, `"output_tokens":17`, `"output_tokens":0`, 1),
		"null usage counter":                   strings.Replace(valid, `"output_tokens":17`, `"output_tokens":null`, 1),
		"encrypted block missing data":         start() + blockStart(0, `{"type":"redacted_thinking"}`) + blockStop(0) + stop("end_turn"),
		"fallback block":                       start() + blockStart(0, `{"type":"fallback","model":"other"}`) + blockStop(0) + stop("end_turn"),
		"input transformations":                strings.Replace(valid, `"content":[]`, `"content":[],"input_transformations":[]`, 1),
	}
}

func assertRejected(t *testing.T, wire string) {
	t.Helper()
	got, err := decodeMessage(strings.NewReader(wire))
	if err != errProtocol || !reflect.DeepEqual(got, message{}) {
		t.Fatalf("must reject with a fixed error and no partial result; got error=%v", err)
	}
}

func TestDecodeSecretRejectionDoesNotRewriteReplay(t *testing.T) {
	secret := "sk-" + strings.Repeat("a", 48)
	assertRejected(t, start()+thinkingBlock(0, secret, "sig")+textBlock(1, "ok")+stop("end_turn"))
	assertRejected(t, start()+textBlock(0, secret)+stop("end_turn"))
	assertRejected(t, start()+textBlock(0, secret[:3])+textBlock(1, secret[3:])+stop("end_turn"))
	// Opaque cryptographic material isn't ordinary text: never redact or
	// interpret it, including when it happens to resemble a secret shape.
	got, err := decodeMessage(strings.NewReader(start() + thinkingBlock(0, "", secret) + textBlock(1, "ok") + stop("end_turn")))
	if err != nil || got.Content[0].Signature != secret {
		t.Fatal("opaque signature was interpreted or rewritten")
	}
}

func TestDecodeResourceBounds(t *testing.T) {
	for name, wire := range map[string]string{
		"line":       start() + ":" + strings.Repeat("x", maxSSELineBytes) + "\n\n",
		"data lines": "event: ping\n" + strings.Repeat("data: \n", maxSSEDataLines+1) + "\n\n",
		"event":      "event: ping\n" + strings.Repeat("data: "+strings.Repeat(" ", 2048)+"\n", maxSSEEventBytes/2048+1) + "\n",
		"tool input": start() + toolBlock(0, "tool_1", `{"value":"`+strings.Repeat("x", maxToolInputBytes)+`"}`) + stop("tool_use"),
		"total wire": strings.Repeat(":"+strings.Repeat("x", 1022)+"\n", maxStreamBytes/1024+1) + start() + textBlock(0, "ok") + stop("end_turn"),
	} {
		t.Run(name, func(t *testing.T) { assertRejected(t, wire) })
	}
	for _, size := range []int{maxThinkingBytes, maxThinkingBytes + 1} {
		// Small frames ensure this exercises the assembled hidden-state limit,
		// not the independent per-line scanner limit.
		wire := start() + blockStart(0, `{"type":"thinking","thinking":""}`)
		for left := size; left > 0; {
			n := min(left, 4096)
			wire += delta(0, "signature_delta", "signature", strings.Repeat("s", n))
			left -= n
		}
		wire += blockStop(0) + textBlock(1, "ok") + stop("end_turn")
		got, err := decodeMessage(strings.NewReader(wire))
		if size == maxThinkingBytes {
			if err != nil || len(got.Content[0].Signature) != size {
				t.Fatal("hidden-state boundary positive control failed")
			}
		} else {
			assertRejected(t, wire)
		}
	}
	var wire strings.Builder
	wire.WriteString(start())
	for index := 0; index < maxBlocks; index++ {
		wire.WriteString(textBlock(index, "x"))
	}
	if _, err := decodeMessage(strings.NewReader(wire.String() + stop("end_turn"))); err != nil {
		t.Fatal("block-count boundary positive control failed")
	}
	wire.WriteString(textBlock(maxBlocks, "x"))
	wire.WriteString(stop("end_turn"))
	assertRejected(t, wire.String())
}

func TestDecodeContentAndToolInputExactBounds(t *testing.T) {
	for _, size := range []int{maxContentBytes, maxContentBytes + 1} {
		var wire strings.Builder
		wire.WriteString(start() + blockStart(0, `{"type":"text","text":""}`))
		for left := size; left > 0; {
			n := min(left, 16384)
			wire.WriteString(delta(0, "text_delta", "text", strings.Repeat("x", n)))
			left -= n
		}
		wire.WriteString(blockStop(0) + stop("end_turn"))
		got, err := decodeMessage(strings.NewReader(wire.String()))
		if size == maxContentBytes {
			if err != nil || len(got.visibleText()) != size {
				t.Fatal("content-byte boundary positive control failed")
			}
		} else {
			assertRejected(t, wire.String())
		}
	}
	for _, size := range []int{maxToolInputBytes, maxToolInputBytes + 1} {
		input := `{"v":"` + strings.Repeat("x", size-len(`{"v":""}`)) + `"}`
		wire := start() + toolBlock(0, "tool_1", input) + stop("tool_use")
		got, err := decodeMessage(strings.NewReader(wire))
		if size == maxToolInputBytes {
			if err != nil || string(got.Content[0].Input) != input {
				t.Fatal("tool-input boundary positive control failed")
			}
		} else {
			assertRejected(t, wire)
		}
	}
}

func TestReplayJSONDoesNotRepairOrCollapseValues(t *testing.T) {
	for _, valid := range []string{`{"v":"\ud83d\udc4b"}`, `{"v":"\\ud800"}`, `{"v":9007199254740993}`, `{"x":{"a":1},"y":{"a":2}}`} {
		if !validReplayJSON([]byte(valid)) {
			t.Fatalf("valid control rejected: %s", valid)
		}
	}
	for _, invalid := range []string{`{"v":"\udc4b"}`, `{"v":"\ud83dX"}`, `{"v":"\ud83d\u0061"}`, `{"x":{"a":1,"\u0061":2}}`, `{} {}`, strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66)} {
		if validReplayJSON([]byte(invalid)) {
			t.Fatalf("lossy/ambiguous/unbounded JSON accepted: %s", invalid)
		}
	}
}

type failAfterReader struct{ reader io.Reader }

func (reader failAfterReader) Read(data []byte) (int, error) {
	n, err := reader.reader.Read(data)
	if err == io.EOF {
		err = fmt.Errorf("sensitive transport detail")
	}
	return n, err
}

func TestDecodeStopsAtProtocolTerminalAndSanitizesReaderFailures(t *testing.T) {
	valid := start() + textBlock(0, "ok") + stop("end_turn")
	if _, err := decodeMessage(failAfterReader{strings.NewReader(valid)}); err != nil {
		t.Fatal("must not wait for transport EOF after message_stop")
	}
	got, err := decodeMessage(failAfterReader{strings.NewReader(start() + textBlock(0, "partial"))})
	if err != errProtocol || !reflect.DeepEqual(got, message{}) {
		t.Fatal("read error exposed transport details or partial state")
	}
}

func FuzzDecodeMessage(f *testing.F) {
	f.Add(start() + textBlock(0, "ok") + stop("end_turn"))
	f.Add(start() + thinkingBlock(0, "", "sig") + toolBlock(1, "tool_1", `{}`) + stop("tool_use"))
	f.Add(`\ud800`)
	f.Fuzz(func(t *testing.T, wire string) {
		got, err := decodeMessage(strings.NewReader(wire))
		if err != nil {
			if err != errProtocol || !reflect.DeepEqual(got, message{}) {
				t.Fatal("failure published partial content")
			}
			return
		}
		encoded, err := got.replayContent()
		if err != nil || !validReplayJSON(encoded) || got.validate() != nil {
			t.Fatal("successful decode cannot be replayed")
		}
	})
}
