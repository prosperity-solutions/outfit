//go:build !faultinject

// Package fault holds the numbered kill points of docs/HANDOVER.md scenario 7: places
// in the code that changes things where a test kills the process, to prove that a rerun
// converges with nothing lost. In every build but one made with -tags faultinject,
// Point is an empty function (docs/testing.md, "Fault injection").
package fault

// Point marks a kill point.
func Point(string) {}
