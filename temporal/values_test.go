package temporal

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	sdktemporal "go.temporal.io/sdk/temporal"

	server "github.com/i2y/ringi-server"
)

// The named values of the contract, for the tests.
func str(s string) string {
	var b strings.Builder
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	return `{"str":{"v":` + strings.TrimSpace(b.String()) + `}}`
}

func num(n string) string     { return `{"num":{"v":` + n + `}}` }
func truth(b string) string   { return `{"truth":{"v":` + b + `}}` }
func rows(r ...string) string { return `{"rows":{"v":[` + strings.Join(r, ",") + `]}}` }

const nothing = `"nothing"`

// named writes named values from names and values in turn.
func named(nv ...string) string {
	var parts []string
	for i := 0; i+1 < len(nv); i += 2 {
		b, _ := json.Marshal(nv[i])
		parts = append(parts, `{"name":`+string(b)+`,"value":`+nv[i+1]+`}`)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestToObject(t *testing.T) {
	args := named(
		"request_id", str("T-1"),
		"amount", num("28000"),
		"items", rows(named("what", str("Train to Osaka and back"), "price", num("28000")), named("what", str("Taxi"), "price", num("1500"))),
		"trip_to", nothing,
		"urgent", truth("false"),
		"note", str(`a "quoted" <text>`),
	)
	got, err := toObject(json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"request_id":"T-1","amount":28000,"items":[{"what":"Train to Osaka and back","price":28000},{"what":"Taxi","price":1500}],"trip_to":null,"urgent":false,"note":"a \"quoted\" <text>"}`
	if string(got) != want {
		t.Errorf("toObject:\n got %s\nwant %s", got, want)
	}
	for _, empty := range []string{"", "null", "[]"} {
		if got, err := toObject(json.RawMessage(empty)); err != nil || string(got) != "{}" {
			t.Errorf("toObject(%q) = %s, %v; want {}", empty, got, err)
		}
	}
	for _, bad := range []string{
		named("a", `{"money":{"v":1}}`),
		named("a", `{"str":{"v":1}}`),
		named("a", `{"num":{"v":"1"}}`),
		named("a", `{"truth":{"v":"yes"}}`),
		named("a", `"something"`),
		named("a", `{"str":{"v":"x"},"num":{"v":1}}`),
		named("a", rows(`{"what":1}`)),
		`{"a":1}`,
	} {
		if got, err := toObject(json.RawMessage(bad)); err == nil {
			t.Errorf("toObject(%s) = %s; want an error", bad, got)
		}
	}
}

func TestNamedValues(t *testing.T) {
	// The outputs as a run by Go ends with them: in the order of their names.
	out := `{"differences":"None","due":null,"lines":[{"price":1200,"what":"Fee"}],"months":12.0,"obligations":"Pay the fee each month","risk":"low","signed":true}`
	declared := named("obligations", str("Pay the fee each month"), "differences", str("None"), "risk", str("low"),
		"due", nothing, "lines", rows(named("price", num("1200"), "what", str("Fee"))), "months", num("12"), "signed", truth("true"))
	asCome := named("differences", str("None"), "due", nothing, "lines", rows(named("price", num("1200"), "what", str("Fee"))),
		"months", num("12"), "obligations", str("Pay the fee each month"), "risk", str("low"), "signed", truth("true"))
	for _, c := range []struct {
		order []string
		want  string
	}{
		{[]string{"obligations", "differences", "risk", "absent"}, declared},
		{nil, asCome},
	} {
		got, err := namedValues(json.RawMessage(out), c.order)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want {
			t.Errorf("namedValues(%v):\n got %s\nwant %s", c.order, got, c.want)
		}
	}
	for _, empty := range []string{"", "null", "{}"} {
		if got, err := namedValues(json.RawMessage(empty), nil); err != nil || string(got) != "[]" {
			t.Errorf("namedValues(%q) = %s, %v; want []", empty, got, err)
		}
	}
	for _, bad := range []string{
		`{"a":1.5}`,
		`{"a":{"b":1}}`,
		`{"a":[1,2]}`,
		`{"a":1e300}`,
		`["a"]`,
		`"text"`,
	} {
		if got, err := namedValues(json.RawMessage(bad), nil); err == nil {
			t.Errorf("namedValues(%s) = %s; want an error", bad, got)
		}
	}
}

func TestAnswerOf(t *testing.T) {
	key := server.EffectKey{Ticket: "T-1", Round: 1, Serial: 4}
	agent := askInput{Key: key}
	for _, c := range []struct {
		out  string
		want answerInput
	}{
		{`{"verdict":"ok","reason":"The purpose names what it pays for","score":1}`, answerInput{Key: key, Verdict: "ok", Reason: "The purpose names what it pays for"}},
		{`{"reason":"The fare is above the usual one","verdict":"doubt"}`, answerInput{Key: key, Verdict: "doubt", Reason: "The fare is above the usual one"}},
		{`{"verdict":"doubt"}`, answerInput{Key: key, Verdict: "doubt"}},
		{`{"verdict":"ok","reason":null}`, answerInput{Key: key, Verdict: "ok"}},
	} {
		if got := answerOf(agent, json.RawMessage(c.out), nil); !reflect.DeepEqual(got, c.want) {
			t.Errorf("answerOf(%s) = %+v; want %+v", c.out, got, c.want)
		}
	}
	for _, out := range []string{`{"verdict":"maybe"}`, `{"reason":"no verdict"}`, `{"verdict":1}`, `{"verdict":"ok","reason":3}`, `null`, `[]`} {
		if got := answerOf(agent, json.RawMessage(out), nil); !got.Failed || got.Why == "" || got.Verdict != "" {
			t.Errorf("answerOf(%s) = %+v; want Failed, with why", out, got)
		}
	}
	if got := answerOf(agent, nil, errors.New("the flow failed")); !got.Failed || got.Why != "the flow failed" {
		t.Errorf("answerOf(an error) = %+v; want Failed, with why", got)
	}
	note := askInput{Key: key, Note: true, Outputs: []string{"obligations", "risk"}}
	got := answerOf(note, json.RawMessage(`{"risk":"high","obligations":"","extra":true}`), nil)
	want := named("obligations", str(""), "risk", str("high"), "extra", truth("true"))
	if got.Failed || string(got.Outputs) != want {
		t.Errorf("answerOf(a note) = %+v; want the outputs %s", got, want)
	}
	if got := answerOf(note, json.RawMessage(`{"summary":{"risk":"high"}}`), nil); !got.Failed {
		t.Errorf("answerOf(a note that does not fit) = %+v; want Failed", got)
	}
	if got := answerOf(note, nil, nil); got.Failed || string(got.Outputs) != "[]" {
		t.Errorf("answerOf(a note with no outputs) = %+v; want no outputs", got)
	}
}

func TestEndedOf(t *testing.T) {
	key := server.EffectKey{Ticket: "T-1", Round: 1, Serial: 9}
	in := launchInput{Key: key, Outputs: []string{"reference", "amount"}}
	// the outputs in the order the flow declares them, and the others after them
	got := endedOf(in, json.RawMessage(`{"amount":4800,"note":"paid","reference":"PAY-1"}`), nil)
	want := named("reference", str("PAY-1"), "amount", num("4800"), "note", str("paid"))
	if got.Failed || string(got.Outputs) != want {
		t.Errorf("endedOf(outputs) = %+v; want the outputs %s", got, want)
	}
	for _, out := range []string{``, `null`} {
		if got := endedOf(in, json.RawMessage(out), nil); got.Failed || got.Outputs != nil {
			t.Errorf("endedOf(%q) = %+v; want done, with no outputs", out, got)
		}
	}
	if got := endedOf(in, json.RawMessage(`{"payment":{"reference":"PAY-1"}}`), nil); !got.Failed || !strings.HasPrefix(got.Reason, "the outputs do not fit: ") {
		t.Errorf("endedOf(an object in the outputs) = %+v; want failed, with why", got)
	}
	// the message of the innermost application error, as a reason the server takes
	err := fmt.Errorf("child workflow failed: %w", sdktemporal.NewApplicationErrorWithCause("activity failed", "Act", sdktemporal.NewApplicationError("the accounts system\tis down", "Down")))
	if got := endedOf(in, nil, err); !got.Failed || got.Reason != "the accounts system is down" {
		t.Errorf("endedOf(an error) = %+v; want failed, with the innermost message", got)
	}
	long := strings.Repeat("あ", server.MaxReason+10)
	if got := endedOf(in, nil, errors.New(long)); utf8.RuneCountInString(got.Reason) != server.MaxReason || !strings.HasSuffix(got.Reason, "…") {
		t.Errorf("endedOf(a long error) has a reason of %d characters", utf8.RuneCountInString(got.Reason))
	}
}
