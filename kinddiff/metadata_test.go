package kinddiff_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/kinddiff"
)

func readGenerated(t *testing.T, version, kind string) ([]byte, []byte) {
	t.Helper()
	typ, err := os.ReadFile(filepath.Join("testdata", version, "types", kind+".json"))
	if err != nil {
		t.Fatal(err)
	}
	api, err := os.ReadFile(filepath.Join("testdata", version, "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	return typ, api
}

func compareGeneratedEdit(t *testing.T, kind string, edit func(map[string]any)) *kinddiff.Diff {
	t.Helper()
	return compareGeneratedEdits(t, kind, nil, edit)
}

func compareGeneratedEdits(t *testing.T, kind string, oldEdit, newEdit func(map[string]any)) *kinddiff.Diff {
	t.Helper()
	typ, api := readGenerated(t, "v1", kind)
	oldType := typ
	if oldEdit != nil {
		oldType = editJSON(t, typ, oldEdit)
	}
	newType := editJSON(t, typ, newEdit)
	return comparePair(t, mapFS(kind, oldType, api), mapFS(kind, newType, api), kind)
}

func generatedStages(v map[string]any) []any { return v["stages"].([]any) }

func generatedCases(v map[string]any, stage int) []any {
	return generatedStages(v)[stage].(map[string]any)["approvers"].([]any)
}

func generatedDeadlines(v map[string]any, stage int) []any {
	return generatedStages(v)[stage].(map[string]any)["deadlines"].([]any)
}

func countText(lines []kinddiff.Line, want string) int {
	n := 0
	for _, line := range lines {
		if strings.Contains(line.Text, want) {
			n++
		}
	}
	return n
}

func TestGeneratedMetadataFixture(t *testing.T) {
	typ, api := readGenerated(t, "v1", "expense")
	d := comparePair(t, mapFS("expense", typ, api), mapFS("expense", typ, api), "expense")
	if len(d.Lines("en")) != 0 {
		t.Fatalf("an unchanged generated description has lines: %v", texts(d.Lines("en")))
	}
	var decoded map[string]any
	decoded = mustDecode(t, typ)
	stage := decoded["stages"].([]any)[0].(map[string]any)
	if _, ok := stage["approvers"]; !ok {
		t.Fatalf("the generated fixture has no stage approvers")
	}
	if _, ok := stage["deadlines"]; !ok {
		t.Fatalf("the generated fixture has no stage deadlines")
	}
	if _, ok := decoded["ways"].([]any)[0].(map[string]any)["how"]; !ok {
		t.Fatalf("the generated fixture has no way how")
	}
	if len(decoded["late_tables"].([]any)) == 0 {
		t.Fatalf("the expense fixture has no late table")
	}
}

func mustDecode(t *testing.T, typ []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(typ, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestGeneratedLeaveVersionsHaveNoMetadataDiff(t *testing.T) {
	d := comparePair(t, osDir(filepath.Join("testdata", "v1")), osDir(filepath.Join("testdata", "v2")), "leave")
	for _, line := range d.Lines("en") {
		if strings.Contains(line.Text, "deadline line") || strings.Contains(line.Text, "case ") || strings.Contains(line.Text, "whom it chooses") {
			t.Errorf("unchanged approver or deadline metadata reported: %v", texts(d.Lines("en")))
		}
	}
}

func TestApproverCaseChanges(t *testing.T) {
	firstWhen := "when “Category” is Expenses, and “Amount” is 100,000 JPY or less"
	firstWhenJa := "「種別」が経費、かつ「合計」が100,000円以下のとき"
	t.Run("added", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			cases := generatedCases(v, 0)
			newCase := map[string]any{
				"when":  map[string]any{"en": "when “Category” is Travel", "ja": "「種別」が出張のとき"},
				"who":   map[string]any{"en": "check", "ja": "確かめる人"},
				"ways":  []any{"check"},
				"kind":  "who",
				"route": nil,
			}
			generatedStages(v)[0].(map[string]any)["approvers"] = append(cases, newCase)
		})
		lines := d.Lines("en")
		if !hasSection(lines, "approvers") || !hasText(lines, "is added") || !hasText(lines, "when “Category” is Travel") || !hasText(lines, "kind `who`") {
			t.Errorf("%v", texts(lines))
		}
		if !hasText(d.Lines("ja"), "「種別」が出張のとき") || !hasText(d.Lines("ja"), "事前の確かめ") {
			t.Errorf("Japanese: %v", texts(d.Lines("ja")))
		}
	})
	t.Run("removed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			cases := generatedCases(v, 0)
			generatedStages(v)[0].(map[string]any)["approvers"] = cases[1:]
		})
		lines := d.Lines("en")
		if !hasText(lines, "is removed") || !hasText(lines, firstWhen) {
			t.Errorf("%v", texts(lines))
		}
		if !hasText(d.Lines("ja"), firstWhenJa) {
			t.Errorf("Japanese: %v", texts(d.Lines("ja")))
		}
	})
	t.Run("moved", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			cases := generatedCases(v, 0)
			cases[0], cases[1] = cases[1], cases[0]
		})
		if !hasText(d.Lines("en"), "moves in the route") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("when changed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			one := generatedCases(v, 0)[0].(map[string]any)
			one["when"] = map[string]any{"en": "when “Category” is Expenses", "ja": "「種別」が経費のとき"}
		})
		lines := d.Lines("en")
		if !hasText(lines, "the condition under which it comes") || !hasText(lines, firstWhen) || !hasText(lines, "when “Category” is Expenses") {
			t.Errorf("%v", texts(lines))
		}
		if !hasText(d.Lines("ja"), "来るとき") || !hasText(d.Lines("ja"), "「種別」が経費のとき") {
			t.Errorf("Japanese: %v", texts(d.Lines("ja")))
		}
	})
	t.Run("who changed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			one := generatedCases(v, 0)[0].(map[string]any)
			one["who"] = map[string]any{"en": "precheck owner", "ja": "事前確認の持ち主"}
		})
		lines := d.Lines("en")
		if !hasText(lines, "who answers") || !hasText(lines, "precheck owner") {
			t.Errorf("%v", texts(lines))
		}
		if !hasText(d.Lines("ja"), "答える人") || !hasText(d.Lines("ja"), "事前確認の持ち主") {
			t.Errorf("Japanese: %v", texts(d.Lines("ja")))
		}
	})
	t.Run("ways changed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			one := generatedCases(v, 0)[0].(map[string]any)
			one["ways"] = []any{"check", "manager"}
		})
		lines := d.Lines("en")
		if !hasText(lines, "the set of ways it names") || !hasText(lines, "`check`, `manager`") {
			t.Errorf("%v", texts(lines))
		}
	})
	t.Run("ways reordered", func(t *testing.T) {
		d := compareGeneratedEdits(t, "expense", func(v map[string]any) {
			generatedCases(v, 0)[0].(map[string]any)["ways"] = []any{"check", "manager"}
		}, func(v map[string]any) {
			generatedCases(v, 0)[0].(map[string]any)["ways"] = []any{"manager", "check"}
		})
		if len(d.Lines("en")) != 0 {
			t.Errorf("a reorder of the same set of ways was reported: %v", texts(d.Lines("en")))
		}
	})
	t.Run("kind changed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			one := generatedCases(v, 0)[0].(map[string]any)
			one["kind"] = "note"
		})
		lines := d.Lines("en")
		if !hasText(lines, "the kind of the case") || !hasText(lines, "`who`") || !hasText(lines, "`note`") {
			t.Errorf("%v", texts(lines))
		}
	})
	t.Run("route changed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			one := generatedCases(v, 0)[0].(map[string]any)
			one["route"] = "manager_approval"
		})
		lines := d.Lines("en")
		if !hasText(lines, "the shared route it belongs to") || !hasText(lines, "`manager_approval`") {
			t.Errorf("%v", texts(lines))
		}
	})
}

