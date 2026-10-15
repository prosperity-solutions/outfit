//go:build !faultinject

package apply

// fault marks a point where scenario 7 (docs/HANDOVER.md §7.4) kills the process in
// a binary built with -tags faultinject; in every other build it is nothing.
func fault(string) {}
