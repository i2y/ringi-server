# page

サーバーが動かす申請の、素の HTML のページです。申請書、承認カード、チケットの一覧、履歴を、どのブラウザでも見られるページとして作り、JavaScript があってもなくても使えます。ブラウザがそこから送ったフォームを、API の操作に読みます。そのまわりの、ホーム、各ページの上の帯、知らせも作ります。パッケージ `screens` の上に作ってあり、A2UI の画面と同じデータ、同じ名前、同じ操作とキー、同じ断りの文を使います。依存するのは標準ライブラリだけで、何も保管せず、申請の種類を一つも知りません。ページを HTTP で出すのは、パッケージ `web` です（[../../web/README.ja.md](../../web/README.ja.md)）。しくみは [DESIGN.ja.md](DESIGN.ja.md) に書いてあります。英語の版は [README.md](README.md) です。

## 使い方

```go
set, err := screens.Load(sub)                                // A2UI の画面と同じ
pages, err := page.New(set, page.Options{Assets: "/assets/"}) // そこで pages.Asset(name) を配る
```

新しい経費の申請書を見せ、スクリプトの無いブラウザがそれを送ったときの例です。[example_test.go](example_test.go) にあり、`go test` が動かします。

```go
in := page.Form{
	Form:   screens.Form{Kind: "expense", Language: "en", Surface: "f1", MaySubmit: true, Can: []string{"submit"}},
	Action: "/requests/expense",
	Now:    time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC),
}
pg, err := pages.Form(in) // pg.Document() が HTML のページ

post := page.Post{Values: url.Values{
	"_surface": {"expense.form.f1"}, "_at": {"2026-11-17T09:00:00Z"}, "_do": {"button.submit"},
	"s.category": {"1"}, "f.category": {"travel"},
	"s.items": {"1"}, "r.items": {"0", "1"},
	"f.items.0.what": {"Train to Yokohama and back"}, "f.items.0.price": {"12,800"},
	"f.items.1.what": {"Lunch with the customer"}, "f.items.1.price": {"¥3200"},
	"s.purpose": {"1"}, "f.purpose": {" A visit to a customer "},
	"s.used_on": {"1"}, "f.used_on": {"2026/12/2"},
	"s.trip_to": {"1"}, "f.trip_to": {"Yokohama"},
	"s.receipt": {"1"},
	"comment":   {""},
}}
in.Surface = "" // 新しい申請の面は投稿から読む
rd, err := pages.ReadForm(in, post, nil)
fmt.Println(rd.Operation.Method, rd.Operation.Path)
fmt.Println(string(rd.Operation.Body))
```

出力は次のとおりです。

```
POST /requests/expense/tickets
{"requester":null,"form":{"category":"travel","items":[{"price":12800,"what":"Train to Yokohama and back"},{"price":3200,"what":"Lunch with the customer"}],"purpose":"A visit to a customer","trip_to":"Yokohama","used_on":"2026-12-02"},"comment":null}
```

金額は人が打つとおりに `12,800` や `¥3200` と、日は `2026/12/2` と、目的は前後に空白を付けて打ってあります。本文は、同じ値を A2UI の申請書に入れたときに ritsu の `functions.js` が送る物と同じです（試験が二つを突き合わせます）。サーバーは `rd.Operation` を、HTTP で送られた同じ操作と同じく、その人として実行してください。読む側が投稿を止めたとき（`rd.Operation == nil`）や、API が操作を断ったとき（API が答えた `screens.Refusal` で `rd.Refused(refusal)`）は、`Again: rd` を付けてページをもう一度見せてください。打った値をそのまま残し、おかしい所をそれぞれの部品で言います。値の断りは、その場所が指すフィールドで言います。フィールドで Enter を押した投稿（`rd.Stay()`）には 204 No Content で答えてください。ブラウザはページをそのまま残します。サーバーのハンドラーの全体は DESIGN.ja.md の 1 章にあります。

日本語のページでは、明細の 1 行目の金額は次のようになります。

```html
<div class="field number" id="field-in-items-0-price" data-field="items.price" data-type="number" data-num="1" data-den="1" data-decimals="0" data-format="{}円" data-code="JPY" data-yen="true" data-required="true">
<label for="in-items-0-price">金額 <span class="required">必須</span></label>
<span class="amount"><input type="text" inputmode="decimal" autocomplete="off" id="in-items-0-price" name="f.items.0.price" value="" aria-describedby="checks-in-items-0-price" aria-required="true" /> <span class="unit">円</span></span>
<ul class="checks" id="checks-in-items-0-price"><li data-check="items.price.required" hidden="hidden">明細の金額を入力してください。</li><li data-check="items.price.range" hidden="hidden">明細の金額は1円から1,000,000円までにしてください。</li><li data-read="number" hidden="hidden">明細の金額は数値で入力してください。</li><li data-read="step" hidden="hidden">明細の金額は整数で入力してください。</li></ul>
```