func TestDeadlineMetadataChanges(t *testing.T) {
	t.Run("line added", func(t *testing.T) {
		d := compareGeneratedEdits(t, "expense", func(v map[string]any) {
			lines := generatedDeadlines(v, 0)
			generatedStages(v)[0].(map[string]any)["deadlines"] = append(lines[:1], lines[2:]...)
		}, func(v map[string]any) {})
		if !hasText(d.Lines("en"), "gains the deadline line `remind`") || !hasText(d.Lines("en"), "Its approvers are reminded") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("line removed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			lines := generatedDeadlines(v, 0)
			generatedStages(v)[0].(map[string]any)["deadlines"] = append(lines[:1], lines[2:]...)
		})
		if !hasText(d.Lines("en"), "loses the deadline line `remind`") || !hasText(d.Lines("en"), "Its approvers are reminded") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("days changed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			line := generatedDeadlines(v, 0)[0].(map[string]any)
			line["days"] = 4.0
		})
		lines := d.Lines("en")
		if !hasText(lines, "number of days") || !hasText(lines, "from `3` to `4`") {
			t.Errorf("%v", texts(lines))
		}
		if !hasText(d.Lines("ja"), "日数") {
			t.Errorf("Japanese: %v", texts(d.Lines("ja")))
		}
	})
	t.Run("calendar changed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			line := generatedDeadlines(v, 0)[0].(map[string]any)
			line["calendar"] = "office"
		})
		lines := d.Lines("en")
		if !hasText(lines, "the calendar") || !hasText(lines, "`office`") {
			t.Errorf("%v", texts(lines))
		}
	})
	t.Run("late behavior changed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			line := generatedDeadlines(v, 0)[2].(map[string]any)
			line["written"] = "when late stay"
			line["en"] = "When it is late, it waits on."
			line["ja"] = "期限を過ぎても、待ち続けます。"
		})
		lines := d.Lines("en")
		if !hasText(lines, "what it does when late") || !hasText(lines, "When it is late, it waits on.") {
			t.Errorf("%v", texts(lines))
		}
		if !hasText(d.Lines("ja"), "期限を過ぎても、待ち続けます。") {
			t.Errorf("Japanese: %v", texts(d.Lines("ja")))
		}
	})
	t.Run("sentence changed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			line := generatedDeadlines(v, 0)[0].(map[string]any)
			line["en"] = "It is due two business days after it starts."
		})
		if !hasText(d.Lines("en"), "the English sentence") || !hasText(d.Lines("en"), "two business days") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
}

