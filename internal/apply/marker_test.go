package apply

import (
	"testing"

	"github.com/prosperity-solutions/outfit/internal/plan"
	"github.com/prosperity-solutions/outfit/internal/state"
)

// The planner writes the marker line and the pre-commit guard looks for it: two
// packages, one text. They must never drift apart.
func TestMarkerIsTheSameInPlanAndState(t *testing.T) {
	if plan.Marker != state.SparseMarker {
		t.Fatalf("plan.Marker %q != state.SparseMarker %q", plan.Marker, state.SparseMarker)
	}
}
