package oidc_test

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/oidc"
	"github.com/i2y/ringi-server/web"
)

func claims(email string, verified any, more ...string) oidc.Claims {
	all := map[string]json.RawMessage{"sub": json.RawMessage(`"u-1"`)}
	c := oidc.Claims{Issuer: "https://id.example.test", Subject: "u-1", Email: email, All: all}
	if email != "" {
		all["email"], _ = json.Marshal(email)
	}
	if verified != nil {
		all["email_verified"], _ = json.Marshal(verified)
		c.EmailVerified = verified == true
	}
	for i := 0; i+1 < len(more); i += 2 {
		all[more[i]] = json.RawMessage(more[i+1])
	}
	return c
}

// ByEmail finds the one person whose address is the token's verified email.
func TestByEmail(t *testing.T) {
	m := oidc.ByEmail(func(context.Context) ([]server.OrgPerson, error) { return people, nil })
	for _, c := range []struct {
		name   string
		claims oidc.Claims
		person string
		refuse *oidc.Refusal
	}{
		{"the address", claims("alice@example.test", true), "e1201", nil},
		{"in other case, with spaces", claims(" ALICE@Example.TEST ", true), "e1201", nil},
		{"not verified", claims("alice@example.test", false), "", oidc.ErrNoVerifiedEmail},
		{"verified as a text", claims("alice@example.test", "true"), "", oidc.ErrNoVerifiedEmail},
		{"verified, not said", claims("alice@example.test", nil), "", oidc.ErrNoVerifiedEmail},
		{"no email", claims("", true), "", oidc.ErrNoVerifiedEmail},
		{"an address no one has", claims("carol@example.test", true), "", oidc.ErrNotInOrganization},
		{"an address two people have", claims("dana@example.test", true), "", oidc.ErrAmbiguousEmail},
	} {
		p, err := m(context.Background(), c.claims)
		var no *oidc.Refusal
		switch {
		case c.refuse == nil && (err != nil || p.ID != c.person):
			t.Errorf("%s: %v %v", c.name, p, err)
		case c.refuse != nil && (!errors.As(err, &no) || no != c.refuse):
			t.Errorf("%s: %v %v, not %v", c.name, p, err, c.refuse)
		}
	}
	// The same person twice in the data is one person.
	twice := oidc.ByEmail(func(context.Context) ([]server.OrgPerson, error) {
		return []server.OrgPerson{{ID: "e1", Email: "a@example.test"}, {ID: "e1", Email: "A@example.test"}}, nil
	})
	if p, err := twice(context.Background(), claims("a@example.test", true)); err != nil || p.ID != "e1" {
		t.Errorf("the same person twice: %v %v", p, err)
	}
	// A failure to read the people is a failure, not a refusal.
	failing := oidc.ByEmail(func(context.Context) ([]server.OrgPerson, error) { return nil, errors.New("down") })
	var no *oidc.Refusal
	if _, err := failing(context.Background(), claims("alice@example.test", true)); err == nil || errors.As(err, &no) {
		t.Errorf("the people failing: %v", err)
	}
}

// Roles adds the roles a claim lists, once each.
func TestRoles(t *testing.T) {
	m := oidc.Roles(func(context.Context, oidc.Claims) (server.Person, error) {
		return server.Person{ID: "e1", Roles: []string{"staff"}}, nil
	}, "groups")
	for raw, want := range map[string][]string{
		`["approvers","staff","approvers",""]`: {"staff", "approvers"},
		`"auditors"`:                           {"staff", "auditors"},
		`[1,2]`:                                {"staff"},
		`{"a":"b"}`:                            {"staff"},
	} {
		p, err := m(context.Background(), claims("a@example.test", true, "groups", raw))
		if err != nil || !slices.Equal(p.Roles, want) {
			t.Errorf("groups %s: %v %v, not %v", raw, p.Roles, err, want)
		}
	}
	if p, _ := m(context.Background(), claims("a@example.test", true)); !slices.Equal(p.Roles, []string{"staff"}) {
		t.Errorf("no groups: %v", p.Roles)
	}
	refusing := oidc.Roles(oidc.ByEmail(func(context.Context) ([]server.OrgPerson, error) { return nil, nil }), "groups")
	if _, err := refusing(context.Background(), claims("a@example.test", true, "groups", `["x"]`)); err != oidc.ErrNotInOrganization {
		t.Errorf("Roles of a refusal: %v", err)
	}
}

