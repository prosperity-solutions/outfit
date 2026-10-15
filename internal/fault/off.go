//go:build !faultinject

// Package fault holds the numbered kill points of docs/HANDOVER.md scenario 7: places
// in the code that changes things where a test kills the process, to prove that a rerun
// converges with nothing lost. In every build but one made with -tags faultinject,
// Point is an empty function (docs/testing.md, "Fault injection").
package fault

// Point marks a kill point. Name it for what has happened when it is reached (the state
// of the tree), and give every call site its own name.
func Point(string) {}
