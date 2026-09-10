# Live Judge Structured-output Research

**Date:** 2026-09-10

## Trigger

The first DeepSeek validation produced two Scores from the same immutable
Attempt. One passed; one returned exactly 4096 output tokens and ended in the
existing strict decoder's Indeterminate path because the JSON was truncated.
The prompt already says `JSON`, supplies an exact object example, and forbids
prose, so another prompt-only instruction would not address the wire behavior.

## Provider findings

DeepSeek's current Chat Completions API accepts
`response_format: {"type":"json_object"}`. Its JSON Output guide requires the
prompt to mention JSON and include an example, recommends a sufficient output
budget, and warns that an empty response can still occur. The current judge
prompt already meets the prompt requirements.

DeepSeek V4 enables thinking by default and defaults reasoning effort to high.
The API exposes `thinking: {"type":"disabled"}`. A bounded classification
judge does not need an unbounded hidden reasoning phase; disabling it keeps the
configured output cap available to the strict JSON result.

Primary sources:

- [Chat Completions API](https://api-docs.deepseek.com/api/create-chat-completion/)
- [JSON Output](https://api-docs.deepseek.com/guides/json_mode/)
- [Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode/)

## Decision

Require `responseFormat: "json_object"` in every JudgeConfig. Carry the
optional, provider-specific `thinkingMode` through the OpenAI-compatible
adapter and its auditable request identity; the checked-in DeepSeek config
freezes it to `disabled`, while providers without that extension omit it. Keep
malformed, empty, and truncated output fail-closed as Indeterminate.

Do not hide an automatic retry inside `RunJudge`. One provider invocation must
remain one append-only Score with its own usage and cost. An operator may run
`och-eval judge` again; the second paid observation is then visible rather than
silently merged with the first.