// A refusal is a page of 403 with its words, in the person's language (the one they chose on the
// pages, else their browser's), and the address they signed in with; nothing of the person's is
// written into the page unescaped. The page is drawn in the person's light or dark colours, its
// words in its main part.
func TestRefusalPages(t *testing.T) {
	for _, c := range []struct {
		name, email string
		verified    bool
		lang        string
		chosen      string // the language chosen on the pages (the cookie web.LanguageCookie)
		has         []string
		reason      string
	}{
		{"not verified", "alice@example.test", false, "", "", []string{"has no confirmed email address", "You signed in to your organization's account as alice@example.test."}, "no_verified_email"},
		{"not in the organization", "carol@example.test", true, "", "", []string{"No one in the organization's data has your email address"}, "not_in_organization"},
		{"two people", "dana@example.test", true, "", "", []string{"More than one person in the organization's data has your email address"}, "ambiguous_email"},
		{"in Japanese", "carol@example.test", true, "ja", "", []string{"組織のデータに、あなたのメールアドレスの人がいません。", "組織のアカウントには carol@example.test でログインしました。", "もう一度ログインする"}, "not_in_organization"},
		{"in Japanese chosen on the pages", "carol@example.test", true, "en", "ja", []string{"組織のデータに、あなたのメールアドレスの人がいません。", "もう一度ログインする"}, "not_in_organization"},
		{"in English chosen on the pages", "carol@example.test", true, "ja", "en", []string{"No one in the organization's data has your email address", "Sign in again"}, "not_in_organization"},
		{"an address with markup", `"><script>alert(1)</script>@example.test`, true, "", "", []string{`as "><script>alert(1)</script>@example.test.`}, "not_in_organization"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newApp(t)
			a.p.set(func(p *provider) { p.person["email"], p.person["email_verified"] = c.email, c.verified })
			b := a.browser()
			if c.chosen != "" {
				u, err := url.Parse(a.srv.URL)
				if err != nil {
					t.Fatal(err)
				}
				b.jar.SetCookies(u, []*http.Cookie{{Name: web.LanguageCookie, Value: c.chosen, Path: "/"}})
			}
			cb := b.authorize(b.start("/pages/a"))
			x := b.open(cb, "Accept-Language", c.lang)
			if x.status != http.StatusForbidden || x.setCookie(oidc.SessionCookie) != "" {
				t.Fatalf("%d %q", x.status, x.header.Values("Set-Cookie"))
			}
			// The words as the page writes them: escaped.
			for _, want := range c.has {
				if !strings.Contains(x.body, html.EscapeString(want)) {
					t.Errorf("the page has no %s:\n%s", want, x.body)
				}
			}
			if lang := or(c.chosen, or(c.lang, "en")); !strings.Contains(x.body, `<html lang="`+lang+`">`) {
				t.Errorf("the page is not in %s:\n%s", lang, x.body)
			}
			if !strings.Contains(x.body, `<meta name="color-scheme" content="light dark">`) || strings.Count(x.body, "<main>") != 1 || !strings.Contains(x.body, "</main>") {
				t.Errorf("the page is not drawn in the person's colours, or has no main part:\n%s", x.body)
			}
			if strings.Contains(x.body, "<script>") {
				t.Errorf("the page has the address unescaped:\n%s", x.body)
			}
			if !strings.Contains(x.body, `<a href="/login?return_to=%2Fpages%2Fa">`) {
				t.Errorf("the page does not offer to sign in again, back to the page:\n%s", x.body)
			}
			if !strings.Contains(a.log.String(), `msg="oidc: a sign-in refused" reason=`+c.reason) {
				t.Errorf("the log:\n%s", a.log)
			}
			signedOut(t, b)
		})
	}
}

// A Map's own refusal shows its words (English where it has no Japanese); a Map's failure, or a
// person with no ID, is the server's failure.
func TestMapAnswers(t *testing.T) {
	for _, c := range []struct {
		name   string
		m      oidc.Map
		status int
		has    string
	}{
		{"its own refusal", func(context.Context, oidc.Claims) (server.Person, error) {
			return server.Person{}, &oidc.Refusal{Reason: "contractor", Words: map[string]string{"en": "Contractors use the other server."}}
		}, http.StatusForbidden, "<p>Contractors use the other server.</p>"},
		{"a failure", func(context.Context, oidc.Claims) (server.Person, error) {
			return server.Person{}, errors.New("the directory is down")
		}, http.StatusInternalServerError, "サーバーでエラーが起きました。"},
		{"no ID", func(context.Context, oidc.Claims) (server.Person, error) { return server.Person{}, nil }, http.StatusInternalServerError, "サーバーでエラーが起きました。"},
	} {
		a := newApp(t, func(cfg *oidc.Config) { cfg.Map = c.m })
		b := a.browser()
		cb := b.authorize(b.start("/"))
		x := b.open(cb, "Accept-Language", "ja")
		if x.status != c.status || !strings.Contains(x.body, c.has) || strings.Contains(x.body, "the directory is down") {
			t.Errorf("%s: %d %s", c.name, x.status, x.body)
		}
		signedOut(t, b)
	}
}
