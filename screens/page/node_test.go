package page

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ringi-server/screens"
)

// TestScriptAndServerAlike runs page.js and the set's functions.js in Node, if there is one, and
// holds them to the Go reading: page.js reads and writes numbers and days as number.go and read.go
// do; and each form a page posts is read into the body functions.js sends for the same form
// (`<kind>.given`, `.changes` and `.cleared`, `.edits` and `.edits_cleared`), the fields the script
// hides being those functions.js hides. A post the page's checks stop must fail the same checks of
// functions.js. A post the page sends and functions.js stops (what the page leaves to next) is run
// through the kind, which must refuse it as form at the place of a check functions.js fails.
func TestScriptAndServerAlike(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("SKIP: node is not on PATH; page.js is not held to the Go reading, nor the posts to functions.js")
	}
	_, p := testPages(t)
	in := nodeInput{}
	// numbers: what people type, and what the pages write
	for _, c := range numberTypes {
		u, err := parseUnit(c.written)
		must(t, err)
		ju := jsUnit{Num: u.num, Den: u.den, Decimals: u.decimals, Code: u.code, Yen: u.money && u.code == "JPY"}
		var typed []string
		for _, n := range []int64{0, 1, -1, 7, 125, 999, 1000, 9999, 10000, 123456, -123456, 1234567, maxExact, -maxExact} {
			typed = append(typed, u.text(n, false), u.text(n, true), u.shown(n, "en"), u.shown(n, "ja"))
			for _, grouped := range []bool{false, true} {
				in.Texts = append(in.Texts, jsText{N: n, Unit: ju, Grouped: grouped, written: c.written})
			}
		}
		typed = append(typed, oddNumbers...)
		for _, s := range typed {
			in.Numbers = append(in.Numbers, jsNumber{Typed: s, Unit: ju, written: c.written})
		}
	}
	for _, c := range readCases {
		u, err := parseUnit(c.typ)
		must(t, err)
		in.Numbers = append(in.Numbers, jsNumber{Typed: c.typed, Unit: jsUnit{Num: u.num, Den: u.den, Decimals: u.decimals, Code: u.code, Yen: u.money && u.code == "JPY"}, written: c.typ})
	}
	in.Days = oddDays
	// forms: a new request, one made again, an approver's edit
	in.Forms = formCases(t, p)
	// the values the forms work out, over the tickets' forms
	in.Worked = workedCases(t, p)
	out := runNode(t, node, p, in)
	if len(out.Numbers) != len(in.Numbers) || len(out.Texts) != len(in.Texts) || len(out.Days) != len(in.Days) || len(out.Forms) != len(in.Forms) || len(out.Worked) != len(in.Worked) {
		t.Fatalf("Node answers %d %d %d %d %d, not %d %d %d %d %d", len(out.Numbers), len(out.Texts), len(out.Days), len(out.Forms), len(out.Worked),
			len(in.Numbers), len(in.Texts), len(in.Days), len(in.Forms), len(in.Worked))
	}
	if len(in.Worked) == 0 {
		t.Errorf("no form shows a value it works out")
	}
	for i, c := range in.Worked {
		g := out.Worked[i]
		var v int64
		if err := json.Unmarshal(g.Value, &v); err != nil || v != c.want || g.Text == nil || *g.Text != c.text {
			text := "nothing"
			if g.Text != nil {
				text = strconv.Quote(*g.Text)
			}
			t.Errorf("%s: functions.js works out %s and page.js shows %s; the kind worked out %d, and the server shows %q", c.about, g.Value, text, c.want, c.text)
		}
	}
	for i, c := range in.Numbers {
		u, _ := parseUnit(c.written)
		n, why := u.read(c.Typed)
		if g := out.Numbers[i]; g.Why != why || why == "" && (g.Steps == nil || *g.Steps != n) {
			t.Errorf("%s %q: the script reads %v %q, the server %d %q", c.written, c.Typed, g.Steps, g.Why, n, why)
		}
	}
	for i, c := range in.Texts {
		u, _ := parseUnit(c.written)
		if want := u.text(c.N, c.Grouped); out.Texts[i] != want {
			t.Errorf("%s %d: the script writes %q, the server %q", c.written, c.N, out.Texts[i], want)
		}
	}
	for i, typed := range in.Days {
		day, ok := readDay(typed)
		if g := out.Days[i]; g.Day != day || (g.Why == "") != ok {
			t.Errorf("%q: the script reads %q %q, the server %q %v", typed, g.Day, g.Why, day, ok)
		}
	}
	counts := map[string]int{}
	for i, c := range in.Forms {
		what, err := postForm(t, p, c, out.Forms[i], i)
		if err != nil {
			t.Errorf("%s %s (case %d, %s): %v", c.Kind, c.What, i, c.about, err)
			continue
		}
		counts[c.What+": "+what]++
	}
	t.Logf("%d numbers read, %d written, %d days, %d values worked out, %d forms:", len(in.Numbers), len(in.Texts), len(in.Days), len(in.Worked), len(in.Forms))
	for _, k := range sortedCounts(counts) {
		t.Logf("  %5d  %s", counts[k], k)
	}
}

