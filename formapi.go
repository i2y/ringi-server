package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/i2y/ringi-server/internal/jatext"
)

// formapi.go: the check that a bundle's openapi.json agrees with the descriptions of its kinds
// (DESIGN 15.5). The description in types/<kind>.json is what ritsu writes of the kind; the
// openapi.json is what programs are told they may send. A bundle in which the two disagree — a
// field the form takes and the API refuses, or the other way — is refused at the check, whoever
// signed it, as nothing the server reads would show it.

// formAPIProblems compares the submit body of each kind of the bundle in openapi.json with the
// kind's form in types/<kind>.json, and answers a problem for each disagreement, naming the kind
// and the field. A kind whose submit the openapi does not describe, or whose description does not
// read, is one problem.
func formAPIProblems(fsys fs.FS, kinds []string) []OrgProblem {
	raw, err := fs.ReadFile(fsys, "openapi.json")
	if err != nil {
		return nil
	}
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	var out []OrgProblem
	for _, kind := range kinds {
		desc, err := fs.ReadFile(fsys, "types/"+kind+".json")
		if err != nil {
			continue
		}
		var kd struct {
			Form []map[string]any `json:"form"`
		}
		if json.Unmarshal(desc, &kd) != nil {
			continue
		}
		given := submitFormSchema(doc.Paths, doc.Components.Schemas, kind)
		if given == nil {
			out = append(out, formAPIProblem(kind, "", "the openapi.json has no submit of the kind, or its body's form is not an object schema",
				"openapi.json に種類の submit が無いか、本文のフォームがオブジェクトのスキーマでありません"))
			continue
		}
		out = append(out, compareFormSchema(kind, "", kd.Form, given, doc.Components.Schemas)...)
	}
	return out
}

// formAPIProblem is one disagreement of the openapi and the description, in both languages.
func formAPIProblem(kind, field, en, ja string) OrgProblem {
	at := "types/" + kind + ".json"
	if field != "" {
		at += " " + field
	}
	return OrgProblem{At: at,
		Text:   "The openapi.json and the description of " + kind + " disagree" + where(field) + ": " + en,
		TextJa: jatext.Join("openapi.json と ", kind, " の説明が合いません", whereJa(field), "。", ja)}
}

func where(field string) string {
	if field == "" {
		return ""
	}
	return " at the field " + field
}

func whereJa(field string) string {
	if field == "" {
		return ""
	}
	return "（項目 " + field + "）"
}

