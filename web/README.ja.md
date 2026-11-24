# web

申請の種類の素の HTML のページと、画面と、添付ファイルを、サーバーの API の前で HTTP で出すパッケージです。人はページで申請を出し、承認し、自分の申請を見ます。ritsu が生成した A2UI の画面はサーバーのデータで埋めて返し、画面のボタンの操作を API の操作として動かします。ritsu のカタログと関数を出し、人が付けるファイルを預かります。どれも、ブラウザに要る守り（人、クロスサイトのリクエストへの守り、大きさの上限、ヘッダー）の後ろに置きます。

設計：[DESIGN.ja.md](DESIGN.ja.md)。英語版：[README.md](README.md)。

## 使い方

```go
srv, err := server.New(server.Config{…, Files: store.Files()}) // または web.NewDiskFiles(dir)
h, err := web.New(srv, web.Config{
	Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout", // ページのためのログインのフック
})
signIn.Mount(h) // ログインのルート。同じ守りの後ろに置く
(&http.Server{Addr: ":8080", Handler: h, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe()
```

ハンドラーが受け持つルートは次のとおりです。ほかのリクエストは、サーバーの API に渡します。

| ルート | 返す物 |
|---|---|
| `GET /` | ホーム（`/pages/` への 303） |
| `GET /pages/` | ホーム。本人を待っている物、出せる申請、自分の申請と状態ごとの数。待っている物と自分の申請は、何件かを並べる |
| `GET`、`POST /pages/requests/{kind}/new` | 新しい申請のフォームと、その送信 |
| `GET`、`POST /pages/tickets/{id}` | チケットの承認のカードと、その送信 |
| `GET`、`POST /pages/tickets/{id}/form` | 申請者に戻っている申請を出し直すフォームと、その送信 |
| `GET /pages/tickets/{id}/history` | チケットの履歴 |
| `GET /pages/waiting/{kind}`、`/pages/mine/{kind}` | その種類のチケットのうち本人を待っている物、その種類の本人の申請。新しい順に 1 ページ 50 件 |
| `GET /pages/assets/{name}` | ページのスタイルシートとスクリプト、ritsu の関数 |
| `GET /a2ui/requests/{kind}/form` | 新しい申請のフォーム |
| `GET /a2ui/tickets/{id}/card`、`…/form`、`…/history` | チケットの画面 |
| `GET /a2ui/waiting/{kind}`、`/a2ui/mine/{kind}` | その種類のチケットのうち本人を待っている物、その種類の本人の申請。新しい順に、`?before=` の次から `?limit=` 件（無ければ 50 件、多くて 100 件） |
| `POST /a2ui/actions` | ボタンの操作。操作を動かし、そのあとの画面を返す |
| `GET /a2ui/find/people`、`/a2ui/find/teams`、`/a2ui/find/masters/{file}/{master}` | 選ぶ部品が、名前か ID の一部で探す値。画面の `/find` のアドレスの下（DESIGN 5.1） |
| `GET /a2ui/catalog.json`、`/a2ui/functions.js` | ritsu のカタログと関数 |
| `POST /files` | 本人がアップロードした添付ファイルを預かる（画面の `/upload` のアドレス） |
| `GET /files/{id}` | 添付ファイル。アップロードした本人に |
| `GET /tickets/{ticket}/files/{id}` | チケットが持つ添付ファイル。そのチケットを見られる人に |

だれが送ったかは、サーバーの `Authenticator` が決めます。セッションを持つログイン（パッケージ `oidc`）では、セッションのないままページを開いたブラウザを、`Config.Require` がログインへ送って戻します。各ページの上の帯には、ログアウトのボタンが出ます。人をログインさせるプロキシの後ろに置くサーバーは、`web.ProxyHeader` でプロキシのヘッダーから人を取ります（DESIGN.ja.md の 3.1）。

## ページ