// oddNumbers are typed in every unit besides its own values: white space of every kind, the sign
// and the point where they do not go, another unit.
var oddNumbers = []string{
	"", " ", "1,0000", "1.", ".5", "1..2", "--1", "+-1", "1,000.", "１２３", "１，２３４", "¥", "円", "%", "12円",
	"¥12", "12 円", "12%", "12 %", "12 USD", "12 kg", "12kg", "１２ｋｇ", " 12 ", "\u00a012\u00a0", "\ufeff12",
	"\u008512", "12\u2028", "\u180e12", "\u200b12", "\u300012\u3000", "12\t", "1 000", "1_000", "0x10",
	"1e3", "Infinity", "NaN", "9007199254740991", "9007199254740992", "-9007199254740992",
	"99999999999999999999999", "0.0000000000000000001", "-0", "+0", "00012", "1,234,567.25",
}

// oddDays are days typed in every way the pages read and some they do not.
var oddDays = []string{
	"2026-11-17", "2026/11/17", "2026/1/5", "2026-1-5", "２０２６－１１－１７", "２０２６/１/５", " 2026-11-17 ",
	"2026-02-29", "2024-02-29", "2100-02-29", "2000-02-29", "2026-13-01", "2026-00-10", "2026-04-31",
	"2026-04-30", "26-11-17", "2026-11-17T00:00", "", "0000-01-01", "9999-12-31", "2026/11/17/",
	"2026年11月17日", "2026-011-17", "2026/011/17", "2026.11.17", "20261117", "2026-11-1７",
}

type jsUnit struct {
	Num      int64  `json:"num"`
	Den      int64  `json:"den"`
	Decimals int    `json:"decimals"`
	Code     string `json:"code"`
	Yen      bool   `json:"yen"`
}

type jsNumber struct {
	Typed   string `json:"typed"`
	Unit    jsUnit `json:"unit"`
	written string
}

type jsText struct {
	N       int64  `json:"n"`
	Unit    jsUnit `json:"unit"`
	Grouped bool   `json:"grouped"`
	written string
}

// formCase is a form a person sends: a new request (submit), one made again (resubmit, over the
// ticket's form, before), or an approver's edit (edit, over before).
type formCase struct {
	Kind   string         `json:"kind"`
	What   string         `json:"what"`
	Form   map[string]any `json:"form"`
	Before map[string]any `json:"before"`
	Edit   map[string]any `json:"edit"`
	Shown  []string       `json:"shown"` // the fields shown on a condition
	Codes  []string       `json:"codes"` // the checks of the kind's form
	ticket json.RawMessage
	about  string
}

// workedCase is a computed field a form shows, as the page tells the script of it (its function,
// unit and format), over a ticket's form without its computed fields: what the kind worked out, and
// the text the server shows for it.
type workedCase struct {
	Fn     string         `json:"fn"`
	Form   map[string]any `json:"form"`
	Unit   jsUnit         `json:"unit"`
	Format string         `json:"format"`
	want   int64
	text   string
	about  string
}

