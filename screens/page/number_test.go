package page

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/i2y/ringi-server/screens"
)

// The number types the tests read: those of the examples, and those no example has yet (rates, the
// hundredths of a currency, the quantities).
var numberTypes = []struct {
	written        string
	num, den       int64
	decimals       int
	en, ja         string // 125 steps, shown
	stepEn, stepJa string // one step, as a check says it
}{
	{"number", 1, 1, 0, "125", "125", "1", "1"},
	{"money[JPY]", 1, 1, 0, "125 JPY", "125円", "1 JPY", "1円"},
	{"money[円]", 1, 1, 0, "125 JPY", "125円", "1 JPY", "1円"},
	{"money[円, incl_tax]", 1, 1, 0, "125 JPY", "125円", "1 JPY", "1円"},
	{"money[銭]", 1, 100, 2, "1.25 JPY", "1.25円", "0.01 JPY", "0.01円"},
	{"money[USD]", 1, 1, 0, "125 USD", "125 USD", "1 USD", "1 USD"},
	{"money[USDc]", 1, 100, 2, "1.25 USD", "1.25 USD", "0.01 USD", "0.01 USD"},
	{"money[EURc]", 1, 100, 2, "1.25 EUR", "1.25 EUR", "0.01 EUR", "0.01 EUR"},
	{"rate[step 0.1%]", 1, 10, 1, "12.5%", "12.5%", "0.1%", "0.1%"},
	{"rate[step 0.25%]", 1, 4, 2, "31.25%", "31.25%", "0.25%", "0.25%"},
	{"rate[step 2.5%]", 5, 2, 1, "312.5%", "312.5%", "2.5%", "2.5%"},
	{"rate[step 1%]", 1, 1, 0, "125%", "125%", "1%", "1%"},
	{"rate[step 0.01%]", 1, 100, 2, "1.25%", "1.25%", "0.01%", "0.01%"},
	{"mass[kg]", 1, 1, 0, "125 kg", "125kg", "1 kg", "1kg"},
	{"area[坪]", 1, 1, 0, "125 坪", "125坪", "1 坪", "1坪"},
	{"duration[h]", 1, 1, 0, "125 h", "125h", "1 h", "1h"},
	{"席[席]", 1, 1, 0, "125 席", "125席", "1 席", "1席"},
}

// TestNumberTypes reads each number type into its step, and shows a value in it.
func TestNumberTypes(t *testing.T) {
	for _, c := range numberTypes {
		u, err := parseUnit(c.written)
		if err != nil {
			t.Errorf("%s: %v", c.written, err)
			continue
		}
		if u.num != c.num || u.den != c.den || u.decimals != c.decimals {
			t.Errorf("%s: a step of %d/%d with %d decimals, not %d/%d with %d", c.written, u.num, u.den, u.decimals, c.num, c.den, c.decimals)
		}
		if got := u.shown(125, "en"); got != c.en {
			t.Errorf("%s: 125 is shown %q, not %q", c.written, got, c.en)
		}
		if got := u.shown(125, "ja"); got != c.ja {
			t.Errorf("%s: 125 is shown %q in Japanese, not %q", c.written, got, c.ja)
		}
		if got := u.step("en"); got != c.stepEn {
			t.Errorf("%s: a step is %q, not %q", c.written, got, c.stepEn)
		}
		if got := u.step("ja"); got != c.stepJa {
			t.Errorf("%s: a step is %q in Japanese, not %q", c.written, got, c.stepJa)
		}
	}
	for _, bad := range []string{"rate", "rate[0.1%]", "rate[step 0%]", "rate[step x%]", "money[XYZ]", "money[XYZc]", "mass[stone]", "number[kg]", "money", "[JPY]"} {
		if _, err := parseUnit(bad); err == nil {
			t.Errorf("%s is read", bad)
		}
	}
}

