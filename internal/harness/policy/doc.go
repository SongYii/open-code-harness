// Package policy separates pure, side-effect-free decision strategies from the
// core authorization guard. The guard owns non-bypassable denials and validates
// strategy output; the package does not import host I/O, tools, or Application,
// and tools cannot self-authorize.
package policy