type nodeInput struct {
	Numbers []jsNumber   `json:"numbers"`
	Texts   []jsText     `json:"texts"`
	Days    []string     `json:"days"`
	Forms   []formCase   `json:"forms"`
	Worked  []workedCase `json:"worked"`
}

type formAnswer struct {
	Shown   map[string]bool `json:"shown"`
	Given   map[string]any  `json:"given"`
	Changes map[string]any  `json:"changes"`
	Cleared []any           `json:"cleared"`
	Failing []string        `json:"failing"`
}

type nodeOutput struct {
	Numbers []struct {
		Steps *int64 `json:"steps"`
		Why   string `json:"why"`
	} `json:"numbers"`
	Texts []string `json:"texts"`
	Days  []struct {
		Day string `json:"day"`
		Why string `json:"why"`
	} `json:"days"`
	Forms  []formAnswer `json:"forms"`
	Worked []struct {
		Value json.RawMessage `json:"value"`
		Text  *string         `json:"text"`
	} `json:"worked"`
}

// driver runs in Node beside page.js and functions.js: it reads input.json and writes what the
// script and the functions answer.
const driver = `import { readFileSync } from "node:fs";
import { readNumber, stepsText, readDate, workedText } from "./page.js";
import { functions } from "./functions.js";

const input = JSON.parse(readFileSync(new URL("./input.json", import.meta.url), "utf8"));
const fn = (k, name, args) => {
  const f = functions[k + "." + name];
  if (typeof f !== "function") throw new Error("functions.js has no " + k + "." + name);
  return f(args);
};
const shownOf = (k, form, names) => Object.fromEntries(names.map((n) => [n, fn(k, "shown_" + n, { form }) !== false]));
const failing = (k, form, codes) => codes.filter((code) => fn(k, "check", { form, code }) === false);
const raw = (name, args) => {
  const f = functions[name];
  if (typeof f !== "function") throw new Error("functions.js has no " + name);
  return f(args);
};
const out = {
  numbers: input.numbers.map((c) => readNumber(c.typed, c.unit)),
  texts: input.texts.map((c) => stepsText(c.n, c.unit, c.grouped)),
  days: input.days.map((d) => readDate(d)),
  forms: input.forms.map((c) => {
    const k = c.kind;
    if (c.what === "edit") {
      const after = fn(k, "edited", { edit: c.edit, before: c.before });
      return {
        shown: shownOf(k, after, c.shown),
        changes: fn(k, "edits", { edit: c.edit, before: c.before }),
        cleared: fn(k, "edits_cleared", { edit: c.edit, before: c.before }),
        failing: failing(k, after, c.codes),
      };
    }
    const r = { shown: shownOf(k, c.form, c.shown), given: fn(k, "given", { form: c.form }), failing: failing(k, c.form, c.codes) };
    if (c.what === "resubmit") {
      r.changes = fn(k, "changes", { form: c.form, before: c.before });
      r.cleared = fn(k, "cleared", { form: c.form, before: c.before });
    }
    return r;
  }),
  worked: input.worked.map((c) => ({ value: raw(c.fn, { form: c.form }) ?? null, text: workedText(c.fn, c.form, c.unit, c.format) ?? null })),
};
process.stdout.write(JSON.stringify(out));
`

// runNode writes page.js, functions.js and the driver to a directory of the test's, and runs them.
func runNode(t *testing.T, node string, p *Pages, in nodeInput) nodeOutput {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, body []byte) {
		must(t, os.WriteFile(filepath.Join(dir, name), body, 0o600))
	}
	for _, name := range []string{"page.js", "functions.js"} {
		a, err := p.Asset(name)
		must(t, err)
		write(name, a.Body)
	}
	write("package.json", []byte(`{"type": "module"}`))
	write("driver.js", []byte(driver))
	write("input.json", jsonOf(in))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "driver.js")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var out nodeOutput
	dec := json.NewDecoder(&stdout)
	dec.UseNumber()
	must(t, dec.Decode(&out))
	return out
}

