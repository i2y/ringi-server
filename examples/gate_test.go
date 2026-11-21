package examples

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
)

// The tests of the examples' gate (gate.go) on its own: whom it takes as one of a department, and
// the roles it reads.

// gateOrg is a version of a small organization (fictional): a company, a division, two departments
// and a section of one of them.
func gateOrg() *server.OrgVersion {
	return &server.OrgVersion{
		Day: "2026-04-01",
		Teams: []server.Team{
			{ID: "company", Name: "Example Co.", Kind: "company"},
			{ID: "corp", Name: "Corporate Division", Kind: "division", Parent: "company"},
			{ID: "hr", Name: "Human Resources", Kind: "department", Parent: "corp"},
			{ID: "hr_pay", Name: "Payroll", Kind: "section", Parent: "hr"},
			{ID: "it", Name: "Information Systems", Kind: "department", Parent: "corp"},
		},
		People: []server.OrgPerson{{ID: "p_hr", Name: "Payroll staff"}, {ID: "p_it", Name: "IT staff"}, {ID: "p_corp", Name: "Head of Corporate"}},
		Members: []server.Member{
			{Person: "p_hr", Team: "hr_pay", Position: "staff", Primary: true},
			{Person: "p_it", Team: "it", Position: "staff", Primary: true},
			{Person: "p_corp", Team: "corp", Position: "division_head", Primary: true},
		},
	}
}

// ask asks the examples' gate a question of a version of a kind owned by owner ("" for none).
func ask(t *testing.T, action, person string, roles []string, owner string) bool {
	t.Helper()
	return askIn(t, &Gate{}, gateOrg(), action, person, roles, owner)
}

// askIn asks a gate a question in a version of the organization.
func askIn(t *testing.T, g *Gate, org *server.OrgVersion, action, person string, roles []string, owner string) bool {
	t.Helper()
	q := server.Question{Action: action, Person: server.Person{ID: person, Roles: roles}, Kind: "leave", Version: 1, Owner: owner,
		Ticket: "T1", Requester: person, Requesters: []string{person}, Now: time.Date(2026, 11, 16, 10, 0, 0, 0, Zone), Org: org}
	if action == "import_organization" || action == "read_organization_audit" {
		q.Kind, q.Version, q.Owner, q.Ticket, q.Requester, q.Requesters = "", 0, "", "", "", nil
	}
	ok, err := g.Allow(context.Background(), q)
	if err != nil {
		t.Fatalf("%s as %s %v of %q: %v", action, person, roles, owner, err)
	}
	return ok
}

// A person the organization does not have is no one's department, and a kind with no owner is no
// department's: neither sees the tickets of such a kind, nor switches it.
func TestGateNoOneOfNoDepartment(t *testing.T) {
	for _, c := range []struct {
		action, person string
		roles          []string
		owner          string
	}{
		{"see", "outsider", nil, ""},
		{"see", "outsider", nil, "hr"},
		{"see", "p_hr", nil, ""},
		{"change_request_type", "outsider", []string{"workflow_admin"}, ""},
		{"change_request_type", "outsider", []string{"workflow_admin"}, "hr"},
		{"change_request_type", "p_hr", []string{"workflow_admin"}, ""},
	} {
		if ask(t, c.action, c.person, c.roles, c.owner) {
			t.Errorf("%s as %s %v of a kind owned by %q: allowed", c.action, c.person, c.roles, c.owner)
		}
	}
}

// A role the gate does not declare is left out: the person does what their other roles let them.
func TestGateUndeclaredRoles(t *testing.T) {
	if !ask(t, "submit", "p_hr", []string{"staff", "printer_admin"}, "hr") {
		t.Error("submit as one with a role the gate does not declare: refused")
	}
	if !ask(t, "submit", "p_hr", []string{"printer_admin"}, "hr") {
		t.Error("submit as one with no role the gate declares: refused")
	}
	if !ask(t, "change_request_type", "p_hr", []string{"workflow_admin", "printer_admin"}, "hr") {
		t.Error("change_request_type of their own department's kind, with a role the gate does not declare: refused")
	}
	if ask(t, "change_request_type", "p_it", []string{"workflow_admin", "printer_admin"}, "hr") {
		t.Error("change_request_type of another department's kind: allowed")
	}
}

