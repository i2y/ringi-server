package devlogin_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/devlogin"
	"github.com/i2y/ringi-server/servertest"
)

// TestSignInRolePresets: the page of signing in lists the roles one may try the server with as
// links that ask for them (?roles=), each keeping the page's return_to and words of the find,
// so that a click writes the role without typing it: the link of the roles the page was asked
// with is the active one, and the words are in the input. The roles asked for go before the
// cookie's, and finding a person keeps them.
func TestSignInRolePresets(t *testing.T) {
	_, mux := signIn(t, org{})
	b := &servertest.Browser{Handler: mux}
	page := b.Get(devlogin.LoginPath + "?return_to=%2Fpages%2Fadmin%2F&roles=workflow_admin&q=e12").Body.String()
	wellFormed(t, "the page of signing in with roles", []byte(page))
	for _, s := range []string{
		`id="dev-roles" name="roles" value="workflow_admin"`,
		`href="/login?q=e12&amp;return_to=%2Fpages%2Fadmin%2F&amp;roles="`,
		`href="/login?q=e12&amp;return_to=%2Fpages%2Fadmin%2F&amp;roles=workflow_admin" class="active" aria-current="true"`,
		`href="/login?q=e12&amp;return_to=%2Fpages%2Fadmin%2F&amp;roles=system_admin"`,
		`href="/login?q=e12&amp;return_to=%2Fpages%2Fadmin%2F&amp;roles=auditor"`,
		"An administrator of their own department (workflow_admin)",
	} {
		if !strings.Contains(page, s) {
			t.Errorf("the page with roles=workflow_admin has no %s:\n%s", s, page)
		}
	}
	// no roles asked for, none in the cookie: the one of the staff alone is the active one
	page = b.Get(devlogin.LoginPath).Body.String()
	for _, s := range []string{`href="/login?return_to=%2Fpages%2F&amp;roles=" class="active" aria-current="true"`, `id="dev-roles" name="roles" value=""`} {
		if !strings.Contains(page, s) {
			t.Errorf("the page with no roles has no %s:\n%s", s, page)
		}
	}
	// the roles asked for go before the cookie's, and the cookie's alone are shown
	b.Header = http.Header{"Cookie": {devlogin.RolesCookie + "=" + url.QueryEscape("system_admin")}}
	if page = b.Get(devlogin.LoginPath + "?roles=auditor").Body.String(); !strings.Contains(page, `id="dev-roles" name="roles" value="auditor"`) {
		t.Errorf("the roles asked for do not go before the cookie's:\n%s", page)
	}
	if page = b.Get(devlogin.LoginPath).Body.String(); !strings.Contains(page, `id="dev-roles" name="roles" value="system_admin"`) ||
		!strings.Contains(page, `href="/login?return_to=%2Fpages%2F&amp;roles=system_admin" class="active" aria-current="true"`) {
		t.Errorf("the cookie's roles are not shown:\n%s", page)
	}
	// in an organization of more people than a picker lists, finding one keeps the roles asked for,
	// and a page asked for none sends none with the find
	_, large := signIn(t, org{grown: 1200})
	lb := &servertest.Browser{Handler: large}
	find := `<input type="hidden" name="roles" value="auditor" />`
	if page = lb.Get(devlogin.LoginPath + "?roles=auditor&q=c0042").Body.String(); !strings.Contains(page, find) || !strings.Contains(page, `id="dev-roles" name="roles" value="auditor"`) ||
		!strings.Contains(page, `href="/login?q=c0042&amp;return_to=%2Fpages%2F&amp;roles=auditor" class="active" aria-current="true"`) {
		t.Errorf("finding a person does not keep the roles asked for:\n%s", page)
	}
	wellFormed(t, "the page of signing in, finding a person with roles", []byte(page))
	if page = lb.Get(devlogin.LoginPath + "?q=c0042").Body.String(); strings.Contains(page, `type="hidden" name="roles"`) {
		t.Errorf("a find sends roles no one asked for:\n%s", page)
	}
	// the words are in the page's language
	b.Header = http.Header{"Accept-Language": {"ja"}}
	if page = b.Get(devlogin.LoginPath + "?roles=auditor").Body.String(); !strings.Contains(page, "どの記録も読む人（auditor）") || !strings.Contains(page, `id="dev-roles" name="roles" value="auditor"`) {
		t.Errorf("the page in Japanese with roles=auditor:\n%s", page)
	}
}

// TestSignInPeopleFirst: the page of signing in lists the people first, and the roles after them,
// in a form of their own: the people's buttons sign in with the roles the page was asked with, which
// a line above them says, and roles that do not read are said at their field and are not sent.
func TestSignInPeopleFirst(t *testing.T) {
	_, mux := signIn(t, org{})
	b := &servertest.Browser{Handler: mux}
	page := b.Get(devlogin.LoginPath + "?roles=workflow_admin,auditor").Body.String()
	wellFormed(t, "the page of signing in with two roles", []byte(page))
	people, roles := strings.Index(page, `name="person"`), strings.Index(page, `id="dev-roles"`)
	if people < 0 || roles < 0 || people > roles {
		t.Errorf("the people are not listed before the roles (%d, %d):\n%s", people, roles, page)
	}
	for _, s := range []string{
		`<input type="hidden" name="roles" value="workflow_admin,auditor" />`,
		"You use the server with the roles workflow_admin, auditor.",
		`<details class="roles" open="open">`,
		`<form method="get" action="/login">`,
	} {
		if !strings.Contains(page, s) {
			t.Errorf("the page with two roles has no %s:\n%s", s, page)
		}
	}
	// pressing a person signs in with the roles the people's form sends
	w := b.Post(devlogin.LoginPath, url.Values{"person": {"e1201"}, "roles": {"workflow_admin,auditor"}})
	var signed string
	for _, c := range w.Result().Cookies() {
		if c.Name == devlogin.RolesCookie {
			signed = c.Value
		}
	}
	if signed != url.QueryEscape("workflow_admin,auditor") {
		t.Errorf("the roles signed in with: %d %q", w.Code, signed)
	}
	b.Header = nil
	// with none, the line says so, the roles are folded, and the people's form sends none
	b.Header = http.Header{"Accept-Language": {"ja"}}
	page = b.Get(devlogin.LoginPath + "?roles=").Body.String()
	if !strings.Contains(page, "役割の無いスタッフとして使います。") || strings.Contains(page, `type="hidden" name="roles"`) || !strings.Contains(page, `<details class="roles">`) {
		t.Errorf("the page with no roles:\n%s", page)
	}
	// roles that do not read: said at the field, and not sent with a person
	page = b.Get(devlogin.LoginPath + "?roles=" + url.QueryEscape("a b")).Body.String()
	if !strings.Contains(page, `id="dev-roles-error"`) || strings.Contains(page, `type="hidden" name="roles"`) || !strings.Contains(page, "役割の無いスタッフとして使います。") {
		t.Errorf("the page with roles that do not read:\n%s", page)
	}
}