// formCases are the forms of the vectors' tickets, and each ticket's form with one field changed:
// emptied, given another value, a choice changed (which hides a field shown on its condition).
func formCases(t *testing.T, p *Pages) []formCase {
	ticks, _ := testTickets(t)
	var out []formCase
	for _, kn := range slices.Sorted(maps.Keys(p.kinds)) {
		k := p.kinds[kn]
		d := k.d
		shown, codes := []string{}, []string{}
		for _, f := range d.Form {
			if f.ShownWhen != nil {
				shown = append(shown, f.Name)
			}
			for _, c := range f.Fields {
				for _, ch := range fieldChecks(c, f, "en") {
					codes = append(codes, ch.code)
				}
			}
			for _, ch := range fieldChecks(f, nil, "en") {
				codes = append(codes, ch.code)
			}
		}
		for _, ch := range workedChecks(d, "en", func(*field) bool { return true }) {
			codes = append(codes, ch.code)
		}
		type ticket struct {
			raw    json.RawMessage
			status string
			form   map[string]any
			waits  bool // an approver waits on it, who may edit it
		}
		var all []ticket
		forms := map[string]map[string]any{}
		for _, raw := range ticks[kn] {
			var m map[string]any
			must(t, decode(raw, &m))
			f, _ := m["form"].(map[string]any)
			s, _ := m["status"].(string)
			all = append(all, ticket{raw, s, f, waitingApprover(m) != nil})
			forms[string(jsonOf(f))] = f
		}
		// the forms, in a fixed order, at most twelve spread over them
		keys := slices.Sorted(maps.Keys(forms))
		var afters []map[string]any
		for i := range min(12, len(keys)) {
			afters = append(afters, forms[keys[i*len(keys)/min(12, len(keys))]])
		}
		// a field's other values: each choice, nothing, one of the test's
		others := func(f *field, was any) []any {
			out := []any{nil}
			switch f.kind {
			case "choice":
				for _, c := range f.Choices {
					if c.Name != was {
						out = append(out, c.Name)
					}
				}
			case "text", "longtext":
				out = append(out, "Written again for the test")
			case "date":
				day := f.minDay
				if day == "" {
					day = "2026-12-01"
				}
				out = append(out, day)
			case "number":
				out = append(out, json.Number("1"))
			case "master", "person", "team":
				out = append(out, "x-test")
			case "attachment":
				out = append(out, "other.pdf")
			case "bool":
				b, _ := was.(bool)
				out = append(out, !b)
			}
			return out
		}
		with := func(m map[string]any, name string, v any) map[string]any {
			c := map[string]any{}
			for k, x := range m {
				c[k] = x
			}
			if v == nil {
				delete(c, name)
			} else {
				c[name] = v
			}
			return c
		}
		add := func(c formCase) {
			c.Kind, c.Shown, c.Codes = kn, shown, codes
			out = append(out, c)
		}
		for i, f := range afters {
			add(formCase{What: "submit", Form: f, about: fmt.Sprintf("the form %d", i)})
		}
		// the tickets of a status, one of each form, at most six spread over them; waiting, only those
		// an approver waits on (an edit is an approver's, on a stage of people)
		pick := func(status string, waiting bool) []ticket {
			var got []ticket
			seen := map[string]bool{}
			for _, tk := range all {
				if tk.status == status && (tk.waits || !waiting) && !seen[string(jsonOf(tk.form))] {
					seen[string(jsonOf(tk.form))] = true
					got = append(got, tk)
				}
			}
			if len(got) > 6 {
				got = []ticket{got[0], got[len(got)/5], got[2*len(got)/5], got[3*len(got)/5], got[4*len(got)/5], got[len(got)-1]}
			}
			return got
		}
		for _, tk := range pick("with_requester", false) {
			for i, f := range afters {
				add(formCase{What: "resubmit", Form: f, Before: tk.form, ticket: tk.raw, about: fmt.Sprintf("the form %d", i)})
			}
			for _, f := range d.Form {
				if !f.given() || f.kind == "rows" {
					continue
				}
				for _, v := range others(f, tk.form[f.Name]) {
					add(formCase{What: "resubmit", Form: with(tk.form, f.Name, v), Before: tk.form, ticket: tk.raw, about: fmt.Sprintf("%s: %v", f.Name, v)})
				}
			}
		}
		var edited []*field
		for _, f := range d.Form {
			if f.edited {
				edited = append(edited, f)
			}
		}
		if len(edited) == 0 {
			continue
		}
		for _, tk := range pick("in_progress", true) {
			base := map[string]any{}
			for _, f := range edited {
				if v, ok := tk.form[f.Name]; ok {
					base[f.Name] = v
				}
			}
			add(formCase{What: "edit", Edit: base, Before: tk.form, ticket: tk.raw, about: "nothing changed"})
			for _, f := range edited {
				vals := others(f, tk.form[f.Name])
				for _, a := range afters {
					if v, ok := a[f.Name]; ok && !slices.ContainsFunc(vals, func(x any) bool { return same(x, v) }) {
						vals = append(vals, v)
					}
				}
				for _, v := range vals {
					add(formCase{What: "edit", Edit: with(base, f.Name, v), Before: tk.form, ticket: tk.raw, about: fmt.Sprintf("%s: %v", f.Name, v)})
				}
			}
		}
	}
	return out
}

