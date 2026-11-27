package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/internal/jatext"
)

// keyPrefix is put before an agent's idempotency key: an agent's keys and a screen's keys of the
// same person never meet, and the audit log tells the operations that came through the tools.
const keyPrefix = "mcp:"

// maxKey is the longest idempotency key the contract takes, in bytes (ringi's DESIGN 12.4), which
// the tools' idempotency_key takes too.
const maxKey = 255

// keyOf answers the server's key of an agent's idempotency key: keyPrefix and the key, or, for a
// key too long to keep with keyPrefix within maxKey bytes, keyPrefix and "sha256:" with the hex of
// the key's SHA-256 (the same key always makes the same server's key). ok is false for a key the
// contract does not take: 1 to maxKey bytes of visible ASCII, from ! to ~, with no space.
func keyOf(k string) (string, bool) {
	if len(k) == 0 || len(k) > maxKey {
		return "", false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < '!' || k[i] > '~' {
			return "", false
		}
	}
	if len(keyPrefix)+len(k) <= maxKey {
		return keyPrefix + k, true
	}
	sum := sha256.Sum256([]byte(k))
	return keyPrefix + "sha256:" + hex.EncodeToString(sum[:]), true
}

// handler answers the calls of a tool.
func (t *Tools) handler(d *toolDef, who whoFunc) sdkmcp.ToolHandler {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		p, h, err := who(ctx, req)
		if err != nil {
			return nil, err
		}
		lang := t.language(ctx, p, h)
		c, err := d.call(req.Params.Arguments)
		if err != nil {
			return answerOf(lang, outcomeInvalid, err.Error(), false), nil
		}
		// a find names its values in the session's language when the call names none
		c.Person, c.Via, c.Languages = p, "mcp", []string{lang}
		r, err := t.srv.Do(ctx, c)
		if err != nil {
			if server.Busy(err) {
				// too much work at once is not a failure: the caller may try again in a breath
				t.log.Error("a tool's call found no instance free", "tool", d.name, "person", p.ID, "ticket", c.Ticket, "err", err)
				return busy(lang), nil
			}
			t.log.Error("a tool's call failed", "tool", d.name, "person", p.ID, "ticket", c.Ticket, "err", err)
			return failed(lang), nil
		}
		res := t.reply(lang, d, c.Query, r, false)
		if d.read == "list_requests" && r.Outcome == server.OK {
			if later := t.later(ctx, p, lang); later != "" {
				if tc, ok := res.Content[0].(*sdkmcp.TextContent); ok {
					tc.Text = join(lang, tc.Text, later)
				}
			}
		}
		return res, nil
	}
}

// later says the kinds the person may request from a later day, and the day (Server.Offered), as
// the pages say a kind that has no form yet: the kinds with no version in effect now, whose tools
// of a new request are not listed (choose), and one of which starts later. A kind is said by its
// title in the language of the answer, else as written, of its highest version.
func (t *Tools) later(ctx context.Context, p server.Person, lang string) string {
	highest := map[string]int{}
	for _, pk := range t.packages() {
		for _, k := range pk.kinds {
			highest[k.name] = max(highest[k.name], k.version)
		}
	}
	var said []string
	for _, name := range slices.Sorted(maps.Keys(highest)) {
		from, ok, err := t.srv.Offered(ctx, p, name)
		if err != nil {
			t.log.Error("the day a kind is offered from", "kind", name, "person", p.ID, "err", err)
			continue
		}
		if !ok {
			continue
		}
		title, _ := t.srv.Title(name, highest[name], lang)
		if lang == "ja" {
			said = append(said, jatext.Join(name, "（", title, "）")+"は"+from+"から出せます。")
		} else {
			said = append(said, name+" ("+title+") may be requested from "+from+".")
		}
	}
	return join(lang, said...)
}

// wholeArg reads an argument that is a whole number: a number of JSON that server.WholeNumber reads.
func wholeArg(raw json.RawMessage) (int64, bool) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return 0, false
	}
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	return server.WholeNumber(n)
}

