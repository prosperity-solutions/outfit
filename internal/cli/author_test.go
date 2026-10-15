package cli

import (
	"reflect"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/state"
)

func TestPlanAuthor(t *testing.T) {
	committed := []string{"lean", "web", "yolo"}
	cases := []struct {
		name       string
		cur        state.State
		on, all    bool
		targets    []string
		wantAll    bool
		wantByName []string
	}{
		{"on, everything", state.State{}, true, true, nil, true, nil},
		{"on, named", state.State{}, true, false, []string{"web"}, false, []string{"web"}},
		{"on, named, merged and sorted", state.State{AuthorProfiles: []string{"web"}}, true, false, []string{"lean"}, false, []string{"lean", "web"}},
		{"on, named, while all is on: nothing to add", state.State{Author: true}, true, false, []string{"web"}, true, nil},
		{"on, only the active one named: not 'everything'", state.State{}, true, false, nil, false, nil},
		{"off, everything", state.State{Author: true, AuthorProfiles: []string{"web"}}, false, true, nil, false, nil},
		{"off, named", state.State{AuthorProfiles: []string{"lean", "web"}}, false, false, []string{"lean"}, false, []string{"web"}},
		{"off, named, from all: every other one stays, the active one is not listed", state.State{Author: true}, false, false, []string{"lean"}, false, []string{"web"}},
		{"off, only the active one named", state.State{AuthorProfiles: []string{"web"}}, false, false, nil, false, []string{"web"}},
	}
	for _, c := range cases {
		got := planAuthor(&c.cur, c.on, c.all, c.targets, committed, "yolo")
		if got.Author != c.wantAll || !reflect.DeepEqual(got.AuthorProfiles, c.wantByName) && (len(got.AuthorProfiles) != 0 || len(c.wantByName) != 0) {
			t.Errorf("%s: got all=%v list=%v, want all=%v list=%v", c.name, got.Author, got.AuthorProfiles, c.wantAll, c.wantByName)
		}
	}
}

func TestAuthorOutNeverListsTheActiveProfileOrADeletedOne(t *testing.T) {
	st := &state.State{AuthorProfiles: []string{"gone", "web", "yolo"}}
	v := authorOut(st, []string{"lean", "web", "yolo"}, "yolo")
	if !reflect.DeepEqual(v.Profiles, []string{"web"}) || !reflect.DeepEqual(v.OnDisk, []string{"web", "yolo"}) || !reflect.DeepEqual(v.Hidden, []string{"lean"}) {
		t.Fatalf("%+v", v)
	}
	if v := authorOut(&state.State{Author: true}, []string{"lean", "yolo"}, "yolo"); len(v.Hidden) != 0 || len(v.OnDisk) != 2 {
		t.Fatalf("all on: %+v", v)
	}
}
