package server

import (
	"slices"
	"testing"
)

// TestStageAgain checks which stage a ticket is at (its revision's), whom it waits for and who its
// approvers are, at a stage that waited for a later one: the list has it twice under one name, the
// first time as it was when it waited, with its approvers of then (ringi's DESIGN 5.12). At no stage,
// and where the revision places none of the list, the ticket is at none.
func TestStageAgain(t *testing.T) {
	ticket := func(status, at string) string {
		return `{"id":"T-1","request":"x","version":1,"requester":"ann","made_by":null,"status":"` + status + `","stage":"manager","revision":{"round":1,"stage":` + at + `,"edits":0},"stages":[
			{"name":"manager","phase":"deferred","started":1,"reminded":false,"parts":[{"way":"manager","approvers":["old","bea"],"state":"deferred","answers":[{"by":"bea","seat":"bea"}]}]},
			{"name":"owner","phase":"answered","started":2,"reminded":false,"parts":[{"way":"owner","approvers":["owen"],"state":"passed","answers":[{"by":"owen","seat":"owen"}]}]},
			{"name":"manager","phase":"active","started":3,"reminded":false,"parts":[
				{"way":"manager","approvers":["new","cal"],"state":"active","answers":[{"by":"pam","seat":"cal"}]},
				{"way":"note","approvers":["noa"],"state":"passed","answers":[]}]}]}`
	}
	for _, c := range []struct {
		what, status, at   string
		started            int64
		waiting, approvers []string
	}{
		{"at the stage again", "in_progress", "2", 3, []string{"new"}, []string{"cal", "new", "noa"}},
		{"paused there", "paused", "2", 3, []string{"new"}, []string{"cal", "new", "noa"}},
		{"at the stage between", "in_progress", "1", 2, []string{}, []string{"owen"}},
		{"at no stage", "approved", "null", 0, []string{}, []string{}},
		{"past the list", "in_progress", "3", 0, []string{}, []string{}},
		{"before the list", "in_progress", "-1", 0, []string{}, []string{}},
	} {
		v, err := readTicket([]byte(ticket(c.status, c.at)))
		if err != nil {
			t.Fatal(err)
		}
		st := v.current()
		if (st == nil) != (c.started == 0) || st != nil && *st.Started != c.started {
			t.Errorf("%s: the stage is %+v", c.what, st)
		}
		if got := waitingFor(v); !slices.Equal(got, c.waiting) {
			t.Errorf("%s: waits for %v, want %v", c.what, got, c.waiting)
		}
		if got := stageApprovers(v); !slices.Equal(got, c.approvers) {
			t.Errorf("%s: the approvers are %v, want %v", c.what, got, c.approvers)
		}
	}
}

// TestRevision checks when two revisions are one, and which a ticket at one is at no more (past): a
// round does not come back, nor does a ticket of a round with more edits taken; a stage does.
func TestRevision(t *testing.T) {
	read := func(s string) revisionView {
		r, err := readRevision([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, c := range []struct {
		a, b       string
		same, past bool
	}{
		{`{"round":1,"stage":0,"edits":0}`, `{"round":1,"stage":0,"edits":0}`, true, false},
		{`{"round":1,"stage":null,"edits":0}`, `{"round":1,"edits":0}`, true, false},
		{`{"round":1,"stage":1,"edits":0}`, `{"round":1,"stage":0,"edits":0}`, false, false},
		{`{"round":1,"stage":0,"edits":0}`, `{"round":1,"stage":1,"edits":0}`, false, false},
		{`{"round":1,"stage":0,"edits":0}`, `{"round":1,"stage":null,"edits":0}`, false, false},
		{`{"round":1,"stage":null,"edits":0}`, `{"round":1,"stage":0,"edits":0}`, false, false},
		{`{"round":1,"stage":0,"edits":1}`, `{"round":1,"stage":0,"edits":0}`, false, true},
		{`{"round":1,"stage":0,"edits":0}`, `{"round":1,"stage":0,"edits":1}`, false, false},
		{`{"round":2,"stage":0,"edits":0}`, `{"round":1,"stage":0,"edits":0}`, false, true},
		{`{"round":2,"stage":0,"edits":0}`, `{"round":1,"stage":0,"edits":1}`, false, true},
		{`{"round":1,"stage":0,"edits":1}`, `{"round":2,"stage":0,"edits":0}`, false, false},
		{`{"round":1,"stage":0,"edits":0}`, `{"round":2,"stage":0,"edits":0}`, false, false},
	} {
		a, b := read(c.a), read(c.b)
		if a.same(b) != c.same || a.past(b) != c.past {
			t.Errorf("%s and %s: same %v, past %v", c.a, c.b, a.same(b), a.past(b))
		}
	}
	for _, bad := range []string{``, `{`, `"x"`, `1`, `{"round":"1"}`, `{"round":1.5}`, `{"stage":[]}`} {
		if r, err := readRevision([]byte(bad)); err == nil {
			t.Errorf("%s is read as the revision %+v", bad, r)
		}
	}
}