ページは素の HTML です。スクリプトを動かさないブラウザでも、申請を出し、承認し、差し戻せます。ページのスクリプトが動くときは、スクリプトでできることだけを足します（入力に合わせて合計を出す、送る前に確かめの結果を言う、行の追加をサーバーとのやりとりなしで行う）。フォームは、そのフォームを出したページに送ります。ハンドラーはそれを API の操作として読み、本人として動かします（文とコメントは、両端の空白を除いて読みます）。受け付けた送信はチケットのカードへ進む（303）ので、送り直しても二度目の操作にはなりません。受け付けなかった送信は、入れた値と理由を付けてページをもう一度見せます。ページ自身の確かめで止めたときは 422、サーバーが断ったときは 409 で、断りはページの言語で、断りが指すフィールドか、ページの頭に書きます。ページを見せたあとでチケットが変わっていたときは、今のチケットで見せ直します。スクリプトを動かさないページで、フィールドで Enter を押しても、操作にはなりません。サーバーは 204 を返し、ブラウザは入れた値もフォーカスもそのままに、ページを残します。一覧は新しい順に 50 件ずつ見せ、続きがあるときは次のページへのリンクを付けます。

ページの言語は英語か日本語で、画面の言語は種類が持つ言語のどれかです。`?lang=` で選んだ物（ページなら、それからはクッキーが覚えます）、なければブラウザの言語（`Accept-Language`）、なければ組織のデータがその人に与える言語（`people.csv` の `language` 列）、それもなければ種類を書いた言語です。どちらも、一つの関数（`server.LanguageChoice`）で選びます。例のサーバーは <http://localhost:8080/> でページを出します（根の README）。`examples/pages.sh` は、申請から決裁までを curl で通します。

ページは、スタイルシートとスクリプトをサーバーから読み込みます。ポリシーはページ専用の物です。

```sh
curl -sI -H 'X-Person: e1201' 'http://127.0.0.1:8080/pages/?lang=en'
```

```
HTTP/1.1 200 OK
Cache-Control: no-store
Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'
Content-Type: text/html; charset=utf-8
Referrer-Policy: same-origin
Set-Cookie: ringi-lang=en; Path=/; Max-Age=31536000; HttpOnly; SameSite=Lax
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Content-Length: 1985
```

ほかのサイトが送るフォームは、読む前に断り、そう伝えるページを返します。

```sh
curl -s -o /dev/null -w '%{http_code}\n' -H 'X-Person: e1201' -H 'Origin: https://elsewhere.example' \
  --data-urlencode '_do=button.submit' 'http://127.0.0.1:8080/pages/requests/expense/new?lang=en'
```

```
403
```

## 画面を curl で試す

`examples/` で、添付ファイルをディレクトリに置く形で例のサーバーを起動します。

```sh
go run ./cmd/server -files ./files
```

`-dev-login` を付けないので、根の README と同じく、例のサーバーは人を `X-Person` ヘッダーから取ります。手元で試すための物で、本番には使えません。別のターミナルで、次のように試します（ID は毎回変わります）。

**新しい申請のフォーム。** 営業一課の担当（e1201）が、経費のフォームを開きます。返るのは、レンダラーが受け取る順に並んだ A2UI のメッセージです。`can` は、フォームが見せるボタンを表します。

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/requests/expense/form?lang=en' \
  | jq -c '[.[] | keys[0]], .[1].updateDataModel.value.can'
```

```
["createSurface","updateDataModel","updateComponents"]
{"discard":false,"resubmit":false,"submit":true}
```

フォームのデータには、部品が使うアドレスも、ハンドラーが入れます。選ぶ部品が値を探す `find`（`/a2ui/find`）と、添付ファイルのフィールドがファイルを送る `upload`（`/files`）です。

**人を探す。** レンダラーの `PersonPicker` は、フォームの `find` にあるアドレスの下で、どちらの言語でも、名前の一部で人を探します。ページの選ぶ部品も、人が 1,000 人（`web.PicksListed`）を超えると同じように探します（DESIGN 5.1）。

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/find/people?q=staff+3&lang=en' | jq -c .
curl -s -G -H 'X-Person: e1201' --data-urlencode 'q=営業 3' --data-urlencode 'lang=ja' \
  http://127.0.0.1:8080/a2ui/find/people | jq -c .
```

```
{"found":[{"value":"e1203","name":"Sales staff 3","label":"Sales staff 3 (e1203)"}],"more":false}
{"found":[{"value":"e1203","name":"営業担当 3","label":"営業担当 3（e1203）"},{"value":"e3101","name":"営業一部長","label":"営業一部長（e3101）"}],"more":false}
```