// readCases are numbers typed in a unit, and what they are read as.
var readCases = []struct {
	typ   string
	typed string
	want  struct {
		n   int64
		why string
	}
}{
	{"money[JPY]", "1000", want{1000, ""}},
	{"money[JPY]", "1,000", want{1000, ""}},
	{"money[JPY]", "１，０００", want{1000, ""}},
	{"money[JPY]", "¥1,000", want{1000, ""}},
	{"money[JPY]", "1,000円", want{1000, ""}},
	{"money[JPY]", " 1000 JPY ", want{1000, ""}},
	{"money[JPY]", "1000.0", want{1000, ""}},
	{"money[JPY]", "1000.5", want{0, notStep}},
	{"money[JPY]", "-5", want{-5, ""}},
	{"money[JPY]", "－５", want{-5, ""}},
	{"money[JPY]", "+5", want{5, ""}},
	{"money[JPY]", "1,00", want{0, notNumber}},
	{"money[JPY]", "1e3", want{0, notNumber}},
	{"money[JPY]", "abc", want{0, notNumber}},
	{"money[JPY]", "12 USD", want{0, notNumber}},
	{"money[JPY]", "9007199254740991", want{9007199254740991, ""}},
	{"money[JPY]", "9007199254740992", want{0, notExact}},
	{"money[USDc]", "1.25", want{125, ""}},
	{"money[USDc]", "1.2", want{120, ""}},
	{"money[USDc]", "1", want{100, ""}},
	{"money[USDc]", "1.255", want{0, notStep}},
	{"money[USDc]", "1.25 USD", want{125, ""}},
	{"money[銭]", "1.25", want{125, ""}},
	{"money[銭]", "¥1.25", want{125, ""}},
	{"rate[step 0.1%]", "12.5", want{125, ""}},
	{"rate[step 0.1%]", "12.5%", want{125, ""}},
	{"rate[step 0.1%]", "１２．５％", want{125, ""}},
	{"rate[step 0.1%]", "12.34", want{0, notStep}},
	{"rate[step 0.1%]", "0.1", want{1, ""}},
	{"rate[step 0.25%]", "31.25", want{125, ""}},
	{"rate[step 0.25%]", "0.5", want{2, ""}},
	{"rate[step 0.25%]", "0.3", want{0, notStep}},
	{"rate[step 2.5%]", "5", want{2, ""}},
	{"rate[step 2.5%]", "312.5", want{125, ""}},
	{"rate[step 2.5%]", "1", want{0, notStep}},
	{"mass[kg]", "12kg", want{12, ""}},
	{"mass[kg]", "12 kg", want{12, ""}},
	{"number", "1,234,567", want{1234567, ""}},
	{"number", "", want{0, notNumber}},
}

type want = struct {
	n   int64
	why string
}

// TestReadNumbers reads what people type.
func TestReadNumbers(t *testing.T) {
	for _, c := range readCases {
		u, err := parseUnit(c.typ)
		if err != nil {
			t.Fatal(err)
		}
		n, why := u.read(c.typed)
		if n != c.want.n || why != c.want.why {
			t.Errorf("%s %q: %d %q, not %d %q", c.typ, c.typed, n, why, c.want.n, c.want.why)
		}
	}
}

// TestNumbersBothWays shows numbers of every type and reads them back: plain, grouped, with the unit,
// in both languages.
func TestNumbersBothWays(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, c := range numberTypes {
		u, err := parseUnit(c.written)
		if err != nil {
			t.Fatal(err)
		}
		values := []int64{0, 1, -1, 7, 99, 100, 125, 999, 1000, 9999, 10000, 123456, -123456, maxExact, -maxExact}
		for range 200 {
			values = append(values, r.Int64N(2*maxExact)-maxExact, r.Int64N(100000)-50000)
		}
		for _, n := range values {
			for _, typed := range []string{u.text(n, false), u.text(n, true), u.shown(n, "en"), u.shown(n, "ja")} {
				got, why := u.read(typed)
				if why != "" || got != n {
					t.Errorf("%s: %d is shown %q and read %d %q", c.written, n, typed, got, why)
				}
			}
		}
	}
}

