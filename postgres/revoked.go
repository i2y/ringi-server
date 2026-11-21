package postgres

import (
	"context"
	"time"
)

// Revoke records that a session of signing in was ended before its time (a sign-out): every
// process that reads the same tables refuses the session from then on. until is when the session
// would have ended anyway; the records whose time has passed are dropped as others are recorded.
// With Revoked, the store is what a way of signing in asks about ended sessions (the Revocations
// of package oidc).
func (s *Store) Revoke(ctx context.Context, session string, until time.Time) error {
	_, err := s.pool.Exec(ctx, s.sql["revoke"], session, until)
	return err
}

// Revoked reports whether a session was recorded as ended.
func (s *Store) Revoked(ctx context.Context, session string) (bool, error) {
	var ended bool
	err := s.pool.QueryRow(ctx, s.sql["revoked"], session).Scan(&ended)
	return ended, err
}