// submitFormSchema answers the schema of the form in the body of the kind's submit, resolving the
// references openapi.json makes of the components.
func submitFormSchema(paths map[string]map[string]json.RawMessage, schemas map[string]json.RawMessage, kind string) map[string]any {
	op, ok := paths["/requests/"+kind+"/tickets"]["post"]
	if !ok {
		return nil
	}
	var body struct {
		RequestBody struct {
			Content map[string]struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"content"`
		} `json:"requestBody"`
	}
	if json.Unmarshal(op, &body) != nil {
		return nil
	}
	for _, media := range body.RequestBody.Content {
		s := resolveSchema(media.Schema, schemas, 0)
		if s == nil {
			return nil
		}
		props := objectOf(s["properties"])
		if form, ok := props["form"]; ok {
			return resolveSchema(rawOf(form), schemas, 0)
		}
		return nil
	}
	return nil
}

// resolveSchema reads a schema, following the references of $ref into the components.
func resolveSchema(raw json.RawMessage, schemas map[string]json.RawMessage, depth int) map[string]any {
	if depth > 8 || len(raw) == 0 {
		return nil
	}
	var s map[string]any
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		if name == ref {
			return nil
		}
		return resolveSchema(schemas[name], schemas, depth+1)
	}
	return s
}

func objectOf(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func rawOf(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// compareFormSchema compares the fields of the form with the schema, recursively for the rows:
// each field the form takes is a property of the schema in a shape that agrees, and the schema
// has no property the form does not take. Fields computed, or filled in by approvers alone, are
// not in the submit body: they are not asked of the schema, and the schema must not take them.
func compareFormSchema(kind, prefix string, fields []map[string]any, schema map[string]any, schemas map[string]json.RawMessage) []OrgProblem {
	var out []OrgProblem
	props := objectOf(schema["properties"])
	if !objectSchema(schema) {
		return []OrgProblem{formAPIProblem(kind, prefix, "the form's schema is not an object", "フォームのスキーマがオブジェクトでありません")}
	}
	seen := map[string]bool{}
	for _, f := range fields {
		name, _ := f["name"].(string)
		if name == "" {
			continue
		}
		if computed, _ := f["computed"].(bool); computed {
			continue
		}
		if approvers, _ := f["approvers_only"].(bool); approvers {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		seen[name] = true
		prop, ok := props[name]
		if !ok {
			out = append(out, formAPIProblem(kind, path, "the form takes the field, and the schema has no property of it",
				"フォームが受ける項目が、スキーマにありません"))
			continue
		}
		out = append(out, compareFieldSchema(kind, path, f, resolveSchema(rawOf(prop), schemas, 0), schemas)...)
	}
	for name := range props {
		if !seen[name] {
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			out = append(out, formAPIProblem(kind, path, "the schema takes the property, and the form has no such field",
				"スキーマが受ける項目が、フォームにありません"))
		}
	}
	return out
}

func objectSchema(s map[string]any) bool {
	if s == nil {
		return false
	}
	if t, ok := s["type"].(string); ok {
		return t == "object"
	}
	// the given schema writes type object; a nested one may name it in one string
	return objectOf(s["properties"]) != nil
}

// typeIn reports whether the schema's type (a string or a list, as the nullable shapes write it)
// holds one of the words.
func typeIn(s map[string]any, words ...string) bool {
	switch t := s["type"].(type) {
	case string:
		for _, w := range words {
			if t == w {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if xs, ok := x.(string); ok {
				for _, w := range words {
					if xs == w {
						return true
					}
				}
			}
		}
	}
	return false
}

// schemaTypeName words the type the schema has, for the problem.
func schemaTypeName(s map[string]any) string {
	b, err := json.Marshal(s["type"])
	if err != nil {
		return "?"
	}
	return string(b)
}

// compareFieldSchema checks one field against its schema property: the type, the range and the
// length the schema can say in machine-readable words. The dates' bounds, and what is written in
// the descriptions alone, are not compared: the schema cannot say them.
func compareFieldSchema(kind, path string, f map[string]any, s map[string]any, schemas map[string]json.RawMessage) []OrgProblem {
	if s == nil {
		return []OrgProblem{formAPIProblem(kind, path, "the property's schema does not read", "項目のスキーマが読めません")}
	}
	typ, _ := f["type"].(string)
	var out []OrgProblem
	bad := func(en, ja string) {
		out = append(out, formAPIProblem(kind, path, en, ja))
	}
	category := formTypeCategory(typ)
	switch category {
	case "string":
		if !typeIn(s, "string") {
			bad(fmt.Sprintf("the field is %s, and the schema's type is %s, not string", typ, schemaTypeName(s)),
				fmt.Sprintf("項目は %s ですが、スキーマの型は %s です", typ, schemaTypeName(s)))
		}
		if typ == "date" && s["format"] != "date" {
			bad(fmt.Sprintf("the field is a date, and the schema's format is %v, not date", s["format"]),
				fmt.Sprintf("項目は日付ですが、スキーマの format は %v です", s["format"]))
		}
	case "choice":
		if !typeIn(s, "string") {
			bad(fmt.Sprintf("the field is a choice, and the schema's type is %s, not string", schemaTypeName(s)),
				fmt.Sprintf("項目は選択ですが、スキーマの型は %s です", schemaTypeName(s)))
		}
		out = append(out, compareChoiceEnum(kind, path, f, s)...)
	case "number":
		if !typeIn(s, "integer") {
			bad(fmt.Sprintf("the field is %s, and the schema's type is %s, not integer (a whole number of the unit's step)", typ, schemaTypeName(s)),
				fmt.Sprintf("項目は %s ですが、スキーマの型は %s で、整数（単位のきざみの整数倍）でありません", typ, schemaTypeName(s)))
		}
		out = append(out, compareNumberBound(kind, path, f, s, "min", "minimum")...)
		out = append(out, compareNumberBound(kind, path, f, s, "max", "maximum")...)
	case "bool":
		if !typeIn(s, "boolean") {
			bad(fmt.Sprintf("the field is bool, and the schema's type is %s, not boolean", schemaTypeName(s)),
				fmt.Sprintf("項目は真偽値ですが、スキーマの型は %s です", schemaTypeName(s)))
		}
	case "rows":
		if !typeIn(s, "array") {
			bad(fmt.Sprintf("the field is rows, and the schema's type is %s, not array", schemaTypeName(s)),
				fmt.Sprintf("項目は明細ですが、スキーマの型は %s です", schemaTypeName(s)))
		} else {
			items := resolveSchema(rawOf(s["items"]), schemas, 0)
			var sub []map[string]any
			for _, sf := range mapSlice(f["fields"]) {
				sub = append(sub, sf)
			}
			out = append(out, compareFormSchema(kind, path, sub, items, schemas)...)
		}
		out = append(out, compareCountBound(kind, path, f, s, "minItems", "maxItems")...)
	case "lengthy":
		if !typeIn(s, "string") {
			bad(fmt.Sprintf("the field is %s, and the schema's type is %s, not string", typ, schemaTypeName(s)),
				fmt.Sprintf("項目は %s ですが、スキーマの型は %s です", typ, schemaTypeName(s)))
		}
		out = append(out, compareCountBound(kind, path, f, s, "minLength", "maxLength")...)
	default:
		bad(fmt.Sprintf("the field's type %q is not one the server knows: a ritsu newer than the server wrote it, and the server is to be updated", typ),
			fmt.Sprintf("項目の型 %q はサーバーの知らない型です。サーバーより新しい ritsu が書いた型なので、サーバーを更新してください", typ))
	}
	return out
}

func mapSlice(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []map[string]any
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// answerFormError checks the form a module's answer keeps against the fields the kind's
// description declares: every key is a declared field, and every value the field's type, within
// its bounds. A null value is an empty field, and a field may be left out whole. An error is the
// server's failure, as a stopped call is: the ticket is not saved, since the store would keep what
// the description says no ticket of the kind may hold.
func answerFormError(fields []descField, form map[string]any, at string) error {
	byName := make(map[string]descField, len(fields))
	for _, f := range fields {
		byName[f.Name] = f
	}
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		f, ok := byName[key]
		if !ok {
			return fmt.Errorf("%s keeps %q, which the description does not declare", at, key)
		}
		v := form[key]
		if v == nil {
			continue
		}
		if err := answerFieldError(f, v, at+"."+key); err != nil {
			return err
		}
	}
	return nil
}

func answerFieldError(f descField, v any, at string) error {
	cat := formTypeCategory(f.Type)
	switch cat {
	case "bool":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s is not a boolean, as the description declares it", at)
		}
	case "number":
		n, ok := v.(float64)
		if !ok {
			return fmt.Errorf("%s is not a whole number of its unit, as the description declares it", at)
		}
		if n != math.Trunc(n) {
			return fmt.Errorf("%s is not a whole number of its unit, as the description declares it", at)
		}
		if lo, ok := descNumber(f.Min); ok && n < lo {
			return fmt.Errorf("%s is under the description's smallest, %v", at, lo)
		}
		if hi, ok := descNumber(f.Max); ok && n > hi {
			return fmt.Errorf("%s is over the description's largest, %v", at, hi)
		}
	case "rows":
		rows, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s is not rows, as the description declares it", at)
		}
		if n, ok := descNumber(f.CountMin); ok && float64(len(rows)) < n {
			return fmt.Errorf("%s has fewer rows than the description's smallest, %v", at, n)
		}
		if n, ok := descNumber(f.CountMax); ok && float64(len(rows)) > n {
			return fmt.Errorf("%s has more rows than the description's largest, %v", at, n)
		}
		for i, row := range rows {
			obj, ok := row.(map[string]any)
			if !ok {
				return fmt.Errorf("%s row %d is not a row of fields", at, i+1)
			}
			if err := answerFormError(f.Fields, obj, fmt.Sprintf("%s row %d", at, i+1)); err != nil {
				return err
			}
		}
	default:
		// every string the description declares: text, long text, date, the choices, person,
		// team, attachment
		str, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s is not text, as the description declares it", at)
		}
		if cat == "choice" && !strings.HasPrefix(f.Type, "choice from") && len(f.Choices) > 0 {
			in := false
			for _, c := range f.Choices {
				if c.Name == str {
					in = true
					break
				}
			}
			if !in {
				return fmt.Errorf("%s is not one of the description's choices", at)
			}
		}
		if n, ok := descNumber(f.CountMin); ok && float64(utf8.RuneCountInString(str)) < n {
			return fmt.Errorf("%s is shorter than the description's smallest, %v letters", at, n)
		}
		if n, ok := descNumber(f.CountMax); ok && float64(utf8.RuneCountInString(str)) > n {
			return fmt.Errorf("%s is longer than the description's largest, %v letters", at, n)
		}
	}
	return nil
}

// descNumber reads a bound of a field's description as a number.
func descNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// formTypeCategory is how the check reads a field's type, as ritsu writes its schema in
// openapi.json: text and long text are strings of a bounded length; the numbers, each with its
// unit in brackets (money, rate, mass, length, area, volume, duration, temperature, sound), are
// integers of a range; date is a string of format date; the choices, from a list or a master, are
// strings of an enum when the description lists the choices; attachment, person and team are
// strings; bool is boolean; rows are an array of the rows' fields. A type the server does not
// know is "".
func formTypeCategory(typ string) string {
	head := strings.TrimSpace(strings.SplitN(typ, "[", 2)[0])
	switch head {
	case "choice":
		return "choice"
	case "number", "money", "rate", "mass", "length", "area", "volume", "duration", "temperature", "sound":
		return "number"
	case "rows":
		return "rows"
	case "text", "long text":
		return "lengthy"
	case "date", "person", "attachment", "team":
		return "string"
	case "bool":
		return "bool"
	}
	if strings.HasPrefix(typ, "choice from ") {
		return "choice"
	}
	return ""
}

// compareChoiceEnum checks the schema's enum against the field's choices: exactly their names,
// and null. A choice from a master or the authority, whose choices the description does not list,
// has no enum to compare; the schema must not name one either, as the master's rows change while
// the server runs.
func compareChoiceEnum(kind, path string, f map[string]any, s map[string]any) []OrgProblem {
	var names []string
	for _, c := range mapSlice(f["choices"]) {
		if n, ok := c["name"].(string); ok {
			names = append(names, n)
		}
	}
	var enum []string
	hasNull := false
	if arr, ok := s["enum"].([]any); ok {
		for _, x := range arr {
			if x == nil {
				hasNull = true
			} else if xs, ok := x.(string); ok {
				enum = append(enum, xs)
			}
		}
	}
	if len(names) == 0 {
		if len(enum) > 0 {
			return []OrgProblem{formAPIProblem(kind, path,
				fmt.Sprintf("the form does not list the choices (they are read while the server runs), and the schema's enum is %s", strings.Join(enum, ", ")),
				fmt.Sprintf("フォームは選択肢を挙げていない（実行時に読む）のに、スキーマの enum は %s です", strings.Join(enum, ", ")))}
		}
		return nil
	}
	missing := difference(names, enum)
	extra := difference(enum, names)
	if len(missing) > 0 || len(extra) > 0 || !hasNull {
		en := fmt.Sprintf("the choices are %s, and the schema's enum is %s", strings.Join(names, ", "), strings.Join(enum, ", "))
		ja := fmt.Sprintf("選択肢は %s ですが、スキーマの enum は %s です", strings.Join(names, ", "), strings.Join(enum, ", "))
		return []OrgProblem{formAPIProblem(kind, path, en, ja)}
	}
	return nil
}

func difference(a, b []string) []string {
	var out []string
	for _, x := range a {
		found := false
		for _, y := range b {
			if x == y {
				found = true
				break
			}
		}
		if !found {
			out = append(out, x)
		}
	}
	return out
}

// compareNumberBound checks one bound of a number field against the schema's. The form's min and
// max of a number type are numbers; a bound the form does not set is not asked of the schema.
func compareNumberBound(kind, path string, f map[string]any, s map[string]any, key, word string) []OrgProblem {
	fv, has := f[key]
	fnum, isNum := fv.(float64)
	if !has || fv == nil || !isNum {
		return nil
	}
	sv, ok := s[word].(float64)
	if !ok || sv != fnum {
		en := fmt.Sprintf("the field's %s is %v, and the schema's %s is %v", key, fnum, word, s[word])
		ja := fmt.Sprintf("項目の %s は %v ですが、スキーマの %s は %v です", key, fnum, word, s[word])
		return []OrgProblem{formAPIProblem(kind, path, en, ja)}
	}
	return nil
}

// compareCountBound checks the field's count_min and count_max against the schema's words
// (minLength and maxLength for text, minItems and maxItems for rows): each is compared when the
// form sets it, and when the form sets neither the schema must not bound it either.
func compareCountBound(kind, path string, f map[string]any, s map[string]any, minWord, maxWord string) []OrgProblem {
	var out []OrgProblem
	for _, pair := range [][2]string{{"count_min", minWord}, {"count_max", maxWord}} {
		fv, has := f[pair[0]]
		fnum, isNum := fv.(float64)
		sv, sHas := s[pair[1]].(float64)
		switch {
		case has && fv != nil && isNum:
			if !sHas || sv != fnum {
				en := fmt.Sprintf("the field's %s is %v, and the schema's %s is %v", pair[0], fnum, pair[1], s[pair[1]])
				ja := fmt.Sprintf("項目の %s は %v ですが、スキーマの %s は %v です", pair[0], fnum, pair[1], s[pair[1]])
				out = append(out, formAPIProblem(kind, path, en, ja))
			}
		case sHas && (fv == nil || !has):
			en := fmt.Sprintf("the form does not bound the field, and the schema has %s %v", pair[1], sv)
			ja := fmt.Sprintf("フォームに上限が無いのに、スキーマには %s の %v があります", pair[1], sv)
			out = append(out, formAPIProblem(kind, path, en, ja))
		}
	}
	return out
}

// vectorInputProblems checks the input of each vector of the kind against the schema the bundle's
// openapi.json gives the submit's form (DESIGN 15.5): a vector written of an input the API would
// not take says nothing of what the code answers to what comes in. The ranges are not checked:
// ritsu writes edge vectors, inputs past a bound the kind's own code is to refuse. At most three
// problems of a kind are told.
func vectorInputProblems(fsys fs.FS, kind string, given *schema) []OrgProblem {
	lines, err := readLines(fsys, "vectors/"+kind+".jsonl")
	if err != nil {
		return nil
	}
	var out []OrgProblem
	bad := 0
	for _, line := range lines {
		var v struct {
			From   string          `json:"from"`
			Ticket json.RawMessage `json:"ticket"`
			Event  struct {
				Submit struct {
					Form json.RawMessage `json:"form"`
				} `json:"submit"`
			} `json:"event"`
		}
		if json.Unmarshal(line, &v) != nil {
			continue
		}
		if len(v.Ticket) > 0 && string(v.Ticket) != "null" {
			continue
		}
		if len(v.Event.Submit.Form) == 0 {
			continue
		}
		form, err := decode(v.Event.Submit.Form)
		if err != nil {
			continue
		}
		if err := given.check(form, "form", false); err != nil {
			bad++
			if len(out) < 3 {
				at := "vectors/" + kind + ".jsonl"
				if v.From != "" {
					at += " " + v.From
				}
				out = append(out, OrgProblem{At: at,
					Text:   "A vector's input does not have the shape the bundle's openapi.json gives the submit: " + err.Error(),
					TextJa: jatext.Join("ベクタの入力が、バンドルの openapi.json の submit の形と合いません（", err.Error(), "）")})
			}
		}
	}
	if bad > len(out) {
		out = append(out, OrgProblem{At: "vectors/" + kind + ".jsonl",
			Text:   fmt.Sprintf("%d vectors' inputs do not have the submit's shape in all", bad),
			TextJa: fmt.Sprintf("submit の形に合わないベクタの入力は、全部で %d 件です", bad)})
	}
	return out
}
