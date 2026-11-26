package kinddiff_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/kinddiff"
)

func compareEditedType(t *testing.T, edit func(map[string]any)) *kinddiff.Diff {
	t.Helper()
	typ, api := readExamples(t, "expense")
	newType := editJSON(t, typ, edit)
	return comparePair(t, mapFS("expense", typ, api), mapFS("expense", newType, api), "expense")
}

func formItems(v map[string]any) []any { return v["form"].([]any) }

func TestFormChanges(t *testing.T) {
	t.Run("field added", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			form := formItems(v)
			form = append(form, map[string]any{"name": "note", "written": "note", "label": "Note", "type": "string"})
			v["form"] = form
		})
		lines := d.Lines("en")
		if !hasText(lines, `field "Note" (note) is added`) {
			t.Errorf("%v", texts(lines))
		}
		if !hasText(d.Lines("ja"), "追加されました") {
			t.Errorf("Japanese: %v", texts(d.Lines("ja")))
		}
	})
	t.Run("field removed", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			form := formItems(v)
			v["form"] = append(form[:2], form[3:]...)
		})
		if !hasText(d.Lines("en"), `field "Amount" (amount) is removed`) {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("type", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { formItems(v)[2].(map[string]any)["type"] = "number" })
		if !hasText(d.Lines("en"), `type of the field "Amount" (amount) changes`) {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("range", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { formItems(v)[2].(map[string]any)["max"] = 300000.0 })
		if !hasText(d.Lines("en"), "changes from 1 through 10000000 to 1 through 300000") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("requiredness", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { formItems(v)[3].(map[string]any)["optional"] = true })
		if !hasText(d.Lines("en"), "becomes optional") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("shown condition", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			f := formItems(v)[5].(map[string]any)
			f["shown_when"] = map[string]any{"condition": "category == 'trip'", "field": "category", "op": "==", "value": "trip"}
		})
		if !hasText(d.Lines("en"), "condition that makes the field") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("field moved", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			form := formItems(v)
			form[2], form[3] = form[3], form[2]
		})
		if !hasText(d.Lines("en"), "moves from position") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
}

func TestStageChanges(t *testing.T) {
	stages := func(v map[string]any) []any { return v["stages"].([]any) }
	t.Run("added", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			s := stages(v)
			v["stages"] = append(s, map[string]any{"name": "audit", "written": "audit", "label": "Audit"})
		})
		if !hasText(d.Lines("en"), `stage "Audit" (audit) is added`) {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("removed", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { v["stages"] = stages(v)[1:] })
		if !hasText(d.Lines("en"), "precheck") || !hasText(d.Lines("en"), "is removed") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("moved", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			s := stages(v)
			s[0], s[1] = s[1], s[0]
		})
		if !hasText(d.Lines("en"), "moves from position") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("unknown rule key", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { stages(v)[1].(map[string]any)["rule"] = "manager" })
		lines := d.Lines("en")
		if !hasText(lines, "`rule` of the stage") || !hasSection(lines, "other") {
			t.Errorf("%v", texts(lines))
		}
	})
	t.Run("unknown deadline key", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { stages(v)[1].(map[string]any)["deadline"] = "3 days" })
		lines := d.Lines("en")
		if !hasText(lines, "`deadline` of the stage") || !hasSection(lines, "other") {
			t.Errorf("%v", texts(lines))
		}
	})
	t.Run("reason", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			stages(v)[1].(map[string]any)["comment_required"] = []any{"reject", "send_back"}
		})
		if !hasText(d.Lines("en"), "requires a reason for `send_back`") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
}

func TestApproverChanges(t *testing.T) {
	ways := func(v map[string]any) []any { return v["ways"].([]any) }
	t.Run("way added", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			w := ways(v)
			v["ways"] = append(w, map[string]any{"name": "owner", "written": "owner", "label": "Owner"})
		})
		if !hasSection(d.Lines("en"), "approvers") || !hasText(d.Lines("en"), `"Owner" (owner) is added`) {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("way removed", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { v["ways"] = ways(v)[1:] })
		if !hasText(d.Lines("en"), "manager") || !hasText(d.Lines("en"), "is removed") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("unknown rule key", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { ways(v)[0].(map[string]any)["rule"] = "department manager" })
		lines := d.Lines("en")
		if !hasText(lines, "`rule` of the way to choose approvers") || !hasSection(lines, "other") {
			t.Errorf("%v", texts(lines))
		}
	})
	t.Run("self approval", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { v["self_approval"] = true })
		if !hasText(d.Lines("en"), "Self approval changes") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("editable field", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { v["approvers_edit"] = []any{"purpose", "account"} })
		if !hasText(d.Lines("en"), "may now edit the field") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
}

