# screens

サーバーが動かす申請の画面。この Go のパッケージは、ritsu が書く A2UI 1.0 のテンプレート（`ritsu gen --target requests`）に、一人の見る人のためにチケットの値を入れ、ボタンが押されたときにレンダラが送るメッセージを、それに当たる API の操作にする。依存するのは標準ライブラリだけで、何も保管せず、申請の種類を一つも知らない。どの種類も同じコードを通る。どうしているかは [DESIGN.ja.md](DESIGN.ja.md) に書いた。英語の版は [README.md](README.md) である。

## 使い方

```go
//go:embed requests
var generated embed.FS

sub, _ := fs.Sub(generated, "requests")
set, err := screens.Load(sub) // openapi.json、types/、a2ui/
```

チケットの承認カードを見せるには、カードの操作をそれぞれ gate に尋ねて、値を入れる。

```go
actions, _ := set.Actions("expense", "card") // edit、approve、reject、send_back、…、comment
can := allowed(viewer, ticket, actions)       // サーバーが gate に尋ねる
serving := screens.Serving{Find: "/a2ui/find", Upload: "/files"}
card, err := set.Card(screens.Card{Language: "ja", Ticket: ticketJSON, Can: can, Names: directory, Serving: serving})
// card.Messages：createSurface、updateDataModel、updateComponents。この順にレンダラに送る
```

`directory` はサーバーの `screens.Lookup` である。表ごとに一度、画面が見せる ID で尋ねられ、人の名前、マスタの行の名前、ファイルの URL を答える。`serving` は、サーバーが画面の部品のために決めたアドレスを持つ。部品が人、チーム、マスタの行を探すアドレス（`/find`）と、添付を預けるアドレス（`/upload`。添付のある種類の画面）である。断りのあとは、その断りも持つ（`/refused`）。試験が `testdata/golden/expense.card.en.json` に置いた承認カードは、

```json
{"version":"1.0","createSurface":{"catalogId":"https://a2ui.org/specification/v1_0/catalogs/basic/catalog.json","surfaceId":"expense.card.T-1"}}
```

で始まり、データには、ほかの物と並んで次を持つ。

```json
"ticket": {"id": "T-1", "made_by": null, "requester": "e1201", "revision": {"edits": 0, "round": 1, "stage": 0}, "stage": "first", "status": "in_progress"},
"can": {"add_approver": false, "approve": true, "comment": true, "discard": false, "edit": false, "hand_over": false, "reject": true, "send_back": true, "withdraw": false},
"add_approver": {"approver": null, "parts": [{"part": "manager"}]},
"files": {"receipt.pdf": "https://files.example.test/receipt.pdf"},
"find": "/a2ui/find",
"refused": {"field": null, "text": null}
```

新しい申請の申請書は、見せるたびに別の `Surface`（ランダムな ID）が要る。申請の冪等キーを面から作るからである。

```go
form, err := set.Form(screens.Form{Kind: "leave", Language: "ja", Surface: newID(), Can: []string{"submit"}, Serving: serving})
```

二つの一覧には、サーバーが見る人のために読んだチケットを入れる。`set.List` には見る人の答えを待つチケットを、`set.Mine` には見る人自身の申請（見る人が申請者の物と、見る人が代わりに出した物）を入れる。

```go
waiting, err := set.List(screens.List{Kind: "expense", Language: "ja", Tickets: waitingTickets, Names: directory})
mine, err := set.Mine(screens.List{Kind: "expense", Language: "ja", Tickets: ownTickets, Names: directory})
```

ボタンが押されると、レンダラがアクションのメッセージを送る。それを操作にし、HTTP から来た同じ操作と同じに、送った人の操作として実行する。

```go
a, err := screens.ParseAction(message)
op, err := set.Operation(a)
// op.ID、op.Method、op.Path、op.Ticket、op.Body、op.Key
```

`testdata/golden/operations.json` から。次のメッセージは、

```json
{"version":"1.0","action":{"name":"approve","surfaceId":"expense.card.T-1","sourceComponentId":"button.approve","timestamp":"2026-11-16T09:05:00.000Z","context":{"ticket":"T-1","revision":{"round":1,"stage":0,"edits":0},"comment":"Fine."}}}
```

次の操作になる。

