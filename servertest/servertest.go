// Package servertest has the parts of a server for tests and examples: a clock you set, an
// authenticator that trusts a header, and a notifier, flows, agents, books, files and an audit log
// that remember what they were given; and, for the tests of a project's pages, a browser with no
// script (Browser) and the form of a page as a browser holds it (Form). None of them is for
// production.
package servertest

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
)

// Clock is a clock that tells the time it is set to.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

// NewClock makes a clock set to t.
func NewClock(t time.Time) *Clock { return &Clock{t: t} }

// Now tells the time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Set sets the time.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// Add moves the time on.
func (c *Clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// Header is an authenticator that trusts a header to say who sends the request: X-Person (the
// person's ID) and X-Roles (their roles, apart with commas; "staff" when it is not there). For
// tests and examples only: anyone who can send a request can say they are anyone.
type Header struct{}

// Authenticate reads the headers.
func (Header) Authenticate(r *http.Request) (server.Person, error) {
	id := strings.TrimSpace(r.Header.Get("X-Person"))
	if id == "" {
		return server.Person{}, server.ErrNoPerson
	}
	roles := []string{"staff"}
	if h := r.Header.Get("X-Roles"); h != "" {
		roles = nil
		for _, x := range strings.Split(h, ",") {
			if x = strings.TrimSpace(x); x != "" {
				roles = append(roles, x)
			}
		}
	}
	return server.Person{ID: id, Roles: roles}, nil
}

// Notifier remembers the notices it is given. Fail, when set, makes Notify fail for a notice.
type Notifier struct {
	mu      sync.Mutex
	notices []server.Notice
	calls   int
	Fail    func(server.Notice) error
}

// Notify remembers a notice; a key given again is remembered once.
func (n *Notifier) Notify(_ context.Context, x server.Notice) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	if n.Fail != nil {
		if err := n.Fail(x); err != nil {
			return err
		}
	}
	for _, have := range n.notices {
		if have.Key == x.Key {
			return nil
		}
	}
	n.notices = append(n.notices, x)
	return nil
}

// Calls answers how many times Notify was called, a key given again included.
func (n *Notifier) Calls() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

// Notices answers the notices given, the first first.
func (n *Notifier) Notices() []server.Notice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.notices)
}

// Flows remembers the flows launched.
type Flows struct {
	mu       sync.Mutex
	launches []server.Launch
}

// Launch remembers a launch; a key given again is remembered once.
func (f *Flows) Launch(_ context.Context, l server.Launch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, have := range f.launches {
		if have.Key == l.Key {
			return nil
		}
	}
	f.launches = append(f.launches, l)
	return nil
}

// Launches answers the flows launched.
func (f *Flows) Launches() []server.Launch {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.launches)
}

// Agents remembers what was asked. Answer, when set, is called for each new ask after it is
// remembered (in its own goroutine), to answer it as an agent would.
type Agents struct {
	mu     sync.Mutex
	asks   []server.Ask
	Answer func(server.Ask)
}

// Ask remembers an ask; a key given again is remembered once.
func (a *Agents) Ask(_ context.Context, x server.Ask) error {
	a.mu.Lock()
	for _, have := range a.asks {
		if have.Key == x.Key {
			a.mu.Unlock()
			return nil
		}
	}
	a.asks = append(a.asks, x)
	answer := a.Answer
	a.mu.Unlock()
	if answer != nil {
		go answer(x)
	}
	return nil
}

// Asks answers what was asked.
func (a *Agents) Asks() []server.Ask {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.asks)
}

// Books are books in memory that answer done to every call, and done_before to a call they made
// before: the same call (hold, post or release) of the same effect with the same arguments, as a
// book that keys its holds by the ticket and the try answers a call given again. Refuse, when set,
// refuses a call with its reason; Fail, when set, answers a call with an error, as books that do not
// answer do, and the call is not made. With a memstore, a call counts only once its move is saved.
type Books struct {
	mu     sync.Mutex
	calls  []server.BookCall
	Refuse func(server.BookCall) string
	Fail   func(server.BookCall) error
}

// Call makes a call.
func (b *Books) Call(_ context.Context, tx any, c server.BookCall) (server.BookAnswer, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Fail != nil {
		if err := b.Fail(c); err != nil {
			return server.BookAnswer{}, err
		}
	}
	if b.Refuse != nil {
		if why := b.Refuse(c); why != "" {
			return server.BookAnswer{Outcome: "refused", Reason: why}, nil
		}
	}
	for _, have := range b.calls {
		if have.Key == c.Key && have.Call == c.Call && have.Book == c.Book && have.Transfer == c.Transfer && reflect.DeepEqual(have.Args, c.Args) {
			return server.BookAnswer{Outcome: "done_before"}, nil
		}
	}
	keep := func() {
		b.mu.Lock()
		b.calls = append(b.calls, c)
		b.mu.Unlock()
	}
	if t, ok := tx.(interface{ AfterCommit(func()) }); ok {
		t.AfterCommit(keep)
	} else {
		b.calls = append(b.calls, c)
	}
	return server.BookAnswer{Outcome: "done"}, nil
}

// Calls answers the calls made.
func (b *Books) Calls() []server.BookCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.calls)
}

// Files keeps files in memory: it is memstore.Files, which a server that keeps its tickets in memory
// keeps its attachments in. With a Base, URL answers Base/<id>; without one, "".
type Files = memstore.Files

// AuditLog remembers its entries.
type AuditLog struct {
	mu      sync.Mutex
	entries []server.AuditEntry
}

// Record remembers an entry.
func (a *AuditLog) Record(_ context.Context, e server.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

// Entries answers the entries.
func (a *AuditLog) Entries() []server.AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.entries)
}

// ErrFailed is the failure of the parts that are told to fail.
var ErrFailed = errors.New("failed, as the test said")