// The department is found at the kind of team that owns the kind: a kind a division owns is
// administered by an administrator of that division, from any team under it.
func TestGateDepartmentOfTheOwnersKind(t *testing.T) {
	if !ask(t, "change_request_type", "p_hr", []string{"workflow_admin"}, "corp") {
		t.Error("change_request_type of a kind the division owns, by an administrator in a section under it: refused")
	}
	if !ask(t, "change_request_type", "p_corp", []string{"workflow_admin"}, "corp") {
		t.Error("change_request_type of a kind the division owns, by an administrator in the division: refused")
	}
	if ask(t, "change_request_type", "p_corp", []string{"workflow_admin"}, "hr") {
		t.Error("change_request_type of a kind a department owns, by an administrator of the division above it: allowed")
	}
}

// A role the gate does not declare is said in the log once, for the first hundred such roles.
func TestGateSaysUndeclaredRolesOnce(t *testing.T) {
	var log bytes.Buffer
	g := &Gate{Log: slog.New(slog.NewTextHandler(&log, nil))}
	for range 3 {
		askIn(t, g, gateOrg(), "submit", "p_hr", []string{"staff", "printer_admin"}, "hr")
	}
	if n := strings.Count(log.String(), "role=printer_admin"); n != 1 {
		t.Errorf("printer_admin said %d times; want once:\n%s", n, log.String())
	}
	for i := range 150 {
		askIn(t, g, gateOrg(), "submit", "p_hr", []string{fmt.Sprintf("role_%03d", i)}, "hr")
	}
	if n := strings.Count(log.String(), "\n"); n != 100 {
		t.Errorf("%d lines for 151 roles the gate does not declare; want 100 (the first hundred)", n)
	}
}

// The administration's roles (project/shared/company.gate): an administrator of everything
// switches every kind (one with no owner among them) and imports the organization's data; an auditor
// reads the whole record and changes nothing; an administrator of a department switches and reads
// only their department's kinds, and imports nothing.
func TestGateAdministration(t *testing.T) {
	for _, c := range []struct {
		action, person string
		roles          []string
		owner          string
		want           bool
	}{
		{"change_request_type", "p_it", []string{"system_admin"}, "hr", true},
		{"change_request_type", "p_it", []string{"system_admin"}, "", true},
		{"read_audit", "p_it", []string{"system_admin"}, "hr", true},
		{"import_organization", "p_it", []string{"system_admin"}, "", true},
		{"read_organization_audit", "p_it", []string{"system_admin"}, "", true},
		{"read_audit", "p_it", []string{"auditor"}, "hr", true},
		{"read_audit", "p_it", []string{"auditor"}, "", true},
		{"read_organization_audit", "p_it", []string{"auditor"}, "", true},
		{"change_request_type", "p_it", []string{"auditor"}, "it", false},
		{"import_organization", "p_it", []string{"auditor"}, "", false},
		{"add_approver", "p_it", []string{"auditor"}, "it", false},
		{"hand_over", "p_it", []string{"auditor"}, "it", false},
		{"read_audit", "p_hr", []string{"workflow_admin"}, "hr", true},
		{"read_audit", "p_hr", []string{"workflow_admin"}, "it", false},
		{"read_audit", "p_hr", []string{"workflow_admin"}, "", false},
		{"import_organization", "p_hr", []string{"workflow_admin"}, "", false},
		{"read_organization_audit", "p_hr", []string{"workflow_admin"}, "", false},
		{"import_organization", "p_hr", []string{"staff"}, "", false},
		{"read_audit", "p_hr", nil, "hr", false},
	} {
		if got := ask(t, c.action, c.person, c.roles, c.owner); got != c.want {
			t.Errorf("%s as %s %v of a kind owned by %q: %v; want %v", c.action, c.person, c.roles, c.owner, got, c.want)
		}
	}
}

// A kind with no owner is given an ID no team of the version has: a team whose ID is "(no owner)"
// does not make its administrators the owners of such a kind.
func TestGateNoOwnerIsNoTeam(t *testing.T) {
	org := gateOrg()
	org.Teams = append(org.Teams, server.Team{ID: "(no owner)", Name: "Odd", Kind: "department", Parent: "corp"})
	org.People = append(org.People, server.OrgPerson{ID: "p_odd", Name: "Odd staff"})
	org.Members = append(org.Members, server.Member{Person: "p_odd", Team: "(no owner)", Position: "staff", Primary: true})
	if askIn(t, &Gate{}, org, "change_request_type", "p_odd", []string{"workflow_admin"}, "") {
		t.Error("change_request_type of a kind with no owner, by an administrator of the team (no owner): allowed")
	}
	if askIn(t, &Gate{}, org, "see", "p_odd", nil, "") {
		t.Error("see a ticket of a kind with no owner, by one of the team (no owner): allowed")
	}
	if !askIn(t, &Gate{}, org, "change_request_type", "p_odd", []string{"system_admin"}, "") {
		t.Error("change_request_type of a kind with no owner, by an administrator of everything: refused")
	}
}
