package kinddiff_test

import (
	"strings"
	"testing"

	"github.com/i2y/ringi-server/kinddiff"
)

func generatedField(v map[string]any, path string) map[string]any {
	fields := v["form"].([]any)
	var out map[string]any
	for _, name := range strings.Split(path, ".") {
		out = nil
		for _, item := range fields {
			field := item.(map[string]any)
			if field["name"] == name {
				out = field
				break
			}
		}
		if out == nil {
			return nil
		}
		if nested, ok := out["fields"].([]any); ok {
			fields = nested
		}
	}
	return out
}

func compareGeneratedFieldEdit(t *testing.T, path string, edit func(map[string]any)) *kinddiff.Diff {
	t.Helper()
	return compareGeneratedEdit(t, "expense", func(v map[string]any) {
		field := generatedField(v, path)
		if field == nil {
			t.Fatalf("no generated field %s", path)
		}
		edit(field)
	})
}

func TestFieldKeysInDetail(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		edit   func(map[string]any)
		wantEn string
		wantJa string
	}{
		{name: "written", path: "amount", edit: func(f map[string]any) { f["written"] = "goukei" }, wantEn: "name as written", wantJa: "書かれたままの名前"},
		{name: "label", path: "amount", edit: func(f map[string]any) { f["label"] = "Total" }, wantEn: "label of the field", wantJa: "ラベルが"},
		{name: "labels", path: "amount", edit: func(f map[string]any) {
			f["labels"].([]any)[0].(map[string]any)["text"] = "総額"
		}, wantEn: "labels in other languages", wantJa: "ほかの言語のラベル"},
		{name: "type", path: "amount", edit: func(f map[string]any) { f["type"] = "number" }, wantEn: "type of the field", wantJa: "型が"},
		{name: "type_written", path: "amount", edit: func(f map[string]any) { f["type_written"] = "money" }, wantEn: "type as written", wantJa: "書かれたままの型"},
		{name: "min", path: "amount", edit: func(f map[string]any) { f["min"] = 2.0 }, wantEn: "amount range", wantJa: "金額の範囲"},
		{name: "max", path: "amount", edit: func(f map[string]any) { f["max"] = 300000.0 }, wantEn: "amount range", wantJa: "金額の範囲"},
		{name: "count_min", path: "items", edit: func(f map[string]any) { f["count_min"] = 2.0 }, wantEn: "row-count limit", wantJa: "行数の制限"},
		{name: "count_max", path: "trip_to", edit: func(f map[string]any) { f["count_max"] = 80.0 }, wantEn: "length limit", wantJa: "文字数の制限"},
		{name: "choices", path: "category", edit: func(f map[string]any) {
			f["choices"].([]any)[0].(map[string]any)["label"] = "Business trip"
		}, wantEn: "choice", wantJa: "選択肢"},
		{name: "optional", path: "purpose", edit: func(f map[string]any) { f["optional"] = true }, wantEn: "becomes optional", wantJa: "なくてもよい項目"},
		{name: "computed", path: "amount", edit: func(f map[string]any) { f["computed"] = false }, wantEn: "no longer computed", wantJa: "自動では計算されなくなります"},
		{name: "reads", path: "amount", edit: func(f map[string]any) { f["reads"] = []any{"items.what"} }, wantEn: "fields read", wantJa: "読む項目"},
		{name: "worked_on_screen", path: "amount", edit: func(f map[string]any) { f["worked_on_screen"] = false }, wantEn: "no longer worked out on the screen", wantJa: "画面では自動では計算されなくなります"},
		{name: "approvers_only", path: "account", edit: func(f map[string]any) { f["approvers_only"] = false }, wantEn: "no longer one only approvers fill in", wantJa: "承認者だけが入力するものではなくなります"},
		{name: "open_in", path: "used_on", edit: func(f map[string]any) { f["open_in"] = "office" }, wantEn: "calendar of open days", wantJa: "入力できる日を数えるカレンダー"},
		{name: "shown_when", path: "trip_to", edit: func(f map[string]any) {
			f["shown_when"].(map[string]any)["condition"] = "category is purchase"
		}, wantEn: "condition that makes the field", wantJa: "見せる条件"},
		{name: "required_when", path: "receipt", edit: func(f map[string]any) {
			f["required_when"].(map[string]any)["condition"] = "amount > 50_000JPY"
		}, wantEn: "condition that makes the field", wantJa: "必須にする条件"},
		{name: "fields", path: "items.price", edit: func(f map[string]any) { f["max"] = 500000.0 }, wantEn: "amount range", wantJa: "金額の範囲"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := compareGeneratedFieldEdit(t, tc.path, tc.edit)
			en := d.Lines("en")
			ja := d.Lines("ja")
			if !hasText(en, tc.wantEn) {
				t.Errorf("English: %v", texts(en))
			}
			if !hasText(ja, tc.wantJa) {
				t.Errorf("Japanese: %v", texts(ja))
			}
		})
	}
}

func TestDateRangeWords(t *testing.T) {
	d := compareGeneratedFieldEdit(t, "used_on", func(f map[string]any) { f["min"] = "2026-02-01" })
	if !hasText(d.Lines("en"), "date range") {
		t.Errorf("English: %v", texts(d.Lines("en")))
	}
	if !hasText(d.Lines("ja"), "日付の範囲") {
		t.Errorf("Japanese: %v", texts(d.Lines("ja")))
	}
}

func TestFieldBoundsAreNotReadAsMissing(t *testing.T) {
	d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
		form := v["form"].([]any)
		form = append(form, map[string]any{
			"name": "short_note", "written": "short_note", "label": "Short note",
			"type": "text", "type_written": "text", "min": nil, "max": nil,
			"count_min": nil, "count_max": 100.0, "choices": []any{}, "optional": false,
			"computed": false, "reads": []any{}, "worked_on_screen": false,
			"approvers_only": false, "open_in": nil, "shown_when": nil, "required_when": nil,
			"fields": []any{},
		})
		v["form"] = form
	})
	en := d.Lines("en")
	ja := d.Lines("ja")
	if hasText(en, "without bounds") || hasText(ja, "制限なし") {
		t.Errorf("a bounded text field is called unbounded: %v / %v", texts(en), texts(ja))
	}
	if !hasText(en, "length limit up to 100 characters") {
		t.Errorf("English count bound: %v", texts(en))
	}
	if !hasText(ja, "文字数の制限は100文字以内") {
		t.Errorf("Japanese count bound: %v", texts(ja))
	}
}

func TestJapaneseNamesAreSpacedAsThePagesSpaceThem(t *testing.T) {
	d := comparePair(t, osDir("testdata/v1"), osDir("testdata/v2"), "leave")
	ja := d.Lines("ja")
	if !hasText(ja, "`submit_leave` に送る本文では、`form.days` の `maximum` が") {
		t.Errorf("ASCII names are not set off: %v", texts(ja))
	}
	if !hasText(ja, "が`20`から`25`に変わります") {
		t.Errorf("numbers do not stay next to the Japanese: %v", texts(ja))
	}
}
