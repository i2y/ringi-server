# A2UI の画面（screens）

パッケージ `screens` は、ringi のサーバーの申請を A2UI 1.0 の画面にし、ボタンが送ったものを読みます。画面のテンプレートは ritsu が書き（`ritsu gen --target requests`）、このパッケージが、申請の値を、見る人に合わせて入れます。ボタンが押されると、レンダラーが送るメッセージを API の操作にします。依存するのは標準ライブラリだけで、何も保管しません。どの申請の種類も、同じコードで動きます。英語版は [README.md](README.md) です。

| したいこと | 読むもの |
|---|---|
| 自分のアプリケーションで A2UI の画面を見せる | [A2UI の画面を使う](../docs/screens.ja.md#a2ui-の画面を使う)。サーバーはパッケージ [`web`](../web/README.ja.md) で画面を HTTP で出すので、このパッケージを自分で呼ぶことはありません |
| `web` を使わずに、自分のサーバーから画面を出す | 下の[使い方](#使い方) |
| 同じ画面を素の HTML で見せる | パッケージ [`page`](page/README.ja.md)。A2UI のレンダラーも JavaScript も要らない |
| しくみとその理由を知る | [設計](DESIGN.ja.md) |

## 使い方

### ritsu が書いたものを読む

```go
//go:embed requests
var generated embed.FS

sub, _ := fs.Sub(generated, "requests")
set, err := screens.Load(sub) // openapi.json、types/、a2ui/
```

### 申請を見せる

申請のカードには、申請の内容、経路、見る人が今できる操作のボタンがあります。カードの操作のうち、その人ができるものを調べてから、値を入れます。

```go
actions, _ := set.Actions("expense", "card") // edit、approve、reject、send_back、…、comment
can := allowed(viewer, ticket, actions)       // サーバーが gate と申請の種類で調べる（Server.Can）
serving := screens.Serving{Find: "/a2ui/find", Upload: "/files"}
card, err := set.Card(screens.Card{Language: "ja", Ticket: ticketJSON, Can: can, Names: directory, Serving: serving})
// card.Messages：createSurface、updateDataModel、updateComponents。この順にレンダラーに送る
```

- `directory` は、サーバーが用意する `screens.Lookup` です。表ごとに一度、画面に出す ID を渡されて呼ばれ、人の名前、マスタの行の名前、ファイルの URL を返します。
- `serving` は、サーバーが決めるアドレスです。`Find` は、人、チーム、マスタの行を探すところ、`Upload` は、添付ファイルをアップロードするところです。

試験が `testdata/golden/expense.card.en.json` に置いているカードは、次のように始まります。

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

### 新しい申請のフォームを見せる

見せるたびに、別の `Surface`（ランダムな ID）を渡します。二重に申請されないようにするキーを、そこから作るためです。

```go
form, err := set.Form(screens.Form{Kind: "leave", Language: "ja", Surface: newID(), Can: []string{"submit"}, Serving: serving})
```

### 一覧を見せる

`set.List` には、見る人を待っている申請を入れます。`set.Mine` には、その人の申請を入れます。自分が申請者のものと、ほかの人の代わりに出したものです。

```go
waiting, err := set.List(screens.List{Kind: "expense", Language: "ja", Tickets: waitingTickets, Names: directory})
mine, err := set.Mine(screens.List{Kind: "expense", Language: "ja", Tickets: ownTickets, Names: directory})
```

### ボタンの操作を行う

ボタンが押されると、レンダラーがアクションのメッセージを送ります。それを操作にし、送った人の操作として、HTTP で来た同じ操作と同じように行います。

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

本文は、ほかの本文と同じように、サーバーが確かめます。キーは、操作の `Idempotency-Key` です。新しい申請ではフォームと本文ごとに一つ、申請への操作では押すたびに一つです（[設計](DESIGN.ja.md)の 3.1）。

### 受け付けられなかった理由を見せる

サーバーが操作を拒否したとき、API が返したものを `screens.Refusal` に入れます。種類、英語と日本語の文、フォームの拒否ならその場所（`at`。`items[1].price` のように、行を 0 から数えます）です。`set.Refused` は、それを画面に出すメッセージを作ります。本人が入れた値は、そのまま残ります。

| 拒否 | 文が出るところ |
|---|---|
| フォームの場所を指す | そのフィールドの横 |
| 種類が `comment` | コメントの下 |
| そのほか | 画面の先頭 |

次は、日本語のフォームでの、`items[0].price` の拒否です（`testdata/golden/expense.form.refused.ja.json`）。

```json
{"version":"1.0","updateDataModel":{"surfaceId":"expense.form.f1","path":"/refused","value":{"comment":false,"field":"items","text":"`1_000_001JPY` は、`items[0].price` の範囲（`>=1JPY <=1_000_000JPY`）の外です"}}}
```

拒否と一緒に見せる文は、このパッケージにあります。ページとエージェントのツールも、これを使います。

| 関数 | 返す文 |
|---|---|
| `RefusalWords(lang, kind)` | 拒否の文。拒否の種類だけで決まる（[設計](DESIGN.ja.md)の 3.2） |
| `LimitedWords` | コメントを今は受け付けないとき |
| `OfferedWords` | 申請の種類に有効な版がまだなく、ある日から使えるとき |

その日から出せる人（`Server.Offered`）には、出せる日と戻る先を入れた画面 `offered` を見せます（`set.Offered(screens.Offered{Kind: "leave", Language: "ja", From: "2027-04-01", Home: "/pages/", Surface: newID()})`）。この画面が約束に加わる前に ritsu が生成したパッケージには、この画面がありません。そのときのフォームは、`OfferedWords` の文の 404 になります。

### カタログと関数を出す

レンダラーは、ritsu のカタログと関数も読みます。`set.Asset("catalog.json")` と `set.Asset("functions.js")` は、それぞれを Content-Type と ETag と一緒に返します。`set.Assets()` は、二つをファイルシステムとして返します。

## 試験

```sh
go test .            # 6 秒ほど。./... ならページ（page/）も含めて 9 秒ほど
go test -race .      # 65 秒ほど。./... なら 95 秒ほど
```

試験は、`testdata/` に生成してある ritsu の例を使います。十の種類の 120 のテンプレート、そのベクタ、ritsu が生成した Go のパッケージです。画面は、`testdata/driver.mjs` を通して、Node 24 でレンダラーと同じように読みます。Node がなければ、その試験は `SKIP:` と書いて通ります。

- ベクタから 5,542 の画面に値を入れ、336 のボタンをすべて押す。
- ベクタのイベントを、画面のボタンを 2,230 回押して行い、画面が送ったものを生成されたコードに渡す。送った 2,052 回の結果は、どれもベクタのとおり。
- 画面が送らせなかったものは、申請の種類もすべて拒否する。画面の検査が止めた 176 回、画面が隠すフィールドに値を入れた 2 回、どの画面からも送られないものを求める 138 行。

試験の一覧は、[設計](DESIGN.ja.md)の 7 章にあります。新しい ritsu の例を取り込むには、ritsu のチェックアウトを用意し、`ritsu` を `PATH` に置いて、次を実行します。

```sh
testdata/regen.sh <ritsu のチェックアウト>/crates/ringi/examples
testdata/bless.sh   # golden のファイル。コミットする前に差分を読む
```
