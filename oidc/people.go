package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	server "github.com/i2y/ringi-server"
)

// Claims are the claims of an ID token that passed every check.
type Claims struct {
	Issuer  string
	Subject string
	// Email is the token's email, and EmailVerified whether the token says the provider verified
	// it (the JSON boolean true).
	Email         string
	EmailVerified bool
	Name          string
	// All are every claim of the token, as it writes them.
	All map[string]json.RawMessage
}

func newClaims(iss, sub string, all map[string]json.RawMessage) Claims {
	c := Claims{Issuer: iss, Subject: sub, All: all}
	json.Unmarshal(all["email"], &c.Email)
	json.Unmarshal(all["name"], &c.Name)
	var verified bool
	if json.Unmarshal(all["email_verified"], &verified) == nil {
		c.EmailVerified = verified
	}
	return c
}

// Strings answers a claim that is a text or a list of texts (groups, roles); nil when the token has
// no such claim, or it is something else.
func (c Claims) Strings(name string) []string {
	raw, ok := c.All[name]
	if !ok {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
}

// Map makes the framework's person from the claims of an ID token that passed every check. A
// *Refusal keeps the person from signing in, with the words they are shown; any other error is a
// failure (500).
type Map func(ctx context.Context, c Claims) (server.Person, error)

// Refusal is what a Map answers to keep a person from signing in: the person is shown Words in
// their language (English when there are none of theirs), with 403, and Reason is logged.
type Refusal struct {
	Reason string
	// Words are the words shown, by language: "en", and "ja" when there are Japanese ones.
	Words map[string]string
}

func (r *Refusal) Error() string { return "oidc: the sign-in is refused: " + r.Reason }

func (r *Refusal) words() words {
	return words{en: r.Words["en"], ja: or(r.Words["ja"], r.Words["en"])}
}

// The refusals of ByEmail.
var (
	ErrNoVerifiedEmail = &Refusal{Reason: "no_verified_email", Words: map[string]string{
		"en": "Your organization's account has no confirmed email address, so this server cannot tell who you are. Ask the people who manage the accounts.",
		"ja": "組織のアカウントに確認済みのメールアドレスが無いため、このサーバーではあなたが誰かを確かめられません。アカウントの管理者に相談してください。",
	}}
	ErrNotInOrganization = &Refusal{Reason: "not_in_organization", Words: map[string]string{
		"en": "No one in the organization's data has your email address, so you cannot use this server yet. Ask the people who keep the organization's data to add you.",
		"ja": "組織のデータに、あなたのメールアドレスの人がいません。そのため、まだこのサーバーを使えません。組織のデータを管理している人に、登録を頼んでください。",
	}}
	ErrAmbiguousEmail = &Refusal{Reason: "ambiguous_email", Words: map[string]string{
		"en": "More than one person in the organization's data has your email address, so this server cannot tell which one you are. Ask the people who keep the organization's data to correct it.",
		"ja": "組織のデータに、あなたのメールアドレスの人が二人以上います。そのため、どの人かを決められません。組織のデータを管理している人に、直すよう頼んでください。",
	}}
)

// People answers the people of the version of the organization in effect today.
type People func(ctx context.Context) ([]server.OrgPerson, error)

// ByEmail answers a Map that finds the person whose address in the organization's data is the
// token's email, when the provider verified it. Addresses are compared without regard to case,
// after trimming spaces.
func ByEmail(people People) Map {
	return func(ctx context.Context, c Claims) (server.Person, error) {
		email := strings.TrimSpace(c.Email)
		if email == "" || !c.EmailVerified {
			return server.Person{}, ErrNoVerifiedEmail
		}
		all, err := people(ctx)
		if err != nil {
			return server.Person{}, fmt.Errorf("oidc: the people of the organization: %w", err)
		}
		var found []string
		for _, p := range all {
			if p.ID != "" && strings.EqualFold(strings.TrimSpace(p.Email), email) {
				found = append(found, p.ID)
			}
		}
		ids := slices.Compact(slices.Sorted(slices.Values(found)))
		switch len(ids) {
		case 0:
			return server.Person{}, ErrNotInOrganization
		case 1:
			return server.Person{ID: ids[0]}, nil
		}
		return server.Person{}, ErrAmbiguousEmail
	}
}

// Roles answers a Map that makes the person m makes, with the roles the claim lists (a text or a
// list of texts) added.
func Roles(m Map, claim string) Map {
	return func(ctx context.Context, c Claims) (server.Person, error) {
		p, err := m(ctx, c)
		if err != nil {
			return p, err
		}
		for _, role := range c.Strings(claim) {
			if role != "" && !slices.Contains(p.Roles, role) {
				p.Roles = append(p.Roles, role)
			}
		}
		return p, nil
	}
}

// PeopleOf answers the people of the version of the organization the server has in effect today,
// with their addresses (Server.People).
func PeopleOf(ctx context.Context, srv *server.Server) ([]server.OrgPerson, error) {
	if srv == nil {
		return nil, fmt.Errorf("oidc: PeopleOf: no server")
	}
	people, err := srv.People(ctx)
	if err != nil {
		return nil, fmt.Errorf("oidc: the people of the organization: %w", err)
	}
	return people, nil
}
