# 素の HTML のページ（screens/page）

パッケージ `page` は、ringi のサーバーの申請のページを作ります。フォーム、申請とそのボタンのページ、一覧、履歴です。どのブラウザでも、JavaScript があってもなくても使えます。そのまわりの、ホーム、各ページの上の帯、一つのことを知らせるページも作ります。ブラウザがページから送ったフォームは、API の操作にします。

パッケージ [`screens`](../README.ja.md) の上に作ってあり、データ、名前、操作とそのキー、拒否の文は、A2UI の画面と同じです。依存するのは標準ライブラリだけで、何も保管しません。どの申請の種類も、同じコードで動きます。英語版は [README.md](README.md) です。

| したいこと | 読むもの |
|---|---|
| ページを使う | [動かして、申請を承認する](../../docs/start.ja.md)。サーバーはパッケージ [`web`](../../web/README.ja.md) でページを出すので、このパッケージを自分で呼ぶことはありません |
| `web` を使わずに、自分のサーバーからページを出す | 下の[使い方](#使い方)。サーバーのハンドラーの全体は、[設計](DESIGN.ja.md)の 1 章 |
| しくみとその理由を知る | [設計](DESIGN.ja.md) |

## 使い方

```go
set, err := screens.Load(sub)                                // A2UI の画面と同じ
pages, err := page.New(set, page.Options{Assets: "/assets/"}) // このパスで pages.Asset(name) を出す
```

### フォームを見せて、送られたものを読む

次の例は、新しい経費の申請のフォームを見せ、スクリプトのないブラウザが送ったものを読みます。[example_test.go](example_test.go) にあり、`go test` が動かします。

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
in.Surface = "" // 新しい申請の Surface は、送られたフォームから読む
rd, err := pages.ReadForm(in, post, nil)
fmt.Println(rd.Operation.Method, rd.Operation.Path)
fmt.Println(string(rd.Operation.Body))
```

出力は次のとおりです。

```
POST /requests/expense/tickets
{"requester":null,"form":{"category":"travel","items":[{"price":12800,"what":"Train to Yokohama and back"},{"price":3200,"what":"Lunch with the customer"}],"purpose":"A visit to a customer","trip_to":"Yokohama","used_on":"2026-12-02"},"comment":null}
```

金額は人が打つとおりの `12,800` と `¥3200`、日は `2026/12/2` で、目的の前後には空白があります。本文は、同じ値を A2UI のフォームに入れたときに ritsu の `functions.js` が送るものと同じで、試験が二つを突き合わせます。サーバーは `rd.Operation` を、HTTP で来た同じ操作と同じように、その人の操作として行います。

### ページをもう一度見せる

| 起きたこと | サーバーがすること |
|---|---|
| フォームに読めないところがあった（`rd.Operation == nil`） | `Again: rd` を付けて、ページをもう一度見せる |
| API が操作を拒否した | API が返した `screens.Refusal` で `rd.Refused(refusal)` を呼び、`Again: rd` を付けて、ページをもう一度見せる |
| フィールドで Enter が押された（`rd.Stay()`） | 204 No Content を返す。ブラウザはページをそのまま残す |

もう一度見せるページには、打った値がそのまま残り、直すところが、それぞれの部品の横に出ます。値の拒否は、場所が指すフィールドの横に出ます。

### フィールドの HTML

明細の 1 行目の金額は次のようになります。

```html
<div class="field number" id="field-in-items-0-price" data-field="items.price" data-type="number" data-num="1" data-den="1" data-decimals="0" data-format="{}円" data-code="JPY" data-yen="true" data-required="true">
<label for="in-items-0-price">金額 <span class="required">必須</span></label>
<span class="amount"><input type="text" inputmode="decimal" autocomplete="off" id="in-items-0-price" name="f.items.0.price" value="" aria-describedby="checks-in-items-0-price" aria-required="true" /> <span class="unit">円</span></span>
<ul class="checks" id="checks-in-items-0-price"><li data-check="items.price.required" hidden="hidden">明細の金額を入力してください。</li><li data-check="items.price.range" hidden="hidden">明細の金額は1円から1,000,000円までにしてください。</li><li data-read="number" hidden="hidden">明細の金額は数値で入力してください。</li><li data-read="step" hidden="hidden">明細の金額は整数で入力してください。</li></ul>
```

- `data-check` は A2UI の画面の検査で、文言も同じです。`data-read` は、ページ自身が数を読むときの検査です。
- スクリプトがなければ、通らなかった検査は、サーバーがページをもう一度見せるときに出ます。スクリプトがあれば、`page.js` が、A2UI の画面と同じく ritsu の `functions.js` を使い、人が打つそばから出します。
- 理由の要るステップのボタンは、コメントを専用の文言で検査します（「理由をコメントに書いてください。」）。空のコメントや、空白だけのコメントは通りません。

### ほかのフィールドで変わるフィールド

申請の種類の `Worked` の入口を、`Form.Worked` と `Card.Worked` でページに渡します。サーバーは `Server.Worked` を渡します。ページはこの入口にフォームの値と、申請に承認者の直した値があればそれも渡し、返ってきたとおりに作られます。

| フィールド | ページでは |
|---|---|
| 条件で見せるもので、条件が成り立たない | 隠れ、入力の部品が無効になるので、何も送られない。カードでは、値に入らない |
| 条件で必須になる | 条件が成り立つあいだだけ、必須の印と添え書きが出る |
| フォームが計算する | 値が出る。値が範囲の外なら、検査は、その値の元になったフィールドの名前を出す（「明細の金額を直してください。」） |

どれもスクリプトなしで動きます。スクリプトがあれば、`page.js` が、人が打つそばから同じことをします（`data-shown`、`data-required-when`）。入口が nil のときと、入口のない版では、フォームは何も入力していないときの形になります。

### テキスト

- テキストとコメントは、両端の空白を除いて読みます（Unicode の White_Space。`unicode.IsSpace` が空白とする 25 のコードポイントで、`functions.js` と `page.js` も同じものを除きます）。
- 人が書いたテキストを読むために見せるとき（カードの値、履歴のコメント）は、タブと行の終わりを除く制御文字を、それぞれ図にします（U+001C は ␜、U+007F は ␡）。名前や理由の中に隠れないようにするためです。もう一度送るためにテキストを入れておく部品には、そのまま入れます。

### スタイルシート、スクリプト、ヘッダー

ページは、`page.css`、`page.js`、`functions.js` を `Options.Assets` から読み込みます。インラインのスクリプトもスタイルもないので、`Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; form-action 'self'` のもとで動きます。

### ページのまわり

| 関数 | 作るもの |
|---|---|
| `pages.Home` | ホーム。自分を待っているもの、出せる申請、自分の申請と状態ごとの数。申請の種類はリンクで、その下に申請が何件か出る。有効な版がまだない種類（`Entry.From`。`Server.Offered` が返す日）には、使えるようになる日が出て、リンクはない |
| `pages.Bar` | 各ページの上の帯。ホーム、ログインしている人、ほかの言語、ログアウト |
| `pages.Notice` | 求められたページの代わりに、一つのことを知らせるページ（「見つかりません」「ログインしていません」「送った内容を読めませんでした」「送った内容が大きすぎます」「サーバーのエラー」） |
| `pages.Offered` | 申請の種類をいつから使えるかを知らせるページ。文は、API の `unoffered` の拒否や A2UI の画面と同じ（`screens.OfferedWords`） |

詳しくは、[設計](DESIGN.ja.md)の 3.4 にあります。

## 試験

```sh
go test ./...        # screens/ で。9 秒ほど
go test -race ./...  # 95 秒ほど
```

試験は、`screens` の試験のデータ（`../testdata/`）を使います。

- ベクタのチケットから 9,516 のページを作る。ホーム、一覧の見出しと列、帯、お知らせも、両方の言語で作る。
- 2,990 行のうち人がするイベントを、スクリプトのないブラウザと同じようにページから送り（送信 2,244 回と、行を足すか消す送信 454 回）、ページが送ったものを ritsu が生成したコードに渡す。送った 2,070 回の結果は、どれもベクタのとおり。ページの検査が止めた送信と、どのページからも送られないものを求める行は、申請の種類もすべて拒否する。
- Node があれば、`page.js` の読み方を Go の読み方と、`page.js` が計算する合計（996）を申請の種類が計算した値と、送った 1,208 のフォームを `functions.js` が送るものと突き合わせる。ページが送って `functions.js` が止める 12 のフォームは、止めた検査と同じ場所で、申請の種類もすべて拒否する。Node がなければ、その試験は `SKIP:` の行を出して（`go test -v` で見える）通る。
- 読み戻したページを、キーボードやスクリーンリーダーで使う人に要ることで確かめる。`<main>` が一つ、フォーカスの行き先、検索で見つかったものを一行で出すこと、ほかの言語で見せる文言にその言語の印を付けること。
- `page.css` の色が、明るい配色と暗い配色の両方で、WCAG 2.2 の求めるコントラストを満たすことを確かめる。

試験の一覧は、[設計](DESIGN.ja.md)の 7 章にあります。