func sortedCounts(m map[string]int) []string {
	keys := slices.Collect(maps.Keys(m))
	slices.SortFunc(keys, func(a, b string) int {
		if m[a] != m[b] {
			return m[a] - m[b]
		}
		return strings.Compare(a, b)
	})
	return keys
}

// session is a page in a browser with no script: a post that adds or removes a row shows it again.
type session struct {
	b    *browser
	show func(*Reading) ([]byte, error)
	read func(Post) (*Reading, error)
	lang string
}

func (s *session) rowsTo(rows *field, n int) error {
	for range 50 {
		marks := s.b.values["r."+rows.Name]
		var do string
		switch {
		case len(marks) < n:
			do = "row.add:" + rows.Name
		case len(marks) > n:
			do = "row.remove:" + rows.Name + ":" + marks[len(marks)-1]
		default:
			return nil
		}
		post, err := s.b.press(do)
		if err != nil {
			return err
		}
		rd, err := s.read(post)
		if err != nil {
			return err
		}
		if rd.Operation != nil {
			return fmt.Errorf("%s made an operation", do)
		}
		page, err := s.show(rd)
		if err != nil {
			return err
		}
		if s.b, err = browse(page); err != nil {
			return err
		}
	}
	return errors.New("the rows do not come to the count")
}

