package page

import (
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
)

// maxExact is the largest integer a browser holds exactly (2^53 − 1): a number past it is out of
// range on every screen (ringi's DESIGN 13.3).
const maxExact = 1<<53 - 1

// unit is what one integer on the wire counts, and how a person reads and types it: the integer n
// is the amount n × num / den of the unit people count in (code). A step that is a whole unit has
// num = den = 1.
type unit struct {
	written  string // the type as the description writes it: "money[JPY]", "rate[step 0.1%]"
	num, den int64  // one step, in the unit people count in, in lowest terms
	decimals int    // the digits after the point that show any number of steps exactly
	code     string // the unit people count in: "JPY", "USD", "%", "kg"; "" for a number
	money    bool
	rate     bool
}

// The currencies of ritsu's closed table of units (ritsu's DESIGN 5.1) besides 円, as ISO 4217
// codes; each has a hundredth, the code with c (USD and USDc).
var currencies = []string{
	"USD", "EUR", "GBP", "CHF", "CAD", "AUD", "NZD", "CNY", "HKD", "SGD", "KRW", "INR",
	"TWD", "THB", "SEK", "NOK", "DKK", "PLN", "CZK", "HUF", "TRY", "BRL", "MXN", "ZAR",
	"AED", "SAR", "ILS", "PHP", "IDR", "MYR", "VND",
}

// The units of ritsu's table that are not money, by the word their type starts with.
var quantities = map[string][]string{
	"mass":        {"mg", "g", "kg", "t", "oz", "lb"},
	"length":      {"mm", "cm", "m", "km", "in", "ft", "yd", "mi"},
	"area":        {"mm2", "cm2", "m2", "a", "ha", "km2", "坪", "in2", "ft2", "yd2", "mi2", "ac"},
	"volume":      {"mm3", "cm3", "mL", "L", "m3", "kL"},
	"duration":    {"ms", "s", "min", "h", "d", "w"},
	"temperature": {"℃", "℉"},
	"sound":       {"dB"},
}

var (
	typeShape = regexp.MustCompile(`^([^\[\]]+)\[([^\]]*)\]$`)
	stepShape = regexp.MustCompile(`^step\s*([0-9]+(?:\.[0-9]+)?)\s*%$`)
)

// parseUnit reads a number type as ritsu writes it (`number`, `money[JPY]`, `money[円, incl_tax]`,
// `money[USDc]`, `rate[step 0.1%]`, `mass[kg]`).
func parseUnit(t string) (*unit, error) {
	u := &unit{written: t, num: 1, den: 1}
	if t == "number" {
		return u, nil
	}
	if t == "rate" {
		return nil, fmt.Errorf("the type %q has no step, so a page cannot say what its integer counts", t)
	}
	m := typeShape.FindStringSubmatch(t)
	if m == nil {
		return nil, fmt.Errorf("the type %q is no type this package reads", t)
	}
	word, inner := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	switch word {
	case "money":
		cur, _, _ := strings.Cut(inner, ",")
		cur = strings.TrimSpace(cur)
		u.money = true
		switch {
		case cur == "円" || cur == "JPY":
			u.code = "JPY"
		case cur == "銭":
			u.code, u.den = "JPY", 100
		case slices.Contains(currencies, cur):
			u.code = cur
		case strings.HasSuffix(cur, "c") && slices.Contains(currencies, strings.TrimSuffix(cur, "c")):
			u.code, u.den = strings.TrimSuffix(cur, "c"), 100
		default:
			return nil, fmt.Errorf("the type %q names %q, which is no currency of the table of units", t, cur)
		}
	case "rate":
		s := stepShape.FindStringSubmatch(inner)
		if s == nil {
			return nil, fmt.Errorf("the type %q is no rate with a step (`rate[step 0.1%%]`)", t)
		}
		r, ok := new(big.Rat).SetString(s[1])
		if !ok || r.Sign() <= 0 {
			return nil, fmt.Errorf("the type %q has the step %s%%", t, s[1])
		}
		if !r.Num().IsInt64() || !r.Denom().IsInt64() {
			return nil, fmt.Errorf("the type %q has a step too fine to read", t)
		}
		u.rate, u.code, u.num, u.den = true, "%", r.Num().Int64(), r.Denom().Int64()
	default:
		units, ok := quantities[word]
		switch {
		case ok && slices.Contains(units, inner):
			u.code = inner
		case !ok && word == inner && inner != "":
			u.code = inner // a count of things with a name (`席[席]`)
		default:
			return nil, fmt.Errorf("the type %q is no unit of the table of units", t)
		}
	}
	// the digits after the point that show one step exactly: den is 2^a × 5^b (a step is written
	// as a decimal), and 10^max(a, b) steps are a whole number of units
	a, b, rest := 0, 0, u.den
	for rest%2 == 0 {
		rest /= 2
		a++
	}
	for rest%5 == 0 {
		rest /= 5
		b++
	}
	if rest != 1 {
		return nil, fmt.Errorf("the type %q has a step with no end of digits", t)
	}
	u.decimals = max(a, b)
	return u, nil
}

