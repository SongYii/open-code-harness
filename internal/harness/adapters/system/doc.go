// Package system implements the application Clock and IDGenerator ports
// against the host: the wall clock, and random identifiers.
//
// These ports had no production implementation. Only testkit satisfied them,
// and no production package may import testkit, so the harness could not be
// assembled outside a test — the gap the composition slice exists to close.
//
// The package performs no I/O beyond reading the clock and the system random
// source, decides nothing, and holds no state that survives a call.
package system
