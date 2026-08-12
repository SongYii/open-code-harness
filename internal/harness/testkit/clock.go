// Package testkit provides deterministic implementations of production-owned
// ports for contract, integration, and race tests.
package testkit

import "time"

// FixedClock always returns Time normalized to UTC.
type FixedClock struct {
	Time time.Time
}

func (c FixedClock) Now() time.Time { return c.Time.UTC() }