func TestWayHowChanges(t *testing.T) {
	d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
		way := v["ways"].([]any)[0].(map[string]any)
		way["how"] = map[string]any{"en": "the requester's division head", "ja": "申請者の本部長"}
	})
	lines := d.Lines("en")
	if !hasSection(lines, "approvers") || !hasText(lines, "changes whom it chooses") || !hasText(lines, "the requester's division head") {
		t.Errorf("%v", texts(lines))
	}
	if !hasText(d.Lines("ja"), "申請者の本部長") {
		t.Errorf("Japanese: %v", texts(d.Lines("ja")))
	}
}

func TestLateTableChanges(t *testing.T) {
	t.Run("row changed once", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			table := v["late_tables"].([]any)[0].(map[string]any)
			table["en"].(map[string]any)["rows"].([]any)[2].([]any)[1] = "division_head"
			table["ja"].(map[string]any)["rows"].([]any)[2].([]any)[1] = "本部長"
		})
		lines := d.Lines("en")
		if countText(lines, "Row 3 of the late table") != 1 || !hasText(lines, "manager | division_head") {
			t.Errorf("%v", texts(lines))
		}
		if !hasText(d.Lines("ja"), "上長 | 本部長") {
			t.Errorf("Japanese: %v", texts(d.Lines("ja")))
		}
	})
	t.Run("header changed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			table := v["late_tables"].([]any)[0].(map[string]any)
			table["en"].(map[string]any)["head"].([]any)[0] = "`way`"
			table["ja"].(map[string]any)["head"].([]any)[0] = "決め方"
		})
		lines := d.Lines("en")
		if countText(lines, "head of column 1") != 1 || !hasText(lines, "`way`") {
			t.Errorf("%v", texts(lines))
		}
	})
	t.Run("row removed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			table := v["late_tables"].([]any)[0].(map[string]any)
			for _, lang := range []string{"en", "ja"} {
				rows := table[lang].(map[string]any)["rows"].([]any)
				table[lang].(map[string]any)["rows"] = rows[1:]
			}
		})
		if !hasText(d.Lines("en"), "loses row") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("row added", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			table := v["late_tables"].([]any)[0].(map[string]any)
			table["en"].(map[string]any)["rows"] = append(table["en"].(map[string]any)["rows"].([]any), []any{"auditor", "board"})
			table["ja"].(map[string]any)["rows"] = append(table["ja"].(map[string]any)["rows"].([]any), []any{"監査", "取締役会"})
		})
		lines := d.Lines("en")
		if countText(lines, "gains row") != 1 || !hasText(lines, "auditor | board") {
			t.Errorf("%v", texts(lines))
		}
	})
	t.Run("rows reordered", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			table := v["late_tables"].([]any)[0].(map[string]any)
			for _, lang := range []string{"en", "ja"} {
				rows := table[lang].(map[string]any)["rows"].([]any)
				rows[0], rows[1] = rows[1], rows[0]
			}
		})
		if !hasText(d.Lines("en"), "order of the rows") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("table added", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) {
			tables := v["late_tables"].([]any)
			first := tables[0].(map[string]any)
			copyOfFirst := map[string]any{
				"name": "board_up",
				"en":   first["en"],
				"ja":   first["ja"],
			}
			v["late_tables"] = append(tables, copyOfFirst)
		})
		if !hasText(d.Lines("en"), "late table `board_up` is added") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
	t.Run("table removed", func(t *testing.T) {
		d := compareGeneratedEdit(t, "expense", func(v map[string]any) { v["late_tables"] = []any{} })
		if !hasText(d.Lines("en"), "late table `next_up` is removed") {
			t.Errorf("%v", texts(d.Lines("en")))
		}
	})
}

