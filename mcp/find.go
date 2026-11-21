package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/internal/jatext"
)

// findSentence says what a find answered (find_people, find_teams, find_master_<file>_<master>):
// the values found by their labels and how many, the closest first when the find had words, in the
// order of the values (the IDs, the keys) when it had none; or that none is found. Where more are
// found than it answered, it says to add words, or to raise limit while the limit is below the most
// a find answers (server.FindMost): a find that has more answered as many as its limit, so the
// number found is the limit then. In Japanese, a number is set right next to the word that counts
// with it ("3人"), and a name of ASCII off from kana and kanji (jatext.Join).
func findSentence(lang string, d *toolDef, words bool, body []byte) string {
	var a server.FindAnswer
	if json.Unmarshal(body, &a) != nil {
		return ""
	}
	labels := make([]string, len(a.Found))
	for i, f := range a.Found {
		labels[i] = f.Label
	}
	n := len(labels)
	if lang == "ja" {
		var said string
		order := ""
		switch {
		case n < 2:
		case words:
			order = "近い順に"
		case d.read == "find_master":
			order = "キーの順に"
		default:
			order = "ID の順に"
		}
		switch {
		case n == 0 && d.read == "find_people":
			return "見つかった人はいません。"
		case n == 0 && d.read == "find_teams":
			return "見つかったチームはありません。"
		case n == 0:
			return jatext.Join("マスタ", d.master, "で、見つかった行はありません。")
		case d.read == "find_people":
			said = jatext.Join(order+fmt.Sprintf("%d人見つかりました：", n), strings.Join(labels, "、"), "。")
		case d.read == "find_teams":
			said = jatext.Join(order+fmt.Sprintf("%dチーム見つかりました：", n), strings.Join(labels, "、"), "。")
		default:
			said = jatext.Join("マスタ", d.master, "で、"+order+fmt.Sprintf("%d行見つかりました：", n), strings.Join(labels, "、"), "。")
		}
		switch {
		case a.More && n < server.FindMost:
			said += jatext.Join("ほかにもあります。言葉を足して絞り込むか、", "limit", fmt.Sprintf("を上げてください（%dまで）。", server.FindMost))
		case a.More:
			said += "ほかにもあります。言葉を足して絞り込んでください。"
		}
		return said
	}
	var what string
	switch {
	case n == 0 && d.read == "find_people":
		return "No person is found."
	case n == 0 && d.read == "find_teams":
		return "No team is found."
	case n == 0:
		return "No row of the master " + d.master + " is found."
	case d.read == "find_people":
		what = count(n, "person", "people")
	case d.read == "find_teams":
		what = count(n, "team", "teams")
	default:
		what = count(n, "row", "rows") + " of the master " + d.master
	}
	said := "Found " + what
	switch {
	case n < 2:
	case words:
		said += ", the closest first"
	case d.read == "find_master":
		said += ", in the order of their keys"
	default:
		said += ", in the order of their IDs"
	}
	said += ": " + strings.Join(labels, ", ") + "."
	switch {
	case a.More && n < server.FindMost:
		said += fmt.Sprintf(" There are more: add words to narrow them down, or raise limit (up to %d).", server.FindMost)
	case a.More:
		said += " There are more: add words to narrow them down."
	}
	return said
}
