// Package anthropic owns the experimental deepseek-messages engine.Model route.
// It uses the pinned Anthropic SDK for HTTP requests, typed encoding and indexed
// content accumulation, with bounded raw-input and completion admission outside
// the SDK. SDK structs never cross the adapter boundary.
//
// The decoder preserves ordered content blocks rather than flattening thinking
// into DeepSeek's reasoningContent. Application retains the tool loop and the
// durable history; this adapter owns HTTP, cancellation and route binding. This
// is not a general Claude provider, a request-prefix-binding implementation, or
// a public Provider SDK. Native redacted-block fixtures are conformance only.
package anthropic