// text writes n steps as a person reads them: digits (grouped by thousands when grouped), a point
// and the unit's decimals, without the unit.
func (u *unit) text(n int64, grouped bool) string {
	// n × num / den, times 10^decimals, is a whole number
	v := new(big.Int).Mul(big.NewInt(n), big.NewInt(u.num))
	v.Mul(v, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(u.decimals)), nil))
	v.Quo(v, big.NewInt(u.den))
	neg := v.Sign() < 0
	digits := new(big.Int).Abs(v).String()
	if len(digits) <= u.decimals {
		digits = strings.Repeat("0", u.decimals-len(digits)+1) + digits
	}
	whole, frac := digits[:len(digits)-u.decimals], digits[len(digits)-u.decimals:]
	if grouped {
		whole = group(whole)
	}
	out := whole
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// group puts a comma between each three digits from the right, in a number of five digits or more
// (ritsu's words: `2000`, `20,000`), as the A2UI screens write a number.
func group(digits string) string {
	if len(digits) <= 4 {
		return digits
	}
	var b strings.Builder
	head := len(digits) % 3
	if head > 0 {
		b.WriteString(digits[:head])
	}
	for i := head; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// shown is n steps with the unit, as a page shows a value: `1,000 JPY` and `1,000円`, `12.5%`,
// `12 kg` and `12kg` (ringi's screens write a number so).
func (u *unit) shown(n int64, lang string) string {
	return u.withUnit(u.text(n, true), lang)
}

// withUnit is digits with the unit after them, in lang.
func (u *unit) withUnit(digits, lang string) string {
	switch {
	case u.code == "":
		return digits
	case u.rate:
		return digits + "%"
	case u.money && u.code == "JPY" && lang == "ja":
		return digits + "円"
	case u.money || lang != "ja":
		return digits + " " + u.code
	}
	return digits + u.code
}

// unitWord is the unit as a page writes it after an input: `JPY` (`円` in Japanese), `%`, `kg`.
func (u *unit) unitWord(lang string) string {
	if u.money && u.code == "JPY" && lang == "ja" {
		return "円"
	}
	return u.code
}

// Why a typed number is not read.
const (
	notNumber = "number" // not a number
	notStep   = "step"   // not a whole number of steps
	notExact  = "range"  // a number past what a page holds exactly
)

var amountShape = regexp.MustCompile(`^([+-]?)([0-9]{1,3}(?:,[0-9]{3})+|[0-9]+)(?:\.([0-9]+))?$`)

// read reads a number as a person types it: digits with a sign, a point and commas between each
// three digits (full-width ones too), and the unit after it as the page writes it (or `円` and `¥`
// for yen). It answers the integer of steps, exactly; or why it is not one.
func (u *unit) read(typed string) (int64, string) {
	t := trim(halfWidth(typed))
	if u.money && u.code == "JPY" {
		t = trim(strings.TrimPrefix(t, "¥"))
		t = trim(strings.TrimSuffix(t, "円"))
	}
	if u.code != "" {
		t = trim(strings.TrimSuffix(t, u.code))
	}
	m := amountShape.FindStringSubmatch(t)
	if m == nil {
		return 0, notNumber
	}
	whole := strings.ReplaceAll(m[2], ",", "")
	frac := m[3]
	// the typed amount is digits / 10^len(frac); in steps, × den / num
	v, _ := new(big.Int).SetString(whole+frac, 10)
	if m[1] == "-" {
		v.Neg(v)
	}
	v.Mul(v, big.NewInt(u.den))
	div := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(len(frac))), nil)
	div.Mul(div, big.NewInt(u.num))
	q, r := new(big.Int).QuoRem(v, div, new(big.Int))
	if r.Sign() != 0 {
		return 0, notStep
	}
	if !q.IsInt64() || q.Int64() > maxExact || q.Int64() < -maxExact {
		return 0, notExact
	}
	return q.Int64(), ""
}

// halfWidth writes full-width digits and signs (what a Japanese input method types) as ASCII.
func halfWidth(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '０' && r <= '９':
			return '0' + (r - '０')
		case r == '．':
			return '.'
		case r == '，':
			return ','
		case r == '－', r == '−', r == '‐':
			return '-'
		case r == '＋':
			return '+'
		case r == '％':
			return '%'
		case r == '￥':
			return '¥'
		case r == '　':
			return ' '
		}
		return r
	}, s)
}

// step is one step as a page writes it, with the unit: `0.1%`, `0.01 USD`.
func (u *unit) step(lang string) string {
	return u.withUnit(u.text(1, false), lang)
}