二つ目では e3101 も見つかります。どの言葉も、名前か ID に含まれるからです（`e3101` の `3`）。ページの選ぶ部品は、探す操作が受け付けるより長い言葉（200 字）を送らず、聞かずにその旨を言います。人とチームは、API の探す操作と同じく、組織の人だけが探せます。

```sh
curl -s -w ' %{http_code}\n' -H 'X-Person: x9999' 'http://127.0.0.1:8080/a2ui/find/people?q=staff+3&lang=en'
```

```
{"error":"the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-18"} 404
```

**領収書。** フォームの添付ファイルのフィールドが、フォームの `upload` にあるアドレスへアップロードし、返った `id` をフォームに入れます。

```sh
printf '%%PDF-1.4\n%% a receipt (fictional)\n' > receipt.pdf
curl -s -H 'X-Person: e1201' -F 'file=@receipt.pdf;type=application/pdf' http://127.0.0.1:8080/files | jq .
```

```json
{
  "id": "910972bb8b22fc6b54a795faa6d008fb",
  "name": "receipt.pdf",
  "content_type": "application/pdf",
  "size": 33,
  "url": "/files/910972bb8b22fc6b54a795faa6d008fb"
}
```

**申請。** ここでは API から出します（フォームの申請のボタンも、同じ操作を動かします）。

```sh
F=910972bb8b22fc6b54a795faa6d008fb
cat > request.json <<EOF
{"form": {"category": "purchase", "items": [{"what": "A second monitor", "price": 32000}],
          "purpose": "A monitor for the desk", "used_on": "2026-11-16", "receipt": "$F"},
 "comment": "The receipt is attached."}
EOF
curl -s -H 'X-Person: e1201' -H 'Idempotency-Key: monitor-1' -H 'Content-Type: application/json' \
  -d @request.json http://127.0.0.1:8080/requests/expense/tickets \
  | jq -c '{id: .ticket.id, stage: .ticket.stage, approvers: .ticket.stages[0].parts[0].approvers}'
```

```
{"id":"01M7KSHBTFEM304GVA00FCBYEH","stage":"first","approvers":["e1101"]}
```

フォームで指せるファイルは、チケットがもう持っている物と、送った本人がアップロードした物だけです。代わりに e1101 がアップロードしたファイルを指すと、無い ID と同じく 400 になります。

```sh
printf 'notes\n' > notes.txt
O=$(curl -s -H 'X-Person: e1101' -F 'file=@notes.txt;type=text/plain' http://127.0.0.1:8080/files | jq -r .id)
sed "s/$F/$O/" request.json > other.json
curl -s -w ' %{http_code}\n' -H 'X-Person: e1201' -H 'Idempotency-Key: monitor-2' \
  -H 'Content-Type: application/json' -d @other.json http://127.0.0.1:8080/requests/expense/tickets
```

```
{"error":"form.receipt: no such file"} 400
```

**承認者のカード。** 営業一課長（e1101）が、チケットのカードを日本語で開きます。ボタンは gate が e1101 に許す物で、名前は画面の言語で、領収書はチケットを通したリンクです。

```sh
T=01M7KSHBTFEM304GVA00FCBYEH
curl -s -H 'X-Person: e1101' "http://127.0.0.1:8080/a2ui/tickets/$T/card?lang=ja" | tee card.json \
  | jq -c '.[1].updateDataModel.value | {can, people, files}'
```

```
{"can":{"add_approver":false,"approve":true,"comment":true,"discard":false,"edit":true,"hand_over":false,"reject":true,"send_back":true,"withdraw":false},"people":{"e1101":"営業一課長","e1201":"営業担当 1"},"files":{"910972bb8b22fc6b54a795faa6d008fb":"/tickets/01M7KSHBTFEM304GVA00FCBYEH/files/910972bb8b22fc6b54a795faa6d008fb"}}
```

**領収書を開く。** PDF はブラウザで見せます。見せない種類のファイルは保存させ、CSP に `sandbox` を付けます（ここでは `Date` ヘッダーを省いています）。