func TestUsableChanges(t *testing.T) {
	t.Run("effective from", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { v["effective_from"] = "2026-05-01" })
		if !hasText(d.Lines("en"), "now takes effect from 2026-05-01") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("first day", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { v["usable"].(map[string]any)["from"] = "2026-05-01" })
		if !hasText(d.Lines("en"), "first day") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("last day", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { v["usable"].(map[string]any)["through"] = "2027-03-31" })
		if !hasText(d.Lines("en"), "only through 2027-03-31") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("utc offset", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) { v["utc_offset_minutes"] = 0 })
		if !hasText(d.Lines("en"), "UTC offset") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
}

func TestOtherKeys(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]any)
		want string
		omit bool
	}{
		{name: "generator comment", edit: func(v map[string]any) { v["//"] = "changed" }, want: "`//`"},
		{name: "contract", edit: func(v map[string]any) { v["contract"] = 4 }, want: "`contract`"},
		{name: "file", edit: func(v map[string]any) { v["file"] = "other.ringi" }, want: "`file`"},
		{name: "written", edit: func(v map[string]any) { v["written"] = "expense2" }, want: "`written`"},
		{name: "language", edit: func(v map[string]any) { v["language"] = "ja" }, want: "`language`"},
		{name: "title", edit: func(v map[string]any) { v["title"] = "Expense claim" }, want: "`title`"},
		{name: "labels", edit: func(v map[string]any) { v["labels"].([]any)[0].(map[string]any)["text"] = "経費申請" }, want: "`labels`"},
		{name: "description", edit: func(v map[string]any) { v["description"] = "A shorter description" }, want: "`description`"},
		{name: "owner", edit: func(v map[string]any) { v["owner"] = "finance" }, want: "`owner`"},
		{name: "organization metadata", edit: func(v map[string]any) { v["organization"].(map[string]any)["data"] = "other.json" }, want: "organization"},
		{name: "endings", edit: func(v map[string]any) { v["endings"] = append(v["endings"].([]any), "cancelled") }, want: "ending `cancelled` is added"},
		{name: "events", edit: func(v map[string]any) { v["events"] = append(v["events"].([]any), "close") }, want: "event `close` is added"},
		{name: "masters", edit: func(v map[string]any) { v["masters"].([]any)[0].(map[string]any)["key"] = "number" }, want: "master"},
		{name: "flows", edit: func(v map[string]any) { v["flows"].([]any)[0].(map[string]any)["workflow"] = "expense_precheck_v2" }, want: "flow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := compareEditedType(t, tc.edit)
			lines := d.Lines("en")
			if !hasSection(lines, "other") || !hasText(lines, tc.want) {
				t.Errorf("want %q in other lines: %v", tc.want, texts(lines))
			}
			if len(d.Lines("ja")) == 0 {
				t.Errorf("no Japanese lines")
			}
		})
	}
	t.Run("name refuses before diffing", func(t *testing.T) {
		typ, api := readExamples(t, "expense")
		other := editJSON(t, typ, func(v map[string]any) { v["name"] = "other" })
		if _, err := kinddiff.Compare(mapFS("expense", typ, api), mapFS("expense", other, api), "expense"); err == nil {
			t.Errorf("a change of name did not refuse")
		}
	})
}

func TestLineOrderIsStable(t *testing.T) {
	d := compareEditedType(t, func(v map[string]any) {
		v["version"] = 2
		v["title"] = "Expense claim"
		v["usable"].(map[string]any)["through"] = "2027-03-31"
		formItems(v)[2].(map[string]any)["max"] = 300000.0
		v["stages"].([]any)[1].(map[string]any)["deadline"] = "3 days"
		v["self_approval"] = true
	})
	order := map[string]int{"form": 0, "stages": 1, "approvers": 2, "usable": 3, "api": 4, "other": 5}
	last := -1
	for _, line := range d.Lines("en") {
		n := order[line.Section]
		if n < last {
			t.Fatalf("sections out of order: %v", texts(d.Lines("en")))
		}
		last = n
	}
	again := d.Lines("en")
	for i := range again {
		if again[i] != d.Lines("en")[i] {
			t.Fatalf("lines are not stable")
		}
	}
}

func TestJapaneseNamesUseLabels(t *testing.T) {
	d := compareEditedType(t, func(v map[string]any) { formItems(v)[2].(map[string]any)["type"] = "number" })
	if !hasText(d.Lines("ja"), "「合計」（amount）") {
		t.Errorf("%v", texts(d.Lines("ja")))
	}
}

