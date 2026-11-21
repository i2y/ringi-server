package server_test

import (
	"testing"

	server "github.com/i2y/ringi-server"
)

// TestTeamOf: a person's team of a kind is the first team of that kind at or above their primary
// team; their primary team when none at or above it is of the kind; "" with no primary membership,
// and of no version. Parents that lead back to a team below end the way up.
func TestTeamOf(t *testing.T) {
	v := &server.OrgVersion{Day: "2026-04-01",
		Teams: []server.Team{
			{ID: "company", Kind: "company"},
			{ID: "sales_div", Kind: "division", Parent: "company"},
			{ID: "sales1", Kind: "department", Parent: "sales_div"},
			{ID: "sales1_a", Kind: "section", Parent: "sales1"},
			{ID: "board", Kind: "committee", Parent: "company"},
			{ID: "loop_a", Kind: "section", Parent: "loop_b"},
			{ID: "loop_b", Kind: "section", Parent: "loop_a"},
		},
		Members: []server.Member{
			{Person: "e1203", Team: "board", Position: "staff"},
			{Person: "e1203", Team: "sales1_a", Position: "staff", Primary: true},
			{Person: "e3101", Team: "sales1", Position: "dept_head", Primary: true},
			{Person: "e0001", Team: "company", Position: "president", Primary: true},
			{Person: "e0002", Team: "board", Position: "staff"},
			{Person: "e9001", Team: "loop_a", Position: "staff", Primary: true},
			{Person: "e9002", Team: "gone", Position: "staff", Primary: true},
		},
	}
	for _, c := range []struct{ person, kind, want string }{
		{"e1203", "department", "sales1"},    // up from the section
		{"e1203", "division", "sales_div"},   // further up
		{"e1203", "company", "company"},      // to the top
		{"e1203", "section", "sales1_a"},     // the primary team itself
		{"e1203", "committee", "sales1_a"},   // a team of the kind they are in, but not above the primary: the primary team
		{"e3101", "department", "sales1"},    // a department's head
		{"e0001", "department", "company"},   // none at or above: the primary team
		{"e0002", "department", ""},          // in a team, but no primary membership
		{"e9999", "department", ""},          // not in the version
		{"e9001", "department", "loop_a"},    // parents in a loop: the primary team
		{"e9002", "department", "gone"},      // a primary team the version does not have
		{"e1203", "", "sales1_a"},            // no kind: the primary team
		{"e1203", "Department", "sales1_a"},  // kinds are compared as written
		{"e1203", "department ", "sales1_a"}, // so is a space
	} {
		if got := v.TeamOf(c.person, c.kind); got != c.want {
			t.Errorf("TeamOf(%q, %q) = %q, not %q", c.person, c.kind, got, c.want)
		}
	}
	var none *server.OrgVersion
	if got := none.TeamOf("e1203", "department"); got != "" {
		t.Errorf("TeamOf of no version = %q", got)
	}
}
