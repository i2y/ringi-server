# 画面（screens）

ringi のサーバーの申請を、A2UI 1.0 の画面で見せるためのパッケージです。画面のテンプレートは ritsu が書きます（`ritsu gen --target requests`）。このパッケージは、そこにチケットの値を、見る人に合わせて入れます。ボタンが押されたときにレンダラが送るメッセージは、それに当たる API の操作に直します。依存するのは標準ライブラリだけで、何も保管しません。どの申請の種類も、同じコードで動きます。

しくみとその理由は、[設計](DESIGN.ja.md)にあります。英語版は [README.md](README.md) です。この画面を HTTP で出すのは、パッケージ [`web`](../web/README.ja.md) です。`web` を使うサーバーでは、このパッケージを自分で呼ぶ必要はありません。

## 使い方

```go
//go:embed requests
var generated embed.FS

sub, _ := fs.Sub(generated, "requests")
set, err := screens.Load(sub) // openapi.json、types/、a2ui/
```

チケットの承認カードを見せるときは、カードの操作のうち、見る人が今できるものを調べてから、値を入れます。

```go
actions, _ := set.Actions("expense", "card") // edit、approve、reject、send_back、…、comment
can := allowed(viewer, ticket, actions)       // サーバーが gate と申請の種類で調べる（Server.Can）
serving := screens.Serving{Find: "/a2ui/find", Upload: "/files"}
card, err := set.Card(screens.Card{Language: "ja", Ticket: ticketJSON, Can: can, Names: directory, Serving: serving})
// card.Messages：createSurface、updateDataModel、updateComponents。この順にレンダラに送る
```

`directory` は、サーバーが用意する `screens.Lookup` です。表ごとに一度、画面に出す ID を渡されて呼ばれ、人の名前、マスタの行の名前、ファイルの URL を返します。`serving` には、画面の部品が使うアドレスを、サーバーが決めて入れます。人、チーム、マスタの行を探すアドレス（`/find`）と、添付のある申請の種類の画面では、ファイルをアップロードするアドレス（`/upload`）です。拒否のあとは、その拒否も入ります（`/refused`）。

試験が `testdata/golden/expense.card.en.json` に置いている承認カードは、次のように始まります。

```json
{"version":"1.0","createSurface":{"catalogId":"https://a2ui.org/specification/v1_0/catalogs/basic/catalog.json","surfaceId":"expense.card.T-1"}}
```

データには、たとえば次のものが入ります。

```json
"ticket": {"id": "T-1", "made_by": null, "requester": "e1201", "revision": {"edits": 0, "round": 1, "stage": 0}, "stage": "first", "status": "in_progress"},
"can": {"add_approver": false, "approve": true, "comment": true, "discard": false, "edit": false, "hand_over": false, "reject": true, "send_back": true, "withdraw": false},
"add_approver": {"approver": null, "parts": [{"part": "manager"}]},
"files": {"receipt.pdf": "https://files.example.test/receipt.pdf"},
"find": "/a2ui/find",
"refused": {"field": null, "text": null}
```

新しい申請書には、見せるたびに別の `Surface`（ランダムな ID）を渡します。申請の冪等キーを、そこから作るためです。

```go
form, err := set.Form(screens.Form{Kind: "leave", Language: "ja", Surface: newID(), Can: []string{"submit"}, Serving: serving})
```

二つの一覧には、見る人のためにサーバーが読んだチケットを入れます。`set.List` には、その人を待っているチケットを入れます。`set.Mine` には、その人の申請（自分が申請者のものと、ほかの人の代わりに出したもの）を入れます。

```go
waiting, err := set.List(screens.List{Kind: "expense", Language: "ja", Tickets: waitingTickets, Names: directory})
mine, err := set.Mine(screens.List{Kind: "expense", Language: "ja", Tickets: ownTickets, Names: directory})
```

ボタンが押されると、レンダラがアクションのメッセージを送ります。それを操作に直し、送った人の操作として、HTTP で来た同じ操作と同じように実行します。

```go
a, err := screens.ParseAction(message)
op, err := set.Operation(a)
// op.ID、op.Method、op.Path、op.Ticket、op.Body、op.Key
```

たとえば、`testdata/golden/operations.json` の次のメッセージは、

```json
{"version":"1.0","action":{"name":"approve","surfaceId":"expense.card.T-1","sourceComponentId":"button.approve","timestamp":"2026-11-16T09:05:00.000Z","context":{"ticket":"T-1","revision":{"round":1,"stage":0,"edits":0},"comment":"Fine."}}}
```