検査は、A2UI の画面の検査（`data-check`。文言も同じ）と、ページ自身が数を読むときの検査（`data-read`）です。スクリプトが無ければ、通らない物はサーバーがページをもう一度見せるときに言います。スクリプトがあれば、人が打つそばから `page.js` が言います。そのとき `page.js` は、A2UI の画面と同じく ritsu の `functions.js` に尋ねます。ステップが理由を求めるボタンは、コメントをそのボタンだけの言葉で検査します（「理由をコメントに書いてください。」）。空のコメントや空白だけのコメントは通りません。

`Form.Worked` と `Card.Worked` で、ページに種類の `Worked` の入口を渡します（サーバーは `Server.Worked` を渡します）。ページはそれに、フォームが計算する物を尋ねます。そのとき、チケットに承認者が直した値があれば、それ（`edited`）も渡します。ページは、答えのとおりに作ります。条件で見せるフィールドで条件が成り立たない物は、ページに隠したまま入れ、操作する部品を無効にして、何も送られないようにします。条件で必須になるフィールドは、条件が成り立つあいだだけ必須の印と添え書きを出します。フォームが計算するフィールドは、その値を見せます。計算した値が範囲の外のときの検査は、値を計算したフィールドを挙げて、そのどれかを直すように言います（「明細の金額を直してください。」）。カードでは、隠したフィールドは値に入れず、計算する物は値を見せます。これは全部、スクリプトなしで決まります。入口が nil のページや、入口の無い版は、まだ何も打たれていないときのとおりにフォームを見せます。スクリプトがあれば、`page.js` が、人が打つそばからもう一度決めます（`data-shown`、`data-required-when`）。

テキストとコメントは、両端の空白を除いて読みます（Unicode の White_Space。`unicode.IsSpace` が知る 25 のコードポイント。`functions.js` と `page.js` も同じに除きます）。人が書いたテキストを、読むために見せるとき（カードの値、履歴のコメント）は、タブと行の終わり以外の制御文字はそれぞれ図にして見せます（U+001C は ␜、U+007F は ␡）。名前や理由に隠れないためです。そのまま送るためにテキストを持つ部品（入力）には、そのまま入れます。

ページは `page.css`、`page.js`、一式の `functions.js` を `Options.Assets` から読み込み、インラインのスクリプトもスタイルも持ちません。`Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; form-action 'self'` のもとで動きます。

画面のまわりのページも作ります。`pages.Home` はホーム（本人を待っている物、出せる申請、自分の申請。どの種類もリンク）、`pages.Bar` はサーバーが各ページの上に置く帯（ホーム、ログインしている人、ほかの言語、ログアウト）、`pages.Notice` は求められたページの代わりに一つのことを言うページ（「見つかりません」「ログインしていません」「送った内容を読めませんでした」「送った内容が大きすぎます」「サーバーのエラー」）、`pages.Offered` は「いつから出せるか」を言うページです（API の `unoffered` の答えや A2UI の画面と同じ言葉で。`screens.OfferedWords`）。ホームでは、今効いている版の無い種類の行（`Home.New` の `Entry`）は、その日を言い（`Entry.From`。`Server.Offered` が答えるとおり）、まだ見せるフォームが無いのでリンクを持ちません。DESIGN.ja.md の 3.4 にあります。

## 試験

```sh
go test ./...        # screens/ で。9 秒ほど
go test -race ./...  # 95 秒ほど
```

試験は `screens` の試験の材料（`../testdata/`）を読みます。ベクタのチケットから 9,492 のページを作り、ホーム、一覧の見出しと列、帯、どの知らせも両方の言語で作り、2,964 行のうち人がするイベントを、スクリプトの無いブラウザと同じにページで送り（投稿 2,218 回と、行を足し引きする投稿 454 回）、ページが送る物を ritsu が生成したコードに渡します。送った 2,064 回の答えはどれもベクタのとおりで、ページの検査が止めた投稿と、どのページも送らない物を求める行は、種類もどれも断ります。Node があれば、page.js の読み方を Go の読み方と、page.js が計算して見せる合計（996）を種類が計算した値と、送った 1,208 のフォームを `functions.js` が送る物と突き合わせます。ページが送り `functions.js` が止める 12 のフォームは、種類がどれも、止めた検査の場所で断ります。Node が無ければその試験は `SKIP:` の行を出して飛ばし（`go test -v` で見えます）、通ります。読み戻すどのページも、キーボードやスクリーンリーダーで使う人に要ること（`<main>` が一つ、フォーカスの行き先、検索で見つかった物を一行で言うこと、ほかの言語で見せる種類の文言にその言語の印を付けること）を確かめ、`page.css` の色が WCAG 2.2 の求めるコントラストを明るい配色と暗い配色で満たすことも確かめます。試験の一覧は DESIGN.ja.md の 7 章にあります。