// call makes the framework's call of a tool's arguments (DESIGN 3).
func (d *toolDef) call(args json.RawMessage) (server.Call, error) {
	c := server.Call{Operation: d.name}
	m := map[string]json.RawMessage{}
	if t := bytes.TrimSpace(args); len(t) > 0 && !bytes.Equal(t, []byte("null")) {
		if err := json.Unmarshal(t, &m); err != nil {
			return c, errors.New("the arguments are not a JSON object")
		}
	}
	for _, p := range d.params {
		raw, ok := m[p.arg]
		delete(m, p.arg)
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if p.required {
				return c, fmt.Errorf("the argument %s is missing", p.arg)
			}
			continue
		}
		var s string
		if p.integer {
			// a whole number (a page's limit), read as the API reads one in a body and passed as its
			// digits: 1, 1.0 and 1e0 are 1. Any other value (1.5, a string, true) is passed as its
			// JSON, which the server refuses as the API refuses the query's (limit: a whole number
			// from 1 to 100 is expected, not "1.5"), in the API's order of the parameters.
			if n, ok := wholeArg(raw); ok {
				s = strconv.FormatInt(n, 10)
			} else {
				var b bytes.Buffer
				if json.Compact(&b, raw) != nil {
					return c, fmt.Errorf("the argument %s is not JSON", p.arg)
				}
				s = b.String()
			}
		} else if err := json.Unmarshal(raw, &s); err != nil {
			return c, fmt.Errorf("the argument %s is not a string", p.arg)
		}
		switch p.in {
		case "header":
			if s == "" {
				return c, fmt.Errorf("the argument %s is empty", p.arg)
			}
			k, ok := keyOf(s)
			if !ok {
				return c, fmt.Errorf("the argument %s is not 1 to %d bytes of visible ASCII (from ! to ~, no space)", p.arg, maxKey)
			}
			c.Key = k
		case "path":
			c.Ticket = s
		case "query":
			if c.Query == nil {
				c.Query = url.Values{}
			}
			c.Query.Set(p.name, s)
		}
	}
	if !d.body {
		for k := range m {
			return c, fmt.Errorf("the tool %s takes no argument %s", d.name, k)
		}
		return c, nil
	}
	b, err := canonical(m)
	if err != nil {
		return c, err
	}
	c.Body = b
	return c, nil
}

