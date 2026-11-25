package conformance

import (
	"encoding/json"
	"testing"
)

// TestStageAgain checks that the suite reads a ticket at a stage that waited for a later one at the
// stage it is at: the list has it twice under one name, the first time as it was when it waited
// (ringi's DESIGN 5.12).
func TestStageAgain(t *testing.T) {
	ticket := json.RawMessage(`{"id":"T-1","stage":"manager","revision":{"round":1,"stage":2,"edits":0},"stages":[
		{"name":"manager","phase":"deferred","started":1,"reminded":null,"parts":[{"state":"deferred","approvers":["ann"],"answers":[]}]},
		{"name":"security","phase":"answered","started":2,"reminded":false,"parts":[]},
		{"name":"manager","phase":"active","started":3,"reminded":true,"parts":[{"state":"active","approvers":["ben"],"answers":[]}]}]}`)
	out, err := startedAt(ticket, 99)
	if err != nil {
		t.Fatal(err)
	}
	var tk struct {
		Stages []struct {
			Started int `json:"started"`
		} `json:"stages"`
	}
	json.Unmarshal(out, &tk)
	if tk.Stages[0].Started != 1 || tk.Stages[2].Started != 99 {
		t.Errorf("started: %s", out)
	}
	if yes, ok := remindedOf(ticket, "manager"); !yes || !ok {
		t.Errorf("reminded: %v, %v", yes, ok)
	}
	if who := waitsFor(ticket, "manager"); len(who) != 1 || who[0] != "ben" {
		t.Errorf("waits for %v", who)
	}
	none := json.RawMessage(`{"id":"T-2","stage":null,"revision":{"round":1,"stage":null,"edits":0},"stages":[]}`)
	if _, err := startedAt(none, 1); err == nil {
		t.Error("a ticket at no stage has one")
	}
}