```sh
curl -si -H 'X-Person: e1101' http://127.0.0.1:8080/tickets/$T/files/$F
```

```
HTTP/1.1 200 OK
Cache-Control: no-store
Content-Disposition: inline; filename=receipt.pdf
Content-Length: 33
Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
Content-Type: application/pdf
Referrer-Policy: same-origin
X-Content-Type-Options: nosniff
X-Frame-Options: DENY

%PDF-1.4
% a receipt (fictional)
```

チケットに付く前のファイルは、アップロードした本人だけが開けます。チケットに付いたあとは、そのチケットを見られる人が、チケットを通して開けます。ほかの人には、無いファイルと同じ 404 を返します。

```sh
for p in "e1201 /files/$F" "e1101 /files/$F" "e2101 /tickets/$T/files/$F"; do
  echo "${p%% *} ${p#* } $(curl -s -o /dev/null -w '%{http_code}' -H "X-Person: ${p%% *}" http://127.0.0.1:8080${p#* })"
done
```

```
e1201 /files/910972bb8b22fc6b54a795faa6d008fb 200
e1101 /files/910972bb8b22fc6b54a795faa6d008fb 404
e2101 /tickets/01M7KSHBTFEM304GVA00FCBYEH/files/910972bb8b22fc6b54a795faa6d008fb 404
```

**カードで承認する。** 承認のボタンを押したときにレンダラーが送るメッセージです。`revision` は、カードが見せている物です。

```sh
R=$(jq -c '.[1].updateDataModel.value.ticket.revision' card.json)
cat > approve.json <<EOF
{"version": "1.0", "action": {"name": "approve", "surfaceId": "expense.card.$T", "sourceComponentId": "button.approve",
  "timestamp": "2026-11-17T01:00:00Z", "context": {"ticket": "$T", "revision": $R, "comment": "OK."}}}
EOF
curl -s -H 'X-Person: e1101' -H 'Content-Type: application/json' -d @approve.json \
  'http://127.0.0.1:8080/a2ui/actions?lang=ja' | jq -c '{outcome, ticket, messages: [.messages[] | keys[0]]}'
```

```
{"outcome":"ok","ticket":"01M7KSHBTFEM304GVA00FCBYEH","messages":["deleteSurface","createSurface","updateDataModel","updateComponents"]}
```

ボタンがあったサーフェスを消し、同じサーフェスでカードを埋め直します。同じメッセージをもう一度送っても（ダブルクリックや再送）、同じ操作です。答えは同じで、チケットは一度だけ、二つ目の段へ進んでいます。

```sh
curl -s -H 'X-Person: e1101' -H 'Content-Type: application/json' -d @approve.json \
  'http://127.0.0.1:8080/a2ui/actions?lang=ja' | jq -c '{outcome, ticket, messages: [.messages[] | keys[0]]}'
curl -s -H 'X-Person: e1201' http://127.0.0.1:8080/tickets/$T \
  | jq -c '{stage: .ticket.stage, answered: [.ticket.stages[] | select(.phase == "answered") | .parts[].answers[].by]}'
```

```
{"outcome":"ok","ticket":"01M7KSHBTFEM304GVA00FCBYEH","messages":["deleteSurface","createSurface","updateDataModel","updateComponents"]}
{"stage":"second","answered":["e1101"]}
```

**断り。** 古いカードで、別のコメントを付けてもう一度押すと、新しい操作になり、e1101 はもう承認できません。`words` は本人に見せる一文で、画面の言語で書きます。カードは今のチケットで埋め直し（承認のボタンは消えます）、`refused` に断り（API の日本語の文。フィールドは無し）を入れ、入れたコメントを戻して返します。

```sh
sed 's/"OK."/"OK, once more."/' approve.json > again.json
curl -s -w '\n%{http_code}\n' -H 'X-Person: e1101' -H 'Content-Type: application/json' -d @again.json \
  'http://127.0.0.1:8080/a2ui/actions?lang=ja' > again.out
head -1 again.out | jq -c '{outcome, refused, words, messages: [.messages[] | keys[0]]}'; tail -1 again.out
```

```
{"outcome":"refused","refused":"person","words":"この操作はできません。","messages":["deleteSurface","createSurface","updateDataModel","updateComponents","updateDataModel"]}
409
```

