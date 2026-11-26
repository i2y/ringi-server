package server_test

import (
	"encoding/json"
	"testing"

	server "github.com/i2y/ringi-server"
)

// TestNamedValues: the values a work ends with are made as the contract writes them, and read back
// by Str, Num and Truth.
func TestNamedValues(t *testing.T) {
	for _, c := range []struct {
		v    server.NamedValue
		want string
	}{
		{server.NamedText("number", "ORD-1 \"x\""), `{"name":"number","value":{"str":{"v":"ORD-1 \"x\""}}}`},
		{server.NamedNumber("amount", -4800), `{"name":"amount","value":{"num":{"v":-4800}}}`},
		{server.NamedTruth("sent", true), `{"name":"sent","value":{"truth":{"v":true}}}`},
	} {
		if b, _ := json.Marshal(c.v); string(b) != c.want {
			t.Errorf("%s is written %s; want %s", c.v.Name, b, c.want)
		}
	}
	if s, ok := server.NamedText("n", "ORD-1").Str(); !ok || s != "ORD-1" {
		t.Errorf("NamedText reads back as %q %v", s, ok)
	}
	if n, ok := server.NamedNumber("n", 7).Num(); !ok || n != 7 {
		t.Errorf("NamedNumber reads back as %d %v", n, ok)
	}
	if b, ok := server.NamedTruth("n", false).Truth(); !ok || b {
		t.Errorf("NamedTruth reads back as %v %v", b, ok)
	}
}

// TestParseEffectKey: a key reads back from the text String writes, and text of another form is
// refused.
func TestParseEffectKey(t *testing.T) {
	k := server.EffectKey{Ticket: "01M89PB269CQ4GTPETPAEMR9QQ", Round: 1, Serial: 15}
	if got, err := server.ParseEffectKey(k.String()); err != nil || got != k {
		t.Errorf("ParseEffectKey(%q) = %v, %v", k.String(), got, err)
	}
	for _, s := range []string{"", "T", "T/1", "/1/2", "T/1/", "T//2", "T/+1/2", "T/1/-2", "T/a/2", "T/1/99999999999999999999"} {
		if _, err := server.ParseEffectKey(s); err == nil {
			t.Errorf("ParseEffectKey(%q) is no error", s)
		}
	}
}