次の操作になります。

```json
{"id":"approve","method":"POST","path":"/tickets/T-1/approve","ticket":"T-1","body":{"revision":{"round":1,"stage":0,"edits":0},"comment":"Fine."},"key":"screen-8d0467a0aadff7e480af24d481d53aa2e36df49c83ecfc2172995b9eeded1a9b"}
```

本文は、ほかの本文と同じように、サーバーが操作のスキーマで確かめます。キーは、操作の冪等キーです。新しい申請では申請書と本文ごとに一つ、チケットへの操作では押すたびに一つです（[設計](DESIGN.ja.md)の 3.1）。

サーバーが操作を拒否したとき、API が返したものは `screens.Refusal` に入ります。種類、英語と日本語の文、`form` の拒否ならフォームの場所（`at`。`items[1].price` のように、行を 0 から数えます。`screens.ReadPlace` が読みます）です。`set.Refused` は、見せている申請書や承認カードに拒否を表示するメッセージを作ります。本人が入れた値は、そのまま残ります。文は、場所が指すフィールドの横に出ます。種類が `comment` の拒否はコメントの下に、どちらでもなければ画面の先頭に出ます。次は、日本語の申請書での、`items[0].price` の拒否です（`testdata/golden/expense.form.refused.ja.json`）。

```json
{"version":"1.0","updateDataModel":{"surfaceId":"expense.form.f1","path":"/refused","value":{"comment":false,"field":"items","text":"`1_000_001JPY` は、`items[0].price` の範囲（`>=1JPY <=1_000_000JPY`）の外です"}}}
```

`screens.RefusalWords(lang, kind)` は、拒否と一緒に見せる文を返します。文は拒否の種類だけで決まり（[設計](DESIGN.ja.md)の 3.2）、画面と素の HTML のページが同じ表を使います。`LimitedWords` は、コメントを今は受け付けないときの文を返します。`OfferedWords` は、有効な版がまだなく、ある日から出せる申請の種類の文を返します。その日から出せる人（`Server.Offered`）には、出せる日と戻る先を入れた `offered` の画面を見せます（`set.Offered(screens.Offered{Kind: "leave", Language: "ja", From: "2027-04-01", Home: "/pages/", Surface: newID()})`）。この画面が約束に加わる前に生成したパッケージも、そのまま読めます。そのときのフォームは 404 になり、`OfferedWords` の文が返ります。ページとエージェントのツールも、この文を使います。

レンダラは、ritsu のカタログと関数も読みます。`set.Asset("catalog.json")` と `set.Asset("functions.js")` は、それぞれを Content-Type と ETag と一緒に返します。`set.Assets()` は、二つをファイルシステムとして返します。

## 素の HTML のページ

パッケージ [`page`](page/README.ja.md) は、同じ画面を素の HTML のページで見せ、ブラウザが送ったフォームを同じ操作に直します。A2UI のレンダラがないブラウザでも、JavaScript がなくても使えます。

## 試験

```sh
go test .            # 6 秒ほど。./... ならページ（page/）も含めて 9 秒ほど
go test -race .      # 65 秒ほど。./... なら 95 秒ほど
```

試験は、`testdata/` に生成してある ritsu の例（十の種類の 120 のテンプレート、そのベクタ、ritsu が生成した Go のパッケージ）を使います。画面は、`testdata/driver.mjs` を通して、Node 24 でレンダラと同じように読みます。Node がなければ、その試験は `SKIP:` と書いて通ります。ベクタから 5,542 の画面に値を入れ、336 のボタンをすべて押します。ベクタのイベントは、画面のボタンを 2,230 回押して行い、画面が送ったものを生成されたコードに渡します。送った 2,052 回の結果は、どれもベクタのとおりです。画面の検査が止めた 176 回、画面が隠すフィールドに値を入れた 2 回（画面はそのフィールドを送りません）、どの画面からも送られないものを求める 138 行は、申請の種類もすべて拒否します。試験の一覧は、[設計](DESIGN.ja.md)の 7 章にあります。

新しい ritsu の例を取り込むには、ritsu のチェックアウトを用意し、`ritsu` を `PATH` に置いて、次を実行します。

```sh
testdata/regen.sh <ritsu のチェックアウト>/crates/ringi/examples
testdata/bless.sh   # golden のファイル。コミットする前に差分を読む
```
