package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// discovery is what is read of the provider's discovery document.
type discovery struct {
	Issuer           string   `json:"issuer"`
	Authorization    string   `json:"authorization_endpoint"`
	Token            string   `json:"token_endpoint"`
	JWKSURI          string   `json:"jwks_uri"`
	EndSession       string   `json:"end_session_endpoint"`
	ResponseTypes    []string `json:"response_types_supported"`
	SigningAlgs      []string `json:"id_token_signing_alg_values_supported"`
	ChallengeMethods []string `json:"code_challenge_methods_supported"`
	AuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
	// IssParameter is RFC 9207's: the provider puts iss in its authorization responses.
	IssParameter bool `json:"authorization_response_iss_parameter_supported"`
}

// discover reads the discovery document and checks it (DESIGN.md, 4.1).
func (s *SignIn) discover(ctx context.Context) error {
	where := strings.TrimSuffix(s.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	var d discovery
	if err := getJSON(ctx, s.client, where, &d); err != nil {
		return err
	}
	if d.Issuer != s.cfg.Issuer {
		return fmt.Errorf("its issuer is %q, not %q: the issuer must be the same, character for character", d.Issuer, s.cfg.Issuer)
	}
	for _, e := range []struct{ name, value string }{
		{"authorization_endpoint", d.Authorization}, {"token_endpoint", d.Token}, {"jwks_uri", d.JWKSURI},
	} {
		if u, err := url.Parse(e.value); err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("%s %q: an https URL is required", e.name, e.value)
		}
	}
	if !slices.Contains(d.ResponseTypes, "code") {
		return errors.New("response_types_supported has no code: the module uses the authorization code flow")
	}
	if !slices.ContainsFunc(d.SigningAlgs, func(a string) bool { return s.algs[a] }) {
		return fmt.Errorf("id_token_signing_alg_values_supported %v has none of the allowed algorithms", d.SigningAlgs)
	}
	if d.ChallengeMethods != nil && !slices.Contains(d.ChallengeMethods, "S256") {
		return fmt.Errorf("code_challenge_methods_supported %v has no S256", d.ChallengeMethods)
	}
	if d.AuthMethods != nil && !slices.Contains(d.AuthMethods, s.cfg.ClientAuth) {
		return fmt.Errorf("token_endpoint_auth_methods_supported %v has no %s", d.AuthMethods, s.cfg.ClientAuth)
	}
	if s.cfg.ProviderLogout {
		if u, err := url.Parse(d.EndSession); err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("ProviderLogout: the provider has no end_session_endpoint (an https URL)")
		}
	}
	s.disc = d
	return nil
}

// getJSON reads a JSON document of the provider's.
func getJSON(ctx context.Context, client *http.Client, where string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, where, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := readAnswer(resp)
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", where, resp.Status)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: not JSON of the expected shape: %w", where, err)
	}
	return nil
}

// readAnswer reads an answer of the provider's, 1 MiB at the most.
func readAnswer(resp *http.Response) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxAnswer {
		return nil, errors.New("the answer is larger than 1 MiB")
	}
	return b, nil
}

// errGrant is the token endpoint's invalid_grant: the code was used before, has expired, or is not
// the verifier's.
var errGrant = errors.New("the token endpoint answered invalid_grant")

// tokens is what is read of the token endpoint's answer.
type tokens struct {
	TokenType string `json:"token_type"`
	IDToken   string `json:"id_token"`
	Error     string `json:"error"`
}

// exchange gives the code and the verifier for the tokens (DESIGN.md, section 3), and answers the
// ID token.
func (s *SignIn) exchange(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {s.cfg.RedirectURL},
		"code_verifier": {verifier},
	}
	if s.cfg.ClientAuth == "client_secret_post" {
		form.Set("client_id", s.cfg.ClientID)
		form.Set("client_secret", s.cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.disc.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if s.cfg.ClientAuth == "client_secret_basic" {
		// RFC 6749 2.3.1: the ID and the secret are form-encoded before they are put in the header.
		req.SetBasicAuth(url.QueryEscape(s.cfg.ClientID), url.QueryEscape(s.cfg.ClientSecret))
	}
	resp, err := s.tokenClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("the token endpoint: %w", err)
	}
	defer resp.Body.Close()
	b, err := readAnswer(resp)
	if err != nil {
		return "", fmt.Errorf("the token endpoint: %w", err)
	}
	var t tokens
	jsonErr := json.Unmarshal(b, &t)
	if resp.StatusCode != http.StatusOK {
		if jsonErr == nil && t.Error == "invalid_grant" {
			return "", errGrant
		}
		if jsonErr == nil && t.Error != "" {
			return "", fmt.Errorf("the token endpoint answered %s, error %s", resp.Status, errorCode(t.Error))
		}
		return "", fmt.Errorf("the token endpoint answered %s", resp.Status)
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "application/json" {
		return "", fmt.Errorf("the token endpoint answered %q, not application/json", mt)
	}
	if jsonErr != nil {
		return "", errors.New("the token endpoint's answer is not JSON of the expected shape")
	}
	if !strings.EqualFold(t.TokenType, "Bearer") {
		return "", fmt.Errorf("the token endpoint's token_type is %q, not Bearer", t.TokenType)
	}
	if t.IDToken == "" {
		return "", errors.New("the token endpoint's answer has no id_token")
	}
	return t.IDToken, nil
}

// errorCode answers an error code of the provider's as it may be logged: letters, digits and _, 64
// at the most; anything else is "other".
func errorCode(e string) string {
	if e == "" || len(e) > 64 {
		return "other"
	}
	for _, r := range e {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return "other"
		}
	}
	return e
}
