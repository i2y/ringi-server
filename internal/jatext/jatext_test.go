package jatext

import "testing"

// TestJoin sets names in Japanese sentences: a name of ASCII is set off from kana and kanji by one
// space, before it and after it, and set right next to a full-width mark, before it and after it.
func TestJoin(t *testing.T) {
	for _, c := range []struct {
		pieces []string
		want   string
	}{
		// a name that ends in ASCII, before kana, a kanji and a full-width mark
		{[]string{"Leave request", "の履歴"}, "Leave request の履歴"},
		{[]string{"e1101", "様"}, "e1101 様"},
		{[]string{"Leave request", "：あなたの申請"}, "Leave request：あなたの申請"},
		{[]string{"e1101 (ja)", "（", "e3101", "の代わりに）"}, "e1101 (ja)（e3101 の代わりに）"},
		{[]string{"Price", "、", "Quantity", "のどれか"}, "Price、Quantity のどれか"},
		// a name that starts with ASCII, after kana, a kanji and a full-width mark
		{[]string{"計算した", "Amount", "が、"}, "計算した Amount が、"},
		{[]string{"チケット", "t-1", "（", "leave", "）は", "in_progress", "です。"}, "チケット t-1（leave）は in_progress です。"},
		{[]string{"「", "Leave", "」で見つかりました。"}, "「Leave」で見つかりました。"},
		// names of Japanese, and a name of Japanese that ends in a full-width mark
		{[]string{"明細", "の", "単価"}, "明細の単価"},
		{[]string{"期間（月）", "は"}, "期間（月）は"},
		{[]string{"組織のアカウントには", "佐藤 一郎", "でログインしました。"}, "組織のアカウントには佐藤 一郎でログインしました。"},
		{[]string{"組織のアカウントには", "e1101@example.com", "でログインしました。"}, "組織のアカウントには e1101@example.com でログインしました。"},
		// a sign of ASCII is a side of ASCII; a space already there is kept, and none is added to it
		{[]string{"`start`", "の"}, "`start` の"},
		{[]string{"ステップは ", "draft", " です。"}, "ステップは draft です。"},
		// a letter of another script, the full-width letters and the long vowel mark
		{[]string{"Café", "の"}, "Café の"},
		{[]string{"ＡＢＣ", "の"}, "ＡＢＣの"},
		{[]string{"x", "ー"}, "x ー"},
		{[]string{"々", "x"}, "々 x"},
		// marks of other kinds
		{[]string{"x", "…"}, "x…"},
		{[]string{"x", "〜", "y"}, "x〜y"},
		{[]string{"x", "・", "y"}, "x・y"},
		// two pieces of one kind, empty pieces, the ends of the sentence
		{[]string{"Leave", "request"}, "Leaverequest"},
		{[]string{"の", "", "x", ""}, "の x"},
		{[]string{"x"}, "x"},
		{nil, ""},
	} {
		if got := Join(c.pieces...); got != c.want {
			t.Errorf("Join(%q) = %q; want %q", c.pieces, got, c.want)
		}
	}
}

// TestSpace answers the space of two pieces by their ends alone.
func TestSpace(t *testing.T) {
	for _, c := range []struct{ before, after, want string }{
		{"Leave request", "：", ""},
		{"：", "Leave request", ""},
		{"Leave request", "の", " "},
		{"の", "Leave request", " "},
		{"x", "", ""},
		{"", "x", ""},
		{"x ", "の", ""},
		{"の", " x", ""},
	} {
		if got := Space(c.before, c.after); got != c.want {
			t.Errorf("Space(%q, %q) = %q; want %q", c.before, c.after, got, c.want)
		}
	}
}