// fill types the values of the fields of which in the page, emptying those values does not hold.
func (s *session) fill(d *description, which func(*field) bool, values map[string]any, style int) error {
	for _, f := range d.Form {
		if !which(f) {
			continue
		}
		x := values[f.Name]
		if f.kind != "rows" {
			if err := typeValue(s.b, f, "f."+f.Name, x, style, s.lang); err != nil {
				return err
			}
			continue
		}
		list, _ := x.([]any)
		if err := s.rowsTo(f, len(list)); err != nil {
			return err
		}
		marks := s.b.values["r."+f.Name]
		for i, row := range list {
			m, _ := row.(map[string]any)
			for _, c := range f.Fields {
				if c.given() {
					if err := typeValue(s.b, c, "f."+f.Name+"."+marks[i]+"."+c.Name, m[c.Name], (style+i)%4, s.lang); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// postForm makes a case on its page and posts it, and holds what the page sends to what functions.js
// answers: what it sent, or why not.
func postForm(t *testing.T, p *Pages, c formCase, js formAnswer, i int) (string, error) {
	lang := []string{"en", "ja"}[i%2]
	k := p.kinds[c.Kind]
	s := &session{lang: lang}
	var button string
	switch c.What {
	case "submit", "resubmit":
		in := Form{Form: screens.Form{Kind: c.Kind, Language: lang, Surface: fmt.Sprintf("node%d", i), MaySubmit: true, Can: []string{"submit"}}, Action: "/post", Now: testNow}
		if c.What == "resubmit" {
			in.Form = screens.Form{Language: lang, Ticket: c.ticket, Can: []string{"resubmit", "discard"}}
		}
		s.show = func(rd *Reading) ([]byte, error) {
			in := in
			in.Again = rd
			pg, err := p.Form(in)
			return pg.Document(), err
		}
		keep := &keeper{}
		s.read = func(post Post) (*Reading, error) { return p.ReadForm(in, post, keep) }
		button = "button." + c.What
	case "edit":
		in := Card{Card: screens.Card{Language: lang, Ticket: c.ticket, Can: []string{"edit"}}, Action: "/post", Now: testNow}
		s.show = func(rd *Reading) ([]byte, error) {
			in := in
			in.Again = rd
			pg, err := p.Card(in)
			return pg.Document(), err
		}
		keep := &keeper{}
		s.read = func(post Post) (*Reading, error) { return p.ReadCard(in, post, keep) }
		button = "button.edit"
	}
	page, err := s.show(nil)
	if err != nil {
		return "", err
	}
	if s.b, err = browse(page); err != nil {
		return "", err
	}
	if !slices.Contains(s.b.buttons, button) {
		return "no " + button + " on the page", nil
	}
	which, values := (*field).given, c.Form
	if c.What == "edit" {
		which, values = func(f *field) bool { return f.edited }, c.Edit
	}
	if err := s.fill(k.d, which, values, i%4); err != nil {
		if errors.Is(err, errNotOffered) {
			return "a value the page does not offer", nil
		}
		return "", err
	}
	// the script hides what functions.js says is hidden: its parts are not posted
	hidden := ""
	for name, on := range js.Shown {
		if f := k.d.fieldNamed(name); !on && f != nil && which(f) {
			hide(s.b, name)
			hidden = ", a field hidden"
		}
	}
	post, err := s.b.press(button)
	if err != nil {
		return "", err
	}
	rd, err := s.read(post)
	if err != nil {
		return "", err
	}
	if rd.Operation == nil {
		if len(rd.Problems) == 0 {
			return "", errors.New("the page is shown again with nothing wrong")
		}
		for _, pr := range rd.Problems {
			if !slices.Contains(c.Codes, pr.Code) {
				return "", fmt.Errorf("the page says %s (%s), which is no check of functions.js", pr.Code, pr.Words)
			}
			if !slices.Contains(js.Failing, pr.Code) {
				return "", fmt.Errorf("the page stops it with %s, which functions.js passes (it fails %v)", pr.Code, js.Failing)
			}
		}
		return "stopped by the page's checks, as functions.js stops it" + hidden, nil
	}
	var body map[string]any
	must(t, decode(rd.Operation.Body, &body))
	var jsBody map[string]any
	must(t, decode(jsonOf(js), &jsBody))
	switch c.What {
	case "submit":
		if !alike(body["form"], jsBody["given"]) {
			return "", fmt.Errorf("the page sends %s, functions.js %s", jsonOf(body["form"]), jsonOf(jsBody["given"]))
		}
	case "resubmit", "edit":
		key := "form"
		if c.What == "edit" {
			key = "fields"
		}
		if !alike(body[key], jsBody["changes"]) || !alike(body["clear"], jsBody["cleared"]) {
			return "", fmt.Errorf("the page sends %s and clears %s, functions.js %s and %s", jsonOf(body[key]), jsonOf(body["clear"]), jsonOf(jsBody["changes"]), jsonOf(jsBody["cleared"]))
		}
	}
	if len(js.Failing) > 0 {
		// what the page leaves to next (a condition, a computed value held to its range): the kind
		// refuses it at the place of a check functions.js fails
		code, err := refusedAt(t, c, rd.Operation, js.Failing)
		if err != nil {
			return "", err
		}
		return "sent; functions.js stops it (" + code + "), and next refuses it at that place" + hidden, nil
	}
	return "sent as functions.js sends it" + hidden, nil
}

// refusedAt runs a post the page sends and functions.js stops through the kind, as the server does
// (start for a new request, as the requester of a line of the vectors that makes one, at its
// organization and time; next for a request made again, by its requester, and for an edit, by an
// approver who waits, at the organization and time of the line that made the ticket), and holds the
// answer to the checks functions.js fails: a refusal of form whose place (`at`) is the place of one
// of them. It answers that check.
func refusedAt(t *testing.T, c formCase, op *screens.Operation, failing []string) (string, error) {
	t.Helper()
	k := kindOf(c.Kind)
	var got json.RawMessage
	var err error
	if c.What == "submit" {
		v, by := submitLine(t, c.Kind)
		ev, e := eventOf(op, c.Kind, by, "T-1", "try-1")
		must(t, e)
		var made map[string]json.RawMessage
		must(t, json.Unmarshal(ev, &made))
		got, err = k.Start(made["submit"], v.Organization, v.Now)
	} else {
		var tk map[string]any
		must(t, decode(c.ticket, &tk))
		by := tk["requester"]
		if c.What == "edit" {
			if by = waitingApprover(tk); by == nil {
				return "", fmt.Errorf("functions.js stops it (%v), and no approver waits on the ticket to edit it", failing)
			}
		}
		ev, e := eventOf(op, c.Kind, by, "T-1", "try-2")
		must(t, e)
		v := madeAt(t, c.Kind, c.ticket)
		got, err = k.Next(c.ticket, ev, v.Organization, v.Now)
	}
	if err != nil {
		return "", fmt.Errorf("the kind takes the post as no call: %v", err)
	}
	var ans struct {
		Err *struct {
			Refused string  `json:"refused"`
			Text    string  `json:"text"`
			At      *string `json:"at"`
		} `json:"err"`
	}
	must(t, json.Unmarshal(got, &ans))
	switch {
	case ans.Err == nil:
		return "", fmt.Errorf("functions.js stops it (%v), and the kind takes it", failing)
	case ans.Err.Refused != "form" || ans.Err.At == nil:
		return "", fmt.Errorf("functions.js stops it (%v), and the kind refuses it as %q at no place of the form: %s", failing, ans.Err.Refused, ans.Err.Text)
	}
	at := *ans.Err.At
	for _, code := range failing {
		if checkAt(code, at) {
			return code, nil
		}
	}
	return "", fmt.Errorf("functions.js stops it with %v, and the kind refuses it at %q, the place of none of them: %s", failing, at, ans.Err.Text)
}

// checkAt is whether a check of functions.js is of the place a refusal of form names (`at`, as
// ringi writes it): "trip_to.required" of `trip_to`; "items.price.range" of `items[1].price`, and
// "lines.subtotal.worked" of `lines[0].subtotal` (a cell the form works out in a row); a check of a
// field of the rows, or of the rows themselves, of the rows (`items`).
func checkAt(code, at string) bool {
	steps, ok := screens.ReadPlace(at)
	if !ok || len(steps) > 2 {
		return false
	}
	place := code[:max(strings.LastIndex(code, "."), 0)]
	if len(steps) == 1 {
		return place == steps[0].Name || strings.HasPrefix(place, steps[0].Name+".")
	}
	return steps[0].Row >= 0 && place == steps[0].Name+"."+steps[1].Name
}

// submitLine is the first line of the vectors that makes a request of a kind for oneself and is
// taken: its organization and time, and its requester.
func submitLine(t *testing.T, kind string) (vector, any) {
	t.Helper()
	for _, v := range testVectors(t) {
		var e struct {
			Submit *struct {
				Requester any `json:"requester"`
				By        any `json:"by"`
			} `json:"submit"`
		}
		var a struct {
			OK json.RawMessage `json:"ok"`
		}
		if v.Kind == kind && json.Unmarshal(v.Event, &e) == nil && e.Submit != nil && e.Submit.By == nil &&
			json.Unmarshal(v.Answer, &a) == nil && a.OK != nil {
			return v, e.Submit.Requester
		}
	}
	t.Fatalf("no line of %s makes a request that is taken", kind)
	return vector{}, nil
}

// madeAt is the line of the vectors whose answer is a ticket, at the time the ticket came to be (a
// clock that rings later has not rung), or, for a ticket no answer holds, the first line given it.
func madeAt(t *testing.T, kind string, raw json.RawMessage) vector {
	t.Helper()
	var want any
	must(t, decode(raw, &want))
	var given *vector
	for _, v := range testVectors(t) {
		if v.Kind != kind {
			continue
		}
		var got any
		var ans struct {
			OK *struct {
				Ticket json.RawMessage `json:"ticket"`
			} `json:"ok"`
		}
		if json.Unmarshal(v.Answer, &ans) == nil && ans.OK != nil && decode(ans.OK.Ticket, &got) == nil && alike(got, want) {
			return v
		}
		if given == nil && len(v.Ticket) > 0 && decode(v.Ticket, &got) == nil && alike(got, want) {
			given = &v
		}
	}
	if given == nil {
		t.Fatalf("no line of %s answers the ticket or is given it", kind)
		return vector{}
	}
	return *given
}

// waitingApprover is an approver of a part of the stage a ticket is at that waits for an answer.
func waitingApprover(tk map[string]any) any {
	rev, _ := tk["revision"].(map[string]any)
	at, ok := rev["stage"].(json.Number)
	if !ok {
		return nil
	}
	i, _ := at.Int64()
	stages, _ := tk["stages"].([]any)
	if i < 0 || i >= int64(len(stages)) {
		return nil
	}
	stage, _ := stages[i].(map[string]any)
	for _, p := range asList(stage["parts"]) {
		part, _ := p.(map[string]any)
		if approvers := asList(part["approvers"]); part["state"] == "active" && len(approvers) > 0 {
			return approvers[0]
		}
	}
	return nil
}

// workedCases are the computed fields each kind's form shows, read from the page as a browser has
// them (the function, unit and format the script is told), over the form of each ticket of the
// vectors without its computed fields: the value must be the one the kind worked out on the ticket,
// shown as the server shows it.
func workedCases(t *testing.T, p *Pages) []workedCase {
	ticks, _ := testTickets(t)
	var out []workedCase
	for _, kn := range slices.Sorted(maps.Keys(p.kinds)) {
		k := p.kinds[kn]
		for li, lang := range []string{"en", "ja"} {
			pg, err := p.Form(Form{Form: screens.Form{Kind: kn, Language: lang, Surface: "worked", MaySubmit: true, Can: []string{"submit"}}, Action: "/post", Now: testNow})
			must(t, err)
			root, err := readHTML(pg.Document())
			must(t, err)
			for _, part := range root.all(func(n *node) bool { return n.class("output") && n.Attr["data-worked"] != "" }) {
				a := part.Attr
				f := k.d.fieldNamed(a["data-field"])
				num, err1 := strconv.ParseInt(a["data-num"], 10, 64)
				den, err2 := strconv.ParseInt(a["data-den"], 10, 64)
				dec, err3 := strconv.Atoi(a["data-decimals"])
				if f == nil || errors.Join(err1, err2, err3) != nil {
					t.Fatalf("%s %s: a computed field the test cannot read: %v", kn, lang, a)
				}
				u := jsUnit{Num: num, Den: den, Decimals: dec, Code: a["data-code"], Yen: a["data-yen"] == "true"}
				for i, raw := range ticks[kn] {
					if i%2 != li {
						continue // each ticket in one of the languages
					}
					var tk struct {
						Form map[string]any `json:"form"`
					}
					must(t, decode(raw, &tk))
					want, ok := steps(tk.Form[f.Name])
					if !ok {
						continue
					}
					form := map[string]any{}
					for n, v := range tk.Form {
						if g := k.d.fieldNamed(n); g != nil && !g.Computed {
							form[n] = v
						}
					}
					out = append(out, workedCase{Fn: a["data-worked"], Form: form, Unit: u, Format: a["data-format"], want: want,
						text: f.unit.shown(want, lang), about: fmt.Sprintf("%s %s (%s, ticket %d)", kn, f.Name, lang, i)})
				}
			}
		}
	}
	return out
}