**本人の申請。** 新しい順に、1 ページ 50 件で並びます（`?limit=` で 1〜100 件を選び、`?before=` は前のページの最後のチケット）。各行の `made_by` は、申請者の代わりに申請を出した人です（申請者が自分で出したなら null）。

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/mine/expense?lang=en' \
  | jq -c '[.[] | keys[0]], [.[1].updateDataModel.value.tickets[] | {id, status, stage}]'
```

```
["createSurface","updateDataModel","updateComponents"]
[{"id":"01M7KSHBTFEM304GVA00FCBYEH","status":"in_progress","stage":"second"}]
```

**効いている版が無い種類。** e6001 が休暇の版 1 を止めたあと（版 2 は 2027-04-01 から。くわしくは根の README の「管理」）、新しい休暇の申請にはフォームがありません。その日から出せる人には、その種類の `offered` の画面を、出せる日とホームへ戻る先を埋めて返します。ほかの人には、無い種類と同じに答えます。

```sh
curl -s -o /dev/null -X POST http://127.0.0.1:8080/admin/kinds/leave/versions/1 -H 'X-Person: e6001' \
  -H 'X-Roles: workflow_admin' -H 'Content-Type: application/json' -H 'Idempotency-Key: off-1' \
  -d '{"start": "never", "reason": "Version 2 replaces it", "number": 0}'
curl -s -o offered.json -w '%{http_code}\n' -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/requests/leave/form?lang=en'
jq -c '[.[] | keys[0]], .[1].updateDataModel.value' offered.json
curl -s -w ' %{http_code}\n' -H 'X-Person: e0001' 'http://127.0.0.1:8080/a2ui/requests/leave/form?lang=en'
```

```
200
["createSurface","updateDataModel","updateComponents"] {"from":"2027-04-01","home":"/pages/?lang=en"}
{"error":"no such kind of request"} 404
```

**守り。** ほかのサイトが送る POST は、読む前に断ります。人のいないリクエストには 401 を返します。

```sh
curl -s -w ' %{http_code}\n' -H 'X-Person: e1101' -H 'Content-Type: application/json' \
  -H 'Origin: https://elsewhere.example' -d @again.json http://127.0.0.1:8080/a2ui/actions
curl -s -w ' %{http_code}\n' http://127.0.0.1:8080/a2ui/tickets/$T/card
```

```
{"error":"a request from another origin"} 403
{"error":"no one is signed in"} 401
```

**カタログと関数** には人が要りません。レンダラーは ETag でキャッシュできます。

```sh
curl -sI http://127.0.0.1:8080/a2ui/functions.js
```

```
HTTP/1.1 200 OK
Cache-Control: no-cache
Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
Content-Type: text/javascript; charset=utf-8
Etag: "77def6e1fd2448f4e86c19e81ddbb17b4417949f7f94270f8ea16bd7e0d9e380"
Referrer-Policy: same-origin
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
```

サーバーは Ctrl-C で止めます。

## 試験

リポジトリで `go test ./web` を回すと、ファイルのストア、プロキシのヘッダー、小さな部品を確かめます。`examples/` で `go test ./...` を回すと、例の上でルートを確かめます。守り、だれにどのページ、画面、ボタンを見せるか（種類を受け持つ部門は、その種類のチケットを見られる）、名前、言語、1 ページずつの一覧、ディスクと PostgreSQL（`RINGI_TEST_DATABASE`）の添付ファイルと、受け付けなかった送信のファイル、OpenID Connect と開発用のログイン（`web.PicksListed` を超える人を探す）、ページで申請を出し、断られ、承認されること、ページの上で作るベクタ（フォームはブラウザと同じ形で送る）と画面の上で作るベクタ（ボタンはレンダラーが押すように Node で押す。`PATH` に `node` が無ければ飛ばす）です。そこで `go test -tags browser` を回すと、`RINGI_BROWSER` が指すブラウザ（`--headless` と `--remote-debugging-pipe` を受け付ける物）で、ブラウザでしか分からないことを確かめます。止めた送信と送り直し、スクリプトの無いページのフィールドで押した Enter も、その中にあります。
