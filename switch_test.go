package server_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
)

// The number a switch names (DESIGN 13.2): read as JSON writes a number, and always named; and the
// starts the process that switched takes.

// A switch's number is a whole number from 0 however JSON writes it, as the ticket operations read
// a revision; anything else is 400, in words that name no type of Go.
func TestSwitchNumberAsWritten(t *testing.T) {
	for _, c := range []struct {
		number string
		code   int
	}{
		{`0`, 200}, {`-0`, 200}, {`0.0`, 200}, {`0e0`, 200}, {`0E+2`, 200},
		{`9223372036854775807`, 409}, {`1`, 409},
		{`-1`, 400}, {`0.5`, 400}, {`"0"`, 400}, {`true`, 400}, {`[]`, 400}, {`{}`, 400},
		{`9223372036854775808`, 400}, {`1e400`, 400}, {`null`, 400}, {``, 400},
	} {
		a := newAdmins(t, namedStore{"memory", memstore.New()})
		body := `{"start":"never"`
		if c.number != "" {
			body += `,"number":` + c.number
		}
		code, b, _ := send(a.srv, "POST", "/admin/kinds/trip/versions/2", "boss", "system_admin", a.key(), "application/json", body+"}")
		if code != c.code {
			t.Errorf("number %s: %d %s; want %d", c.number, code, b, c.code)
		}
		if strings.Contains(string(b), "Go struct") || strings.Contains(string(b), "int64") {
			t.Errorf("number %s: the answer names a type of Go: %s", c.number, b)
		}
		if c.code == 400 && !strings.Contains(string(b), "number: ") {
			t.Errorf("number %s: the answer does not say what is wrong: %s", c.number, b)
		}
	}
}

// A switch that names no number (none, or null) does not undo a switch made after its person read
// the start: it is refused (400), and the other's switch stays.
func TestSwitchNamesItsNumber(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_switch_number") {
		a := newAdmins(t, st)
		for _, number := range []string{``, `,"number":null`} {
			if code, b := a.switchTo("bob", "system_admin", 2, map[string]any{"start": "2026-12-15", "number": a.number(2), "reason": "from the 15th"}); code != 200 {
				t.Fatalf("%s: bob's switch: %d %s", st.name, code, b)
			}
			code, b, _ := send(a.srv, "POST", "/admin/kinds/trip/versions/2", "ann", "system_admin", a.key(), "application/json", `{"start":"never"`+number+`}`)
			if code != 400 || !strings.Contains(string(b), "number: ") {
				t.Errorf("%s: a switch with no number%s: %d %s", st.name, number, code, b)
			}
			sts, err := a.store.Starts(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range sts {
				if s.Kind == "trip" && s.Version == 2 && (s.How != "day" || s.Day != "2026-12-15") {
					t.Errorf("%s: a switch with no number%s undid bob's: %+v", st.name, number, s)
				}
			}
			// back to the description, for the next round on the same store
			if code, b := a.switchTo("bob", "system_admin", 2, map[string]any{"start": "description"}); code != 200 {
				t.Fatalf("%s: %d %s", st.name, code, b)
			}
		}
	}
}

// heldStarts is a store whose next reading of the starts, once armed, reads them and then waits to
// be let go before it answers: a reading that began before a switch and ends after it.
type heldStarts struct {
	server.Store
	armed   atomic.Bool
	read    chan struct{}
	release chan struct{}
}

func (h *heldStarts) Starts(ctx context.Context) ([]server.Start, error) {
	if h.armed.CompareAndSwap(true, false) {
		sts, err := h.Store.Starts(ctx)
		close(h.read)
		<-h.release
		return sts, err
	}
	return h.Store.Starts(ctx)
}

// The process that switched takes its switch at once (DESIGN 13.2): another request of it that
// began to read the starts before the switch was kept, and ends after the switch read them again,
// does not put the starts before the switch back.
func TestSwitchingProcessKeepsItsSwitch(t *testing.T) {
	ctx := context.Background()
	st := &heldStarts{Store: memstore.New(), read: make(chan struct{}), release: make(chan struct{})}
	a := newAdmins(t, namedStore{"memory", st})
	if _, err := a.srv.Do(ctx, server.Call{Operation: "list_requests", Person: server.Person{ID: "ann"}}); err != nil {
		t.Fatal(err)
	}
	a.clock.Add(30 * time.Second)
	st.armed.Store(true)
	done := make(chan struct{})
	go func() {
		a.srv.Do(ctx, server.Call{Operation: "list_requests", Person: server.Person{ID: "bob"}})
		close(done)
	}()
	<-st.read
	if code, b := a.switchTo("boss", "system_admin", 2, map[string]any{"start": "now"}); code != 200 {
		t.Fatalf("2 now: %d %s", code, b)
	}
	right := a.current()
	close(st.release)
	<-done
	if after := a.current(); right != 2 || after != 2 {
		t.Errorf("the process that switched 2 to now takes %d just after the switch, and %d after a reading begun before it ends", right, after)
	}
}
