package skills

import (
	"sort"
	"strings"
	"testing"
)

func TestTopicsAreAllThereAndListed(t *testing.T) {
	want := append([]string(nil), Names()...)
	sort.Strings(want)
	got := Files()
	if strings.Join(want, ",") != strings.Join(got, ",") {
		t.Fatalf("topics/*.md %v and the declared order %v differ", got, want)
	}
	if len(Topics()) != len(Names()) {
		t.Fatalf("a declared topic has no file")
	}
	for _, tp := range Topics() {
		if !strings.HasPrefix(tp.Body, "# outfit: ") {
			t.Errorf("%s does not start with '# outfit: '", tp.Name)
		}
		if tp.Summary == "" {
			t.Errorf("%s has no summary", tp.Name)
		}
		if tp.Name == "install" || tp.Name == "remove" {
			t.Errorf("topic %q collides with the `outfit skills` subcommand of that name", tp.Name)
		}
	}
}

func TestPointerIsSmallAndMarked(t *testing.T) {
	if !strings.Contains(Pointer, "\n  "+Marker+"\n") {
		t.Error("the pointer skill lacks the managed-by marker in its frontmatter")
	}
	if len(Pointer) > 4096 {
		t.Errorf("the pointer is %d bytes: it is a pointer, documentation belongs in a topic", len(Pointer))
	}
	if !strings.HasPrefix(Pointer, "---\nname: outfit\n") {
		t.Error("the pointer must start with frontmatter naming the skill 'outfit'")
	}
}
