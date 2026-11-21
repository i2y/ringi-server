package oidc

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// returnTo answers where to come back to after signing in (DESIGN.md, 3.1): a path of this server,
// "/" for none or for the module's own routes; false for anything else.
func (s *SignIn) returnTo(raw string) (string, bool) {
	if raw == "" {
		return "/", true
	}
	if len(raw) > maxReturnTo || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsRune(raw, '\\') {
		return "", false
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f {
			return "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" || !strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	switch path.Clean(u.Path) {
	case s.loginPath, s.callback, s.logoutPath:
		return "/", true
	}
	return raw, true
}

// login starts signing in: it seals the sign-in in a cookie of its own and sends the browser to
// the provider.
func (s *SignIn) login(w http.ResponseWriter, r *http.Request) {
	to, ok := s.returnTo(r.URL.Query().Get("return_to"))
	if !ok {
		s.page(w, r, http.StatusBadRequest, sheet{text: []words{wordsBadReturnTo}, link: s.loginPath, linkText: wordsSignIn})
		return
	}
	p := pending{V: 1, State: random(), Nonce: random(), Verifier: random(), ReturnTo: to, At: s.clock.Now().Unix()}
	name := SignInCookie + p.State[:16]
	value, err := s.seal(name, p)
	if err != nil {
		s.failed(w, r, "starting a sign-in", err)
		return
	}
	http.SetCookie(w, cookie(name, value, signInTime))
	http.Redirect(w, r, s.authorization(&p), http.StatusSeeOther)
}

// authorization answers the address of the authorization request: the endpoint's own query kept,
// Config.Params, and the module's parameters.
func (s *SignIn) authorization(p *pending) string {
	u, _ := url.Parse(s.disc.Authorization)
	q := u.Query()
	for name, values := range s.cfg.Params {
		q[name] = values
	}
	challenge := sha256.Sum256([]byte(p.Verifier))
	q.Set("response_type", "code")
	q.Set("client_id", s.cfg.ClientID)
	q.Set("redirect_uri", s.cfg.RedirectURL)
	q.Set("scope", s.scope)
	q.Set("state", p.State)
	q.Set("nonce", p.Nonce)
	q.Set("code_challenge", b64.EncodeToString(challenge[:]))
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String()
}

// under answers the sign-in under way that state names, and deletes its cookie: nil when there is
// none, it does not open, it is older than ten minutes, or its state is another.
func (s *SignIn) under(w http.ResponseWriter, r *http.Request, state string) *pending {
	if len(state) != 43 {
		return nil
	}
	if _, err := decode64(state); err != nil {
		return nil
	}
	name := SignInCookie + state[:16]
	cookies := r.CookiesNamed(name)
	if len(cookies) == 0 {
		return nil
	}
	http.SetCookie(w, cookie(name, "", 0))
	for _, c := range cookies {
		var p pending
		if s.open(name, c.Value, &p) < 0 || p.V != 1 {
			continue
		}
		if !s.clock.Now().Before(time.Unix(p.At, 0).Add(signInTime)) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(p.State), []byte(state)) != 1 {
			continue
		}
		return &p
	}
	return nil
}

// finish is the callback: it checks the authorization response, gives the code for the tokens,
// checks the ID token, makes the person, and starts the session.
func (s *SignIn) finish(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := s.under(w, r, q.Get("state"))
	if p == nil {
		s.log.Warn("oidc: a sign-in failed", "step", "the callback", "why", "no sign-in under way in this browser has that state")
		s.page(w, r, http.StatusBadRequest, sheet{text: []words{wordsStartAgain}, link: s.loginPath, linkText: wordsSignInAgain})
		return
	}
	again := sheet{link: s.LoginURL(p.ReturnTo), linkText: wordsSignInAgain}
	if iss, ok := q["iss"]; ok || s.disc.IssParameter {
		if !ok || len(iss) != 1 || iss[0] != s.cfg.Issuer {
			s.log.Warn("oidc: a sign-in failed", "step", "the callback", "why", "its iss is not the issuer")
			s.page(w, r, http.StatusBadRequest, again.with(wordsStartAgain))
			return
		}
	}
	if e := q.Get("error"); e != "" {
		code := errorCode(e)
		s.log.Warn("oidc: a sign-in failed", "step", "the provider", "error", code)
		if code == "access_denied" {
			s.page(w, r, http.StatusForbidden, again.with(wordsDenied))
			return
		}
		s.page(w, r, http.StatusBadGateway, again.with(wordsProvider))
		return
	}
	code := q.Get("code")
	if code == "" {
		s.log.Warn("oidc: a sign-in failed", "step", "the callback", "why", "no code")
		s.page(w, r, http.StatusBadRequest, again.with(wordsStartAgain))
		return
	}
	token, err := s.exchange(r.Context(), code, p.Verifier)
	if errors.Is(err, errGrant) {
		s.log.Warn("oidc: a sign-in failed", "step", "the token request", "why", err.Error())
		s.page(w, r, http.StatusBadRequest, again.with(wordsStartAgain))
		return
	}
	if err != nil {
		s.log.Warn("oidc: a sign-in failed", "step", "the token request", "why", err.Error())
		s.page(w, r, http.StatusBadGateway, again.with(wordsProvider))
		return
	}
	c, err := s.check(r.Context(), token, p)
	if err != nil {
		s.log.Warn("oidc: a sign-in failed", "step", "the ID token", "why", err.Error())
		s.page(w, r, http.StatusBadGateway, again.with(wordsProvider))
		return
	}
	person, err := s.cfg.Map(r.Context(), c)
	var no *Refusal
	if errors.As(err, &no) {
		s.log.Warn("oidc: a sign-in refused", "reason", no.Reason, "sub", c.Subject, "email", c.Email)
		sh := again.with(no.words())
		if c.Email != "" {
			sh.text = append(sh.text, wordsSignedInAs.with(c.Email))
		}
		s.page(w, r, http.StatusForbidden, sh)
		return
	}
	if err == nil && person.ID == "" {
		err = errors.New("Map answered a person with no ID")
	}
	if err != nil {
		s.failed(w, r, "making the person", err)
		return
	}
	if old := s.opened(r); old != nil && s.cfg.Revocations != nil {
		if err := s.cfg.Revocations.Revoke(r.Context(), old.ID, time.Unix(old.At, 0).Add(s.limit)); err != nil {
			s.failed(w, r, "ending the session signing in replaces", err)
			return
		}
	}
	now := s.clock.Now().Unix()
	ses := &session{V: 1, ID: sessionID(), P: person.ID, R: person.Roles, S: c.Subject, At: now, Seen: now}
	if err := s.setSession(w, ses); err != nil {
		s.failed(w, r, "starting the session", err)
		return
	}
	s.log.Info("oidc: signed in", "person", person.ID, "sub", c.Subject)
	http.Redirect(w, r, p.ReturnTo, http.StatusSeeOther)
}

// logout ends the session: it records it as ended when there are revocations, deletes its cookie,
// and sends the browser on.
func (s *SignIn) logout(w http.ResponseWriter, r *http.Request) {
	if ses := s.opened(r); ses != nil {
		if s.cfg.Revocations != nil {
			if err := s.cfg.Revocations.Revoke(r.Context(), ses.ID, time.Unix(ses.At, 0).Add(s.limit)); err != nil {
				s.failed(w, r, "ending a session", err)
				return
			}
		}
		s.log.Info("oidc: signed out", "person", ses.P)
	}
	http.SetCookie(w, cookie(SessionCookie, "", 0))
	if !s.cfg.ProviderLogout {
		http.Redirect(w, r, s.afterLogout, http.StatusSeeOther)
		return
	}
	u, _ := url.Parse(s.disc.EndSession)
	q := u.Query()
	q.Set("client_id", s.cfg.ClientID)
	if s.cfg.PostLogoutRedirectURL != "" {
		q.Set("post_logout_redirect_uri", s.cfg.PostLogoutRedirectURL)
	}
	u.RawQuery = q.Encode()
	s.page(w, r, http.StatusOK, sheet{title: wordsSignOut, text: []words{wordsSignedOut}, link: u.String(), linkText: wordsProviderLogout, refresh: true})
}

// failed answers a failure of the server's, and logs it.
func (s *SignIn) failed(w http.ResponseWriter, r *http.Request, what string, err error) {
	s.log.Error("oidc: "+what, "err", err)
	s.page(w, r, http.StatusInternalServerError, sheet{text: []words{wordsFailed}})
}