```json
{"id":"approve","method":"POST","path":"/tickets/T-1/approve","ticket":"T-1","body":{"revision":{"round":1,"stage":0,"edits":0},"comment":"Fine."},"key":"screen-8d0467a0aadff7e480af24d481d53aa2e36df49c83ecfc2172995b9eeded1a9b"}
```

本文は、ほかの本文と同じく、サーバーが操作のスキーマで確かめる。キーは操作の冪等キーで、新しい申請では申請書と本文ごとに一つ、チケットへの操作では押すたびに一つである（DESIGN.ja.md の 3.1）。

サーバーが操作を断ったときは、API が答えた物を `screens.Refusal` が持つ。種類、英語と日本語の文、`form` の断りなら、断りが指すフォームの場所（`at`。行を 0 から数える `items[1].price` の形で、`screens.ReadPlace` が読む）である。`set.Refused` は、見せている申請書か承認カードで断りを言うメッセージを作り、本人が入れた値はそのまま残す。画面は、場所が指すフィールドの横に文を見せ、種類が `comment` の断りはコメントの下に、どちらでもなければ画面の頭に見せる。`testdata/golden/expense.form.refused.ja.json` は、日本語の申請書での `items[0].price` の断りである。

```json
{"version":"1.0","updateDataModel":{"surfaceId":"expense.form.f1","path":"/refused","value":{"comment":false,"field":"items","text":"`1_000_001JPY` は、`items[0].price` の範囲（`>=1JPY <=1_000_000JPY`）の外です"}}}
```

`screens.RefusalWords(lang, kind)` は、断りとともに本人に伝える一文を、断りの種類だけから答える（DESIGN.ja.md の 3.2）。表は一つで、画面と素の HTML のページが両方使う。同じように、`LimitedWords` はコメントを今は受け付けないことを、`OfferedWords` は、今は効いている版が無い種類を出せるようになる日を言う。`offered` の画面が無いパッケージのフォームの答えの 404、ページ、エージェントのツールが、この文を言う。その日から出せる人（`Server.Offered`）には、パッケージが持っていれば（約束に加わる前に生成されたパッケージは持たず、そのまま読める）、`offered` の画面そのものを、出せる日と戻る先を入れて返す：`set.Offered(screens.Offered{Kind: "leave", Language: "ja", From: "2027-04-01", Home: "/pages/", Surface: newID()})`。

レンダラは ritsu のカタログと関数も読む。`set.Asset("catalog.json")` と `set.Asset("functions.js")` が、それぞれを Content-Type と ETag 付きで答え、`set.Assets()` が二つをファイルシステムとして答える。

## 素の HTML のページ

パッケージ `page` は、同じ画面を素の HTML のページで見せる。A2UI のレンダラが無いブラウザでも、必要なら JavaScript が無くても使え、ブラウザが送ったフォームを同じ操作に読む。[page/README.ja.md](page/README.ja.md) にある。画面もページも、パッケージ `web` が HTTP で出す（[../web/README.ja.md](../web/README.ja.md)）。

## 試験

```sh
go test .            # 6 秒ほど。./... ならページ（page/）も含めて 9 秒ほど
go test -race .      # 65 秒ほど。./... なら 95 秒ほど
```

試験は、`testdata/` に生成した ritsu の例（十の種類の 100 のテンプレート、そのベクタ、そこから ritsu が生成した Go のパッケージ）を読み、`testdata/driver.mjs` を通して、Node 24 で画面をレンダラと同じに読む。Node が無ければ、その試験は `SKIP:` と言って通る。ベクタから 5,530 の画面に値を入れ、296 のボタンを全部押し、ベクタのイベントを、その画面でボタンを 2,204 回押して行い、画面が送る物を生成したコードに渡す。送った 2,046 回の答えはどれもベクタのとおりで、画面の検査が止めた 156 回と、画面が隠すフィールドに値を入れる 2 回（画面はそのフィールドを送らない）と、どの画面も送らない物を求める 138 行は、種類もどれも断る。一覧は DESIGN.ja.md の 7 章にある。

新しい ritsu の例を読むには、ritsu のチェックアウトから、`ritsu` を `PATH` に置いて次を走らせる。

```sh
testdata/regen.sh <ritsu のチェックアウト>/crates/ringi/examples
testdata/bless.sh   # golden のファイル。残す前に差分を読む
```