func TestVersionTwoExample(t *testing.T) {
	oldDir := osDir(filepath.Join("testdata", "v1"))
	newDir := osDir(filepath.Join("testdata", "v2"))
	d := comparePair(t, oldDir, newDir, "leave")
	if !d.APIChanged() {
		t.Fatalf("the wider range of days is an API change")
	}
	en := d.Lines("en")
	for _, want := range []string{
		`range of the field "Business days" (days) changes from 1 through 20 to 1 through 25`,
		"first day the kind can be used changes from 2026-04-01 to 2027-04-01",
		"`maximum` of `form.days` changes from `20` to `25`",
	} {
		if !hasText(en, want) {
			t.Errorf("want %q in %v", want, texts(en))
		}
	}
	if len(d.Lines("ja")) != len(en) {
		t.Errorf("English and Japanese line counts differ")
	}
}

func TestApproverKeysInDetail(t *testing.T) {
	t.Run("submitted_by groups and except", func(t *testing.T) {
		typ, api := readExamples(t, "leave")
		newType := editJSON(t, typ, func(v map[string]any) {
			sb := v["submitted_by"].(map[string]any)
			sb["except"].([]any)[0].(map[string]any)["team"] = "executive"
		})
		d := comparePair(t, mapFS("leave", typ, api), mapFS("leave", newType, api), "leave")
		lines := d.Lines("en")
		if !hasSection(lines, "approvers") || !hasText(lines, "people left out") || !hasText(lines, "executive") {
			t.Errorf("%v", texts(lines))
		}
	})
	t.Run("positions", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			v["organization"].(map[string]any)["positions"].([]any)[1].(map[string]any)["label"] = "Team manager"
		})
		lines := d.Lines("en")
		if !hasSection(lines, "approvers") || !hasText(lines, "label of the position") {
			t.Errorf("%v", texts(lines))
		}
	})
	t.Run("team kinds", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			kinds := v["organization"].(map[string]any)["team_kinds"].([]any)
			v["organization"].(map[string]any)["team_kinds"] = append(kinds, map[string]any{"name": "project", "written": "project", "label": "Project", "labels": []any{}})
		})
		lines := d.Lines("en")
		if !hasSection(lines, "approvers") || !hasText(lines, `team kind "Project" (project) is added`) {
			t.Errorf("%v", texts(lines))
		}
	})
	t.Run("needs", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			needs := v["organization"].(map[string]any)["needs"].(map[string]any)
			needs["people"] = []any{"e0001"}
			needs["teams"] = []any{"accounting"}
			needs["members_of"] = append(needs["members_of"].([]any), "executive")
		})
		lines := d.Lines("en")
		for _, want := range []string{"`e0001` is added to the people", "`accounting` is added to the teams", "`executive` is added to the teams whose members"} {
			if !hasText(lines, want) {
				t.Errorf("want %q in %v", want, texts(lines))
			}
		}
		if !hasSection(lines, "approvers") {
			t.Errorf("no approvers section: %v", texts(lines))
		}
	})
	t.Run("way metadata", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			way := v["ways"].([]any)[0].(map[string]any)
			way["label"] = "Line manager"
			way["written"] = "manager2"
			way["labels"].([]any)[0].(map[string]any)["text"] = "直属の上長"
		})
		lines := d.Lines("en")
		for _, want := range []string{"label of the way to choose approvers", "labels in other languages of the way to choose approvers", "name as written of the way to choose approvers"} {
			if !hasText(lines, want) {
				t.Errorf("want %q in %v", want, texts(lines))
			}
		}
	})
	t.Run("stage metadata", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			stage := v["stages"].([]any)[0].(map[string]any)
			stage["label"] = "Preliminary check"
			stage["written"] = "precheck2"
			stage["labels"].([]any)[0].(map[string]any)["text"] = "事前確認"
		})
		lines := d.Lines("en")
		for _, want := range []string{"label of the stage", "labels in other languages of the stage", "name as written of the stage"} {
			if !hasText(lines, want) {
				t.Errorf("want %q in %v", want, texts(lines))
			}
		}
	})
	t.Run("submitted_by is not other", func(t *testing.T) {
		d := compareEditedType(t, func(v map[string]any) {
			v["submitted_by"] = map[string]any{"groups": []any{map[string]any{"kind": "everyone"}}, "except": []any{}}
		})
		for _, line := range d.Lines("en") {
			if line.Section == "other" && strings.Contains(line.Text, "restriction on who may submit") {
				t.Errorf("submitted_by reported in other: %v", texts(d.Lines("en")))
			}
		}
		if !hasSection(d.Lines("en"), "approvers") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
}
