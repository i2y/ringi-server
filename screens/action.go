package screens

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Action is a button's action, as a renderer sends it (A2UI 1.0's message from the renderer to the
// agent).
type Action struct {
	Name      string                     // the event's name: an operationId
	SurfaceID string                     // the surface of the screen it was pressed on
	Component string                     // sourceComponentId: the button's ID
	Timestamp time.Time                  // when it was pressed
	Context   map[string]json.RawMessage // what the button sends, as the renderer resolved it
}

// Operation is an operation of the API (openapi.json).
type Operation struct {
	ID     string          // the operationId: "approve", "submit_expense", "get_ticket"
	Method string          // "GET" or "POST"
	Path   string          // the path, with the ticket put in: "/tickets/T-1/approve"
	Ticket string          // the ticket of the path ({id}); "" when it has none
	Body   json.RawMessage // the body, for an operation that takes one; nil otherwise
	// Query is the query of an operation that takes one, as the button sends it (a list's next
	// page: `before`, the last ticket of the page shown, and list_mine's `kind`); nil when the
	// button sends none of it. A null is left out.
	Query url.Values
	Key   string // the operation's idempotency key, for a POST; "" for a GET (see Set.Operation)
}

// ParseAction reads the message a renderer sends when a button is pressed: {"version": "1.0",
// "action": {"name", "surfaceId", "sourceComponentId", "timestamp", "context", …}}. Any other
// message (a renderer's error, a function's answer) is ErrMessage.
func ParseAction(message []byte) (Action, error) {
	var top map[string]json.RawMessage
	if err := decode(message, &top); err != nil || top == nil {
		return Action{}, fmt.Errorf("%w: %v", ErrMessage, err)
	}
	var version string
	if err := json.Unmarshal(top["version"], &version); err != nil || version != "1.0" {
		return Action{}, fmt.Errorf("%w: its version is %s, not \"1.0\"", ErrMessage, top["version"])
	}
	raw, ok := top["action"]
	if !ok || len(top) != 2 {
		return Action{}, fmt.Errorf("%w: it holds %v, not a version and an action", ErrMessage, sortedKeys(top))
	}
	var a struct {
		Name              *string                    `json:"name"`
		SurfaceID         *string                    `json:"surfaceId"`
		SourceComponentID *string                    `json:"sourceComponentId"`
		Timestamp         *string                    `json:"timestamp"`
		Context           map[string]json.RawMessage `json:"context"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, fmt.Errorf("%w: its action: %v", ErrMessage, err)
	}
	if a.Name == nil || a.SurfaceID == nil || a.SourceComponentID == nil || a.Timestamp == nil || a.Context == nil {
		return Action{}, fmt.Errorf("%w: its action lacks one of name, surfaceId, sourceComponentId, timestamp and context", ErrMessage)
	}
	at, err := time.Parse(time.RFC3339Nano, *a.Timestamp)
	if err != nil {
		return Action{}, fmt.Errorf("%w: its timestamp %q is not a date and time of RFC 3339", ErrMessage, *a.Timestamp)
	}
	return Action{Name: *a.Name, SurfaceID: *a.SurfaceID, Component: *a.SourceComponentID, Timestamp: at, Context: a.Context}, nil
}

// Operation makes a button's action into the operation of the API it is (ringi's DESIGN 13.1):
// the event's name is the operationId, `ticket` the ticket of its path, and the rest of what the
// button sends its body, or the parameters of its query (a string, or a number as it is written)
// for an operation that takes them there.
//
// The surface names the screen (its first two parts are the kind and the screen; ErrSurface for
// another), the component its button (ErrButton for a part that is not a button sending this
// event, or a button that acts on the renderer alone). The context must hold exactly what the button
// sends, and a value the body requires that the API does not take null for (ErrContext).
//
// The key is made from the action, and is the same for the same message sent again. A new request
// (an operation that makes a ticket) has one key for each body on a surface: its button pressed
// twice with the same values is one request, which is why a server shows each new request's form
// under a Surface of its own. An operation on a ticket has a key for each press (the message's
// timestamp too): pressed twice, an operation that takes the revision is refused as stale the second
// time, and a comment is written twice. The person is not in the key; the server keeps keys for
// each person.
func (s *Set) Operation(a Action) (Operation, error) {
	parts := strings.SplitN(a.SurfaceID, ".", 3)
	if len(parts) < 2 {
		return Operation{}, fmt.Errorf("%w: %q", ErrSurface, a.SurfaceID)
	}
	k := s.kinds[parts[0]]
	if k == nil || k.screens[parts[1]] == nil {
		return Operation{}, fmt.Errorf("%w: %q is of no screen of the set", ErrSurface, a.SurfaceID)
	}
	sc := k.screens[parts[1]]
	b := sc.buttons[a.Component]
	switch {
	case b == nil:
		return Operation{}, fmt.Errorf("%w: the %s of %s has no button %q", ErrButton, sc.name, k.name, a.Component)
	case b.event == "":
		return Operation{}, fmt.Errorf("%w: the button %q of the %s of %s acts on the renderer alone", ErrButton, a.Component, sc.name, k.name)
	case b.event != a.Name:
		return Operation{}, fmt.Errorf("%w: the button %q of the %s of %s sends %q, not %q", ErrButton, a.Component, sc.name, k.name, b.event, a.Name)
	}
	for _, key := range b.keys {
		if _, ok := a.Context[key]; !ok {
			return Operation{}, fmt.Errorf("%w: %s does not send %q, which the button %q sends", ErrContext, a.Name, key, a.Component)
		}
	}
	for _, key := range sortedKeys(a.Context) {
		if !slices.Contains(b.keys, key) {
			return Operation{}, fmt.Errorf("%w: %s sends %q, which the button %q does not", ErrContext, a.Name, key, a.Component)
		}
	}
	op := b.op
	out := Operation{ID: op.id, Method: op.method, Path: op.path}
	if op.ticket {
		var t string
		if err := json.Unmarshal(a.Context["ticket"], &t); err != nil || t == "" {
			return Operation{}, fmt.Errorf("%w: %s sends the ticket %s, not its ID", ErrContext, a.Name, a.Context["ticket"])
		}
		out.Ticket = t
		out.Path = strings.Replace(op.path, "{id}", url.PathEscape(t), 1)
	}
	for _, key := range b.keys {
		v := a.Context[key]
		if !op.query[key] || isNull(v) {
			continue
		}
		var text string
		if err := json.Unmarshal(v, &text); err != nil {
			var n json.Number
			if err := decode(v, &n); err != nil {
				return Operation{}, fmt.Errorf("%w: %s sends %s as %s, not a string or a number", ErrContext, a.Name, key, bytes.TrimSpace(v))
			}
			text = n.String()
		}
		if out.Query == nil {
			out.Query = url.Values{}
		}
		out.Query.Set(key, text)
	}
	if op.body {
		var body bytes.Buffer
		body.WriteByte('{')
		n := 0
		for _, key := range b.keys {
			if key == "ticket" {
				continue
			}
			v := a.Context[key]
			if isNull(v) && !op.nullable[key] {
				if op.requires[key] {
					return Operation{}, fmt.Errorf("%w: %s sends no %s, which it requires", ErrContext, a.Name, key)
				}
				continue // a null is no value (ringi's DESIGN 12.4), and the API takes none here
			}
			if n > 0 {
				body.WriteByte(',')
			}
			name, _ := marshal(key)
			body.Write(name)
			body.WriteByte(':')
			if err := json.Compact(&body, v); err != nil {
				return Operation{}, fmt.Errorf("%w: %s sends %s that is not JSON", ErrContext, a.Name, key)
			}
			n++
		}
		body.WriteByte('}')
		out.Body = body.Bytes()
	}
	if op.method == "POST" {
		material := []any{a.SurfaceID, a.Component, string(out.Body)}
		if op.ticket {
			material = append(material, a.Timestamp.UTC().Format(time.RFC3339Nano))
		}
		m, _ := json.Marshal(material)
		sum := sha256.Sum256(m)
		out.Key = "screen-" + hex.EncodeToString(sum[:])
	}
	return out, nil
}

// isNull is whether a JSON text is null.
func isNull(v json.RawMessage) bool {
	return string(bytes.TrimSpace(v)) == "null"
}
