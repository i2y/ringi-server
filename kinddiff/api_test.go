package kinddiff_test

import "testing"

func objectAt(v map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		next, _ := v[key].(map[string]any)
		if next == nil {
			next = map[string]any{}
			v[key] = next
		}
		v = next
	}
	return v
}

func TestAPIRequestBodyGainsAField(t *testing.T) {
	typ, api := readExamples(t, "expense")
	newAPI := editJSON(t, api, func(v map[string]any) {
		props := objectAt(v, "components", "schemas", "expense.given", "properties")
		props["note"] = map[string]any{"type": "string"}
	})
	d := comparePair(t, mapFS("expense", typ, api), mapFS("expense", typ, newAPI), "expense")
	if !d.APIChanged() {
		t.Fatalf("APIChanged is false")
	}
	lines := d.Lines("en")
	if !hasText(lines, "`form.note` is added") {
		t.Errorf("no added-field line: %v", texts(lines))
	}
	for _, line := range lines {
		if !line.API || line.Section != "api" {
			t.Errorf("a line not marked as API: %+v", line)
		}
	}
	if !hasText(d.Lines("ja"), "`form.note` が追加されます") && !hasText(d.Lines("ja"), "`form.note`が追加されます") {
		t.Errorf("no Japanese added-field line: %v", texts(d.Lines("ja")))
	}
}

func TestAPIResponseChanges(t *testing.T) {
	typ, api := readExamples(t, "expense")
	newAPI := editJSON(t, api, func(v map[string]any) {
		props := objectAt(v, "components", "schemas", "ticket", "properties")
		props["extra_answer"] = map[string]any{"type": "string"}
	})
	d := comparePair(t, mapFS("expense", typ, api), mapFS("expense", typ, newAPI), "expense")
	if !d.APIChanged() {
		t.Fatalf("APIChanged is false")
	}
	if !hasText(d.Lines("en"), "`ticket.extra_answer` is added") {
		t.Errorf("no response-field line: %v", texts(d.Lines("en")))
	}
}

func TestAPIOperationAddedAndRemoved(t *testing.T) {
	typ, api := readExamples(t, "expense")
	added := editJSON(t, api, func(v map[string]any) {
		paths := objectAt(v, "paths")
		item := map[string]any{}
		old := objectAt(v, "paths", "/requests/expense/tickets/{id}/edit", "post")
		for k, x := range old {
			item[k] = x
		}
		old["operationId"] = "edit_expense_copy"
		paths["/requests/expense/tickets/{id}/edit-copy"] = map[string]any{"post": item}
	})
	d := comparePair(t, mapFS("expense", typ, api), mapFS("expense", typ, added), "expense")
	if !hasText(d.Lines("en"), "operation `edit_expense_copy`") || !hasText(d.Lines("en"), "is added") {
		t.Errorf("no added-operation line: %v", texts(d.Lines("en")))
	}
	removed := editJSON(t, api, func(v map[string]any) {
		paths := objectAt(v, "paths")
		delete(paths, "/requests/expense/tickets/{id}/edit")
	})
	d = comparePair(t, mapFS("expense", typ, api), mapFS("expense", typ, removed), "expense")
	if !hasText(d.Lines("en"), "operation `edit_expense`") || !hasText(d.Lines("en"), "is removed") {
		t.Errorf("no removed-operation line: %v", texts(d.Lines("en")))
	}
}

func TestAPIDoesNotReportAnotherKind(t *testing.T) {
	typ, api := readExamples(t, "expense")
	newAPI := editJSON(t, api, func(v map[string]any) {
		props := objectAt(v, "components", "schemas", "purchase.given", "properties")
		props["other_kind_field"] = map[string]any{"type": "string"}
	})
	d := comparePair(t, mapFS("expense", typ, api), mapFS("expense", typ, newAPI), "expense")
	if d.APIChanged() || len(d.Lines("en")) != 0 {
		t.Errorf("another kind reported: %v", texts(d.Lines("en")))
	}
}

func TestAPIParameterAndOperationPath(t *testing.T) {
	typ, api := readExamples(t, "expense")
	newAPI := editJSON(t, api, func(v map[string]any) {
		op := objectAt(v, "paths", "/requests/expense/worked", "post")
		op["parameters"] = []any{map[string]any{"name": "verbose", "in": "query", "schema": map[string]any{"type": "boolean"}}}
	})
	d := comparePair(t, mapFS("expense", typ, api), mapFS("expense", typ, newAPI), "expense")
	if !hasText(d.Lines("en"), "adds the parameter `verbose` in query") {
		t.Errorf("no parameter line: %v", texts(d.Lines("en")))
	}
}