// TestNumberFieldsOfADescription makes the parts of number fields no example has, from a
// description made up for the test: what each shows and what a post of it sends.
func TestNumberFieldsOfADescription(t *testing.T) {
	raw := json.RawMessage(`{"name":"rates","title":"Rates","form":[
		{"name":"rate","written":"rate","label":"Rate","type":"rate[step 0.1%]","min":0,"max":1000},
		{"name":"fee","written":"fee","label":"Fee","type":"money[USDc]","min":1,"max":null,"optional":true},
		{"name":"share","written":"share","label":"Share","type":"rate[step 0.25%]","min":null,"max":400}
	]}`)
	d, err := readDescription(raw)
	must(t, err)
	b := builder{k: &kind{name: "rates", d: d}, lang: "en"}
	for _, c := range []struct {
		f                          string
		value                      any
		shown, unit, rangeW, stepW string
	}{
		{"rate", json.Number("125"), "12.5", "%", "Enter Rate from 0.0% to 100.0%.", "Enter Rate in steps of 0.1%."},
		{"fee", json.Number("12345"), "123.45", "USD", "Enter Fee 0.01 USD or more.", "Enter Fee in steps of 0.01 USD."},
		{"share", json.Number("125"), "31.25", "%", "Enter Share 100.00% or less.", "Enter Share in steps of 0.25%."},
	} {
		f := d.fieldNamed(c.f)
		in := b.field(f, nil, "", c.value)
		if in.Value != c.shown || in.Unit != c.unit {
			t.Errorf("%s: shows %q %q, not %q %q", c.f, in.Value, in.Unit, c.shown, c.unit)
		}
		words := map[string]string{}
		for _, ch := range in.Checks {
			words[ch.key(in)] = ch.Words.String()
		}
		if words[c.f+".range"] != c.rangeW || words[c.f+".step"] != c.stepW {
			t.Errorf("%s: says %q and %q", c.f, words[c.f+".range"], words[c.f+".step"])
		}
	}
	// a type a page cannot hold exactly is refused when the pages are made
	for _, bad := range []string{`"rate"`, `"money[XYZ]"`} {
		_, err := readDescription(json.RawMessage(fmt.Sprintf(`{"name":"x","form":[{"name":"a","type":%s}]}`, bad)))
		if err == nil {
			t.Errorf("%s is read", bad)
		}
	}
	if _, err := readDescription(json.RawMessage(`{"name":"x","form":[{"name":"a","type":"number","max":9007199254740992}]}`)); err == nil {
		t.Errorf("an end past 2^53 is read")
	}
	// New refuses a set with a number type a page cannot hold exactly
	for _, bad := range []string{"rate", "money[XYZ]"} {
		set, err := screens.Load(overlay{FS: os.DirFS(testRoot), name: "types/expense.json", body: withType(t, "amount", bad)})
		must(t, err)
		if _, err := New(set, Options{Assets: "/assets/"}); !errors.Is(err, ErrDescription) {
			t.Errorf("a set whose amount is %s: %v", bad, err)
		}
	}
}

// overlay is a directory with one file's body replaced.
type overlay struct {
	fs.FS
	name string
	body []byte
}

func (o overlay) Open(name string) (fs.File, error) {
	if name == o.name {
		return fstest.MapFS{name: {Data: o.body}}.Open(name)
	}
	return o.FS.Open(name)
}

// withType is the expense description with a field of another type.
func withType(t *testing.T, field, typ string) []byte {
	t.Helper()
	raw, err := os.ReadFile(testRoot + "/types/expense.json")
	must(t, err)
	var d map[string]any
	must(t, decode(raw, &d))
	for _, f := range d["form"].([]any) {
		if m := f.(map[string]any); m["name"] == field {
			m["type"] = typ
		}
	}
	return marshal(d)
}