func TestIntroducedLateTableCompatibility(t *testing.T) {
	d := compareGeneratedEdits(t, "expense", func(v map[string]any) {
		delete(v, "late_tables")
	}, func(v map[string]any) {})
	en := d.Lines("en")
	if countText(en, "does not say the tables by which a late stage goes up") != 1 {
		t.Errorf("late-table compatibility line count: %v", texts(en))
	}
	if hasText(en, "late table `next_up` is added") {
		t.Errorf("a table the old description did not expose was reported as added: %v", texts(en))
	}
	if countText(en, "late table `next_up` is added") != 0 {
		t.Errorf("the table was reported as an addition: %v", texts(en))
	}
}

func TestIntroducedMetadataCompatibility(t *testing.T) {
	// testdata/old: version 1 as a ritsu before the approvers and the deadlines wrote it
	d := comparePair(t, osDir(filepath.Join("testdata", "old")), osDir(filepath.Join("testdata", "v2")), "leave")
	en := d.Lines("en")
	if countText(en, "The old version's description does not say how its approvers are chosen") != 1 {
		t.Errorf("approver compatibility line count: %v", texts(en))
	}
	for _, line := range en {
		if line.Section == "approvers" && strings.Contains(line.Text, "is added.") {
			t.Errorf("an old description's missing cases were reported as added: %v", texts(en))
		}
	}
	if !hasText(en, "when “Business days” is more than 5") || !hasText(en, "route `manager_approval`") {
		t.Errorf("new cases are not listed: %v", texts(en))
	}
	if countText(en, "does not say the deadlines of its stages") != 1 {
		t.Errorf("deadline compatibility lines: %v", texts(en))
	}
	if countText(en, "does not say whom its ways choose") != 0 {
		t.Errorf("way exposure has a second approver explanation: %v", texts(en))
	}
	ja := d.Lines("ja")
	if countText(ja, "古い版の説明には承認者の決め方が書かれていないので") != 1 || !hasText(ja, "「営業日の日数」が5より大きいとき") {
		t.Errorf("Japanese compatibility lines: %v", texts(ja))
	}
}