// canonical writes a body with its keys in order at every depth and its numbers as they came, so
// that the same call made again has the same digest.
func canonical(m map[string]json.RawMessage) ([]byte, error) {
	v := map[string]any{}
	for k, raw := range m {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var x any
		if err := dec.Decode(&x); err != nil {
			return nil, fmt.Errorf("the argument %s: %v", k, err)
		}
		v[k] = x
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// title answers a kind's title in lang, of the version given, as the server answers it
// (Server.Title: its `labels <lang>`, else its title as written); "" where that title is not in
// lang, or for a kind the server does not have.
func (t *Tools) title(name string, version int, lang string) string {
	title, in := t.srv.Title(name, version, lang)
	if in != lang {
		return ""
	}
	return title
}

// The outcomes an answer that is not OK tells.
const (
	outcomeRefused     = "refused"
	outcomeInvalid     = "invalid"
	outcomeNotFound    = "not_found"
	outcomeLimited     = "limited"
	outcomeFailed      = "failed"
	outcomeBusy        = "busy"
	outcomeUnavailable = "unavailable"
)

// reply makes a tool's result of the framework's reply to a call, whose query was q. A check's
// answers are not errors: they tell what the call would answer.
func (t *Tools) reply(lang string, d *toolDef, q url.Values, r server.Reply, check bool) *sdkmcp.CallToolResult {
	switch r.Outcome {
	case server.OK:
		if check {
			var b struct {
				Ticket json.RawMessage `json:"ticket"`
			}
			json.Unmarshal(r.Body, &b)
			x := map[string]any{"outcome": "ok", "saved": false, "ticket": b.Ticket}
			text := join(lang, say(lang, "nothing"), ticketSentence(lang, b.Ticket, "would"))
			if len(r.NotAsked) > 0 {
				x["books_not_asked"] = notAsked(r.NotAsked)
				text = join(lang, text, say(lang, "books not asked"))
			}
			out, _ := json.Marshal(x)
			return result(false, text, out)
		}
		return result(false, okSentence(lang, d, q, r.Body, func(name string, version int) string { return t.title(name, version, lang) }), r.Body)
	case server.Refused:
		// The refusal's text in the person's language, and the place of the form it names (ringi's
		// DESIGN 12.4).
		var b struct {
			Refused string  `json:"refused"`
			Text    string  `json:"text"`
			TextJa  string  `json:"text_ja"`
			At      *string `json:"at"`
		}
		json.Unmarshal(r.Body, &b)
		text := b.Text
		if lang == "ja" && b.TextJa != "" {
			text = b.TextJa
		}
		x := map[string]any{"outcome": outcomeRefused, "refused": b.Refused, "text": text}
		if b.At != nil {
			x["at"] = *b.At
		}
		out, _ := json.Marshal(x)
		return result(!check, refusalSentence(lang, b.Refused, text, check), out)
	case server.Invalid:
		return answerOf(lang, outcomeInvalid, r.Problem, check)
	case server.NotFound:
		if r.From != "" {
			return unofferedOf(lang, r, check)
		}
		return answerOf(lang, outcomeNotFound, r.Problem, check)
	case server.Limited:
		return limitedOf(lang, r, check)
	case server.Unavailable:
		return unavailableOf(lang, r, check)
	}
	return failed(lang)
}

// unavailableOf is the result of an event the ticket's kind failed to answer within the minute
// (the API's 503): an error that says how long to wait, as limitedOf does for the server's 429.
func unavailableOf(lang string, r server.Reply, check bool) *sdkmcp.CallToolResult {
	x := map[string]any{"outcome": outcomeUnavailable, "problem": r.Problem}
	if r.RetryAfter > 0 {
		n, _ := strconv.Atoi(server.RetryAfter(r.RetryAfter))
		x["retry_after"] = n
	}
	out, _ := json.Marshal(x)
	wait := server.RetryAfter(r.RetryAfter)
	var text string
	switch {
	case lang == "ja":
		text = join(lang, say(lang, outcomeUnavailable), wait+"秒待ってから、同じ呼び出しを同じ idempotency_key でやり直してください：", r.Problem)
	default:
		text = join(lang, say(lang, outcomeUnavailable), "Make the same call again in "+wait+" seconds, with the same idempotency_key:", r.Problem)
	}
	if check {
		text = join(lang, say(lang, "nothing"), say(lang, "would be limited"), r.Problem)
	}
	return result(!check, text, out)
}

// limitedOf is the result of a call the server does not take now (the API's 429): an error that
// says how long to wait, if waiting helps (retry_after, in seconds), or a check's answer.
func limitedOf(lang string, r server.Reply, check bool) *sdkmcp.CallToolResult {
	x := map[string]any{"outcome": outcomeLimited, "problem": r.Problem}
	wait := ""
	if r.RetryAfter > 0 {
		wait = server.RetryAfter(r.RetryAfter)
		n, _ := strconv.Atoi(wait)
		x["retry_after"] = n
	}
	out, _ := json.Marshal(x)
	var text string
	switch {
	case wait == "":
		text = join(lang, say(lang, "limited"), say(lang, "no more"), r.Problem)
	case lang == "ja":
		text = join(lang, say(lang, "limited"), wait+"秒待ってから、同じ呼び出しを同じ idempotency_key でやり直してください：", r.Problem)
	default:
		text = join(lang, say(lang, "limited"), "Make the same call again in "+wait+" seconds, with the same idempotency_key:", r.Problem)
	}
	if check {
		text = join(lang, say(lang, "nothing"), say(lang, "would be limited"), r.Problem)
	}
	return result(!check, text, out)
}

// notAsked writes the books' calls a check did not ask: the call, the book and the transfer.
func notAsked(calls []server.BookCall) []map[string]string {
	out := make([]map[string]string, len(calls))
	for i, c := range calls {
		out[i] = map[string]string{"call": c.Call, "book": c.Book, "transfer": c.Transfer}
	}
	return out
}

// answerOf is the result of an outcome with a problem: an error, or a check's answer.
func answerOf(lang, outcome, problem string, check bool) *sdkmcp.CallToolResult {
	out, _ := json.Marshal(map[string]any{"outcome": outcome, "problem": problem})
	text := join(lang, say(lang, outcome), problem)
	if check {
		text = join(lang, say(lang, "nothing"), say(lang, "would be "+outcome), problem)
	}
	return result(!check, text, out)
}

// unofferedOf is the result of a call on a kind with no version in effect now, to a person who may
// make it once its next version starts (the API's 404 with `from`): an error that says the day.
func unofferedOf(lang string, r server.Reply, check bool) *sdkmcp.CallToolResult {
	out, _ := json.Marshal(map[string]any{"outcome": outcomeNotFound, "problem": r.Problem, "from": r.From})
	when := "This request may be made from " + r.From + "."
	if lang == "ja" {
		when = "この申請は" + r.From + "から出せます。"
	}
	text := join(lang, say(lang, outcomeNotFound), when)
	if check {
		text = join(lang, say(lang, "nothing"), say(lang, "would be not_found"), when)
	}
	return result(!check, text, out)
}

// failed is the result of a call the server failed to do.
func failed(lang string) *sdkmcp.CallToolResult {
	return result(true, say(lang, outcomeFailed), []byte(`{"outcome":"failed"}`))
}

// busy is the result of a call that found no instance free (the API's 503 with Retry-After: 1):
// too much work at once, not a failure, and the caller may make the same call again in a breath.
func busy(lang string) *sdkmcp.CallToolResult {
	return result(true, say(lang, outcomeBusy), []byte(`{"outcome":"busy","retry_after":1}`))
}

// result is a tool's result: the sentence, and the structured content, also as JSON text.
func result(isError bool, sentence string, structured []byte) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		IsError:           isError,
		StructuredContent: json.RawMessage(structured),
		Content:           []sdkmcp.Content{&sdkmcp.TextContent{Text: sentence}, &sdkmcp.TextContent{Text: string(structured)}},
	}
}

// language answers the language of a person's answers: Config.Language's, else as the pages choose
// theirs (server.LanguageChoice): the most wanted of en and ja in Accept-Language, read as the pages
// read it (server.AcceptLanguages), else the language the organization's data gives the person
// unless the header does not want it, else en.
func (t *Tools) language(ctx context.Context, p server.Person, h http.Header) string {
	if t.cfg.Language != nil {
		if l := t.cfg.Language(p, h); l == "ja" {
			return "ja"
		}
		return "en"
	}
	person, err := t.srv.PersonLanguage(ctx, p.ID)
	if err != nil {
		t.log.Error("the language of a person", "person", p.ID, "err", err)
	}
	if l := (server.LanguageChoice{Accept: h.Get("Accept-Language"), Person: person}).Choose(func(l string) bool { return l == "en" || l == "ja" }); l != "" {
		return l
	}
	return "en"
}