// TestStepsAsTheScreensShowThem holds the page's numbers to the screens' (ringi's DESIGN 13.1 to
// 13.3) on the request `steps` of testdata/steps (rates in steps of 0.25%, 2.5%, 0.1%, 0.5% and 1%,
// and money in cents and in sen, which no example has; regen.sh there makes it again). For each
// number its screens show with the catalog's formatSteps, and each AmountField its form and card ask
// with, the page's unit, read from the type the description writes, has the same decimals, step and
// unit (or currency), and the page says each check of the field as the screen does. Then
// functions.js, run in Node if there is one, shows each value with those arguments, and with the
// arguments of every other number type the page reads, as the page does, in both languages.
func TestStepsAsTheScreensShowThem(t *testing.T) {
	raw, err := os.ReadFile("testdata/steps/types/steps.json")
	must(t, err)
	d, err := readDescription(raw)
	must(t, err)
	// the field a screen reads at a path: form/<name>, or a row's column by its own name
	fieldAt := func(path string) *field {
		name := strings.TrimPrefix(strings.TrimPrefix(path, "/"), "form/")
		if f := d.fieldNamed(name); f != nil {
			return f
		}
		for _, f := range d.Form {
			for _, c := range f.Fields {
				if c.Name == name {
					return c
				}
			}
		}
		return nil
	}
	stepOf := func(u *unit) int64 {
		p := int64(1)
		for range u.decimals {
			p *= 10
		}
		return u.num * p / u.den
	}
	type stepsArgs struct {
		Decimals int    `json:"decimals"`
		Step     int64  `json:"step,omitempty"`
		Unit     string `json:"unit"`
	}
	asked := map[stepsArgs]*unit{}
	calls, fields := 0, 0
	for _, lang := range []string{"en", "ja"} {
		for _, screen := range []string{"form", "card", "list"} {
			b, err := os.ReadFile("testdata/steps/a2ui/steps/" + screen + "." + lang + ".json")
			must(t, err)
			var doc any
			must(t, decode(b, &doc))
			var walk func(x any)
			walk = func(x any) {
				switch v := x.(type) {
				case []any:
					for _, e := range v {
						walk(e)
					}
				case map[string]any:
					for _, e := range v {
						walk(e)
					}
					if v["@call"] == "formatSteps" {
						args := v["args"].(map[string]any)
						value, _ := args["value"].(map[string]any)
						path, _ := value["@path"].(string)
						f := fieldAt(path)
						if f == nil || f.unit == nil {
							if path != "" {
								t.Errorf("%s.%s: formatSteps of %s, which is no number of the description", screen, lang, path)
							}
							return
						}
						calls++
						u := f.unit
						want := stepsArgs{Decimals: u.decimals, Unit: u.withUnit("", lang)}
						if s := stepOf(u); s != 1 {
							want.Step = s
						}
						got := stepsArgs{Decimals: intOf(args["decimals"]), Unit: fmt.Sprint(args["unit"])}
						if args["step"] != nil {
							got.Step = int64(intOf(args["step"]))
						}
						if got != want {
							t.Errorf("%s.%s: %s (%s) is shown with %+v; the page's unit is %+v", screen, lang, path, u.written, got, want)
						}
						asked[want] = u
					}
					if v["component"] == "AmountField" {
						// a field of the form (`field.share`, `field.lines.cut`) or of the card's edit (`edit.share`)
						id, _ := v["id"].(string)
						name, ok := strings.CutPrefix(id, "field.")
						if !ok {
							name, _ = strings.CutPrefix(id, "edit.")
						}
						f := d.fieldNamed(name)
						if f == nil || f.unit == nil {
							t.Errorf("%s.%s: the AmountField %s is no number of the description", screen, lang, id)
							return
						}
						fields++
						u := f.unit
						// the page says what the screen's checks say, code for code
						var rows *field
						if r, _, isRow := strings.Cut(name, "."); isRow {
							rows = d.fieldNamed(r)
						}
						said := map[string]string{}
						for _, c := range fieldChecks(f, rows, lang) {
							said[c.code] = c.words.String()
						}
						checks, _ := v["@checks"].([]any)
						for _, c := range checks {
							c := c.(map[string]any)
							args, _ := c["condition"].(map[string]any)["args"].(map[string]any)
							code, _ := args["code"].(string)
							if got := said[code]; got != c["message"] {
								t.Errorf("%s.%s: %s: the page says %q, the screen %q", screen, lang, code, got, c["message"])
							}
						}
						step := int64(1)
						if v["step"] != nil {
							step = int64(intOf(v["step"]))
						}
						if intOf(v["decimals"]) != u.decimals || step != stepOf(u) {
							t.Errorf("%s.%s: %s (%s) takes %v decimals in steps of %d; the page reads %d in steps of %d", screen, lang, id, u.written, v["decimals"], step, u.decimals, stepOf(u))
						}
						if unitOf, cur := v["unit"], v["currency"]; u.money && cur != u.code || !u.money && unitOf != u.code {
							t.Errorf("%s.%s: %s (%s) has the unit %v and the currency %v", screen, lang, id, u.written, unitOf, cur)
						}
					}
				}
			}
			walk(doc)
		}
	}
	if calls == 0 || fields == 0 {
		t.Fatalf("the screens show %d numbers with formatSteps and ask %d with AmountField", calls, fields)
	}
	// and every number type the page reads, with the arguments a screen would give formatSteps
	for _, c := range numberTypes {
		u, err := parseUnit(c.written)
		must(t, err)
		for _, lang := range []string{"en", "ja"} {
			a := stepsArgs{Decimals: u.decimals, Unit: u.withUnit("", lang)}
			if s := stepOf(u); s != 1 {
				a.Step = s
			}
			if _, ok := asked[a]; !ok {
				asked[a] = u
			}
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("SKIP: node is not on PATH; functions.js's formatSteps is not held to the page's numbers")
	}
	values := []int64{0, 1, -1, 7, 99, 100, 125, 999, 1000, 9999, 10000, 99999, 123456, -123456, 1234567, maxExact, -maxExact}
	var lines []string
	type line struct {
		args stepsArgs
		n    int64
		want string
	}
	var cases []line
	for a, u := range asked {
		for _, n := range values {
			lang := "en"
			if a.Unit != u.withUnit("", "en") {
				lang = "ja"
			}
			cases = append(cases, line{a, n, u.shown(n, lang)})
			b, _ := json.Marshal(map[string]any{"value": n, "decimals": a.Decimals, "step": a.Step, "unit": a.Unit})
			lines = append(lines, string(b))
		}
	}
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "cases.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "steps.mjs"), []byte(`import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const [fnPath, casesPath] = process.argv.slice(2);
const { functions } = await import(pathToFileURL(fnPath).href);
for (const l of readFileSync(casesPath, "utf8").split("\n")) {
  if (l === "") continue;
  const a = JSON.parse(l);
  if (a.step === 0) delete a.step;
  console.log(JSON.stringify(functions.formatSteps(a)));
}
`), 0o600))
	fns, err := filepath.Abs(testRoot + "/a2ui/functions.js")
	must(t, err)
	out, err := exec.Command(node, filepath.Join(dir, "steps.mjs"), fns, filepath.Join(dir, "cases.jsonl")).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	said := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(said) != len(cases) {
		t.Fatalf("node said %d lines for %d cases", len(said), len(cases))
	}
	for i, c := range cases {
		var got string
		must(t, json.Unmarshal([]byte(said[i]), &got))
		if got != c.want {
			t.Errorf("formatSteps(%d, %+v) is %q; the page shows %q", c.n, c.args, got, c.want)
		}
	}
	t.Logf("%d numbers shown with formatSteps, %d AmountFields, %d values shown by both", calls, fields, len(cases))
}

// intOf is a JSON number decoded with its digits, as an int.
func intOf(v any) int {
	n, ok := v.(json.Number)
	if !ok {
		return -1
	}
	i, err := n.Int64()
	if err != nil {
		return -1
	}
	return int(i)
}
