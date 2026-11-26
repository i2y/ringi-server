# ページ、画面、添付ファイルを HTTP で出す（web）

パッケージ `web` は、ringi のサーバーのうち、ブラウザが使う部分です。次の三つを出し、ほかのリクエストはサーバーの API に渡します。

- 人が申請し、承認し、自分の申請を見るページ
- ritsu が作った画面（A2UI）にサーバーのデータを入れたものと、そのボタンの操作
- 人が添付するファイル

どのリクエストにも、ブラウザに必要なことをまとめて行います。だれのリクエストかを見つけ、ほかのサイトから送られたものを拒否し、大きさを制限し、ヘッダーを付けます。英語版は [README.md](README.md) です。

| したいこと | 読むもの |
|---|---|
| `ritsu ringi init` で作ったサーバーを動かす | [動かして、申請を承認する](../docs/start.ja.md)。パッケージ `serve` がこのパッケージを使うので、自分で呼ぶものはありません |
| Go でサーバーを組み立てる | [自分のプログラムに組み込む](../docs/embedding.ja.md)と、下の[使い方](#使い方) |
| API や A2UI で自分の画面を作る | [自分の画面を作る](../docs/screens.ja.md) |
| 送るものと返るものを見る | 下の[curl で試す](#curl-で試す) |
| しくみとその理由を知る | [設計](DESIGN.ja.md) |

## 使い方

```go
srv, err := server.New(server.Config{…, Files: store.Files()}) // または web.NewDiskFiles(dir)
h, err := web.New(srv, web.Config{
	Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout", // ページで使う、ログインのフック
})
signIn.Mount(h) // ログインのルート。同じ検査の後ろに置く
(&http.Server{Addr: ":8080", Handler: h, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe()
```

だれのリクエストかは、サーバーの `Authenticator` が見つけます。セッションを持つログイン（パッケージ [`oidc`](../oidc/README.ja.md)）では、`Config.Require` が、セッションのないブラウザをログインに送り、済んだら元のページへ戻します。どのページにも、ログアウトのボタンがあります。人をログインさせるプロキシの後ろでは、`web.ProxyHeader` が、プロキシの付けたヘッダーから人を取ります（[使い方](../docs/embedding.ja.md#3-プロキシがログインさせる)）。

## ルート

### ページ

| ルート | 返すもの |
|---|---|
| `GET /` | `/pages/` へのリダイレクト |
| `GET /pages/` | ホーム。自分を待っているもの、出せる申請、自分の申請と、それぞれの件数。すべての申請を見られる種類（その種類を受け持つ部） |
| `GET`、`POST /pages/requests/{kind}/new` | 新しい申請のフォームと、その送信 |
| `GET`、`POST /pages/tickets/{id}` | 申請と、その人ができる操作のボタン。その送信 |
| `GET`、`POST /pages/tickets/{id}/form` | 差し戻された申請を再申請するフォームと、その送信 |
| `GET /pages/tickets/{id}/history` | 申請の履歴 |
| `GET /pages/waiting/{kind}`、`/pages/mine/{kind}` | その種類の申請のうち自分を待っているもの、その種類の自分の申請。新しい順に 1 ページ 50 件 |
| `GET /pages/requests/{kind}` | その種類のすべての申請。gate がまとめて見ることを許す人に。新しい順に 1 ページ 50 件 |
| `GET /pages/assets/{name}` | ページのスタイルシートとスクリプト、ritsu の関数 |

### A2UI の画面

| ルート | 返すもの |
|---|---|
| `GET /a2ui/requests/{kind}/form` | 新しい申請のフォーム |
| `GET /a2ui/tickets/{id}/card`、`…/form`、`…/history` | 申請の画面 |
| `GET /a2ui/waiting/{kind}`、`/a2ui/mine/{kind}`、`/a2ui/requests/{kind}` | ページと同じ一覧。`?before=` の次から `?limit=` 件（指定がなければ 50 件、最大 100 件） |
| `POST /a2ui/actions` | ボタンの操作を行い、そのあとの画面を返す |
| `GET /a2ui/find/people`、`/a2ui/find/teams`、`/a2ui/find/masters/{file}/{master}` | 値を選ぶフィールドのために、名前か ID の一部で探した値（[設計](DESIGN.ja.md)の 5.1） |
| `GET /a2ui/catalog.json`、`/a2ui/functions.js` | ritsu のカタログと関数 |

### 添付ファイル

| ルート | 返すもの |
|---|---|
| `POST /files` | 受け取った添付ファイルの ID |
| `GET /files/{id}` | 添付ファイル。アップロードした本人だけ |
| `GET /tickets/{ticket}/files/{id}` | 申請の添付ファイル。その申請を見られる人だけ |

## ページの応答

ページはふつうの HTML で、スクリプトがなくても、申請し、承認し、差し戻せます。スクリプトは、スクリプトにしかできないことを足します。入力に合わせて合計を計算し、送る前に直すところを示し、サーバーとやり取りせずに行を足します。

フォームは、それを表示したページに送ります。ハンドラーは、それを API の操作にして、その人として行います。テキストとコメントは、両端の空白を除きます。

| 送信が | ブラウザに返るもの |
|---|---|
| 受け付けられた | 申請のページ（303）。再読み込みしても、もう一度は送られない |
| ページの検査で止まった | 入力した値と理由を入れた、同じページ（422） |
| サーバーに拒否された | 理由をページの言語で、該当するフィールドかページの先頭に示した、同じページ（409）。表示のあとに申請が変わっていれば、今の内容で表示する |
| スクリプトなしで、フィールドで押した Enter | 何もなし（204）。ページは、入力した値もフォーカスも、そのまま |

## 言語

ページの言語は英語か日本語、画面の言語はその申請の種類が持つ言語です。次の順に、最初に見つかったものを使います。

1. `?lang=` で選んだもの（ページではクッキーが覚える）
2. ブラウザの言語（`Accept-Language`）
3. 組織のデータにあるその人の言語（`people.csv` の列 `language`）
4. 申請の種類の言語（`server.LanguageChoice`）

## curl で試す

`examples/` で、添付ファイルをディレクトリに置いて、例のサーバーを起動します。

```sh
go run ./cmd/server -files ./files
```

だれのリクエストかはヘッダー `X-Person` で決まります。自分のコンピューターで試すためのもので、本人を確かめません（[ログインの三つの方法](../docs/sign-in.ja.md#ログインの三つの方法)）。ページそのものを見るには、サーバーに `-dev-login` を付けて起動し、<http://127.0.0.1:8080/> を開きます（[試す](../README.ja.md#試す)）。下のコマンドは、別の端末の、自分のディレクトリで実行します。ID は毎回変わります。

### ページ

スタイルシートとスクリプトを、サーバーから、専用のポリシーで読み込みます。

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

```

### ほかのサイトが送るフォーム

読む前に拒否し、そう伝えるページを返します。

```sh
curl -s -o /dev/null -w '%{http_code}\n' -H 'X-Person: e1201' -H 'Origin: https://elsewhere.example' \
  --data-urlencode '_do=button.submit' 'http://127.0.0.1:8080/pages/requests/expense/new?lang=en'
```

```
403
```

### 新しい申請のフォーム

営業一課の担当（e1201）が、経費のフォームの画面を開きます。返るのは、レンダラーが受け取る順に並んだ A2UI のメッセージです。`can` は、フォームに出るボタンです。

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/requests/expense/form?lang=en' \
  | jq -c '[.[] | keys[0]], .[1].updateDataModel.value.can'
```

```
["createSurface","updateDataModel","updateComponents"]
{"discard":false,"resubmit":false,"submit":true}
```

フォームのデータには、ハンドラーがアドレスを二つ入れます。値を選ぶフィールドが探す `find`（`/a2ui/find`）と、添付ファイルのフィールドが送る `upload`（`/files`）です。

### 人を探す

レンダラーの `PersonPicker` は、フォームの `find` の下で、名前の一部から人を探します。どちらの言語の名前でもかまいません。ページも、人が 1,000 人（`web.PicksListed`）を超えると、同じように探します。

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/find/people?q=staff+3&lang=en' | jq -c .
curl -s -G -H 'X-Person: e1201' --data-urlencode 'q=営業 3' --data-urlencode 'lang=ja' \
  http://127.0.0.1:8080/a2ui/find/people | jq -c .
```

```
{"found":[{"value":"e1203","name":"Sales staff 3","label":"Sales staff 3 (e1203)"}],"more":false}
{"found":[{"value":"e1203","name":"営業担当 3","label":"営業担当 3（e1203）"},{"value":"e3101","name":"営業一部長","label":"営業一部長（e3101）"}],"more":false}
```

二つ目では e3101 も見つかります。どの言葉も、名前か ID に含まれるためです（`e3101` の `3`）。人とチームを探せるのは、API と同じく、組織の人だけです。

```sh
curl -s -w ' %{http_code}\n' -H 'X-Person: x9999' 'http://127.0.0.1:8080/a2ui/find/people?q=staff+3&lang=en'
```

```
{"error":"the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-18"} 404
```

### 領収書をアップロードする

フォームの添付ファイルのフィールドは、フォームの `upload` にアップロードし、返った `id` をフォームに入れます。

```sh
printf '%%PDF-1.4\n%% a receipt (fictional)\n' > receipt.pdf
curl -s -H 'X-Person: e1201' -F 'file=@receipt.pdf;type=application/pdf' http://127.0.0.1:8080/files | tee uploaded.json | jq .
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

### 申請する

ここでは API から申請します。フォームの申請のボタンも、同じ操作をします。

```sh
F=$(jq -r .id uploaded.json)
cat > request.json <<EOF
{"form": {"category": "purchase", "items": [{"what": "A second monitor", "price": 32000}],
          "purpose": "A monitor for the desk", "used_on": "2026-11-16", "receipt": "$F"},
 "comment": "The receipt is attached."}
EOF
curl -s -H 'X-Person: e1201' -H 'Idempotency-Key: monitor-1' -H 'Content-Type: application/json' \
  -d @request.json http://127.0.0.1:8080/requests/expense/tickets | tee made.json \
  | jq -c '{id: .ticket.id, stage: .ticket.stage, approvers: .ticket.stages[0].parts[0].approvers}'
```

```
{"id":"01M7KSHBTFEM304GVA00FCBYEH","stage":"first","approvers":["e1101"]}
```

フォームに入れられるファイルは、チケットがすでに持つものと、送る本人がアップロードしたものだけです。e1101 がアップロードしたファイルを入れると、存在しない ID と同じく 400 になります。

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

### 承認者のカード

営業一課長（e1101）が、チケットのカードを日本語で開きます。ボタンは e1101 が今押せるもので、名前は画面の言語です。領収書へは、チケットを通してリンクします。

```sh
T=$(jq -r .ticket.id made.json)
curl -s -H 'X-Person: e1101' "http://127.0.0.1:8080/a2ui/tickets/$T/card?lang=ja" | tee card.json \
  | jq -c '.[1].updateDataModel.value | {can, people, files}'
```

```
{"can":{"add_approver":false,"approve":true,"comment":true,"discard":false,"edit":true,"hand_over":false,"reject":true,"send_back":true,"withdraw":false},"people":{"e1101":"営業一課長","e1201":"営業担当 1"},"files":{"910972bb8b22fc6b54a795faa6d008fb":"/tickets/01M7KSHBTFEM304GVA00FCBYEH/files/910972bb8b22fc6b54a795faa6d008fb"}}
```

### 領収書を開く

PDF はブラウザで表示されます。表示しない種類のファイルは保存になり、応答には CSP の `sandbox` が付きます（ここでは `Date` ヘッダーを省いています）。

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

チケットに付く前のファイルを取得できるのは、アップロードした本人だけです。付いたあとは、そのチケットを見られる人が、チケットを通して取得できます。ほかの人には、存在しないファイルと同じ 404 です。

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

### カードで承認する

承認のボタンを押したときにレンダラーが送るメッセージです。`revision` は、カードに表示されているものです。

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

ボタンがあったサーフェスは削除され、同じサーフェスに新しいカードが入ります。同じメッセージをもう一度送っても（ダブルクリックや再送）、同じ操作です。応答は同じで、チケットは一度だけ、二つ目のステップへ進んでいます。

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

### 拒否

古いカードで、別のコメントを付けてもう一度押すと、別の操作になり、e1101 はもう承認できません。`words` は本人に示す文で、画面の言語です。カードは今の内容で返ります。承認のボタンはなく、`refused` に拒否の内容があり、入力したコメントが戻っています。

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

### 自分の申請

新しい順に、1 ページ 50 件です。`?limit=` で 1〜100 件にでき、`?before=` は前のページの最後のチケットです。各行の `made_by` は、申請者の代わりに申請を出した人で、申請者が自分で出したときは null です。

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/mine/expense?lang=en' \
  | jq -c '[.[] | keys[0]], [.[1].updateDataModel.value.tickets[] | {id, status, stage}]'
```

```
["createSurface","updateDataModel","updateComponents"]
[{"id":"01M7KSHBTFEM304GVA00FCBYEH","status":"in_progress","stage":"second"}]
```

### 有効な版がない申請の種類

e6001 が休暇の第 1 版を止めます（[管理する](../docs/api.ja.md#管理する)）。第 2 版は 2027-04-01 からなので、それまで、新しい休暇の申請にはフォームがありません。その日から出せる人には、日付とホームへの戻り先を示す画面を返します。ほかの人には、そのような種類は存在しません。

```sh
curl -s -o /dev/null -X POST http://127.0.0.1:8080/admin/kinds/leave/versions/1 -H 'X-Person: e6001' \
  -H 'X-Roles: workflow_admin' -H 'Content-Type: application/json' -H 'Idempotency-Key: off-1' \
  -d '{"start": "never", "reason": "Version 2 replaces it", "number": 0}'
curl -s -o offered.json -w '%{http_code}\n' -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/requests/leave/form?lang=en'
jq -c '[.[] | keys[0]]' offered.json
jq -c '.[1].updateDataModel.value' offered.json
curl -s -w ' %{http_code}\n' -H 'X-Person: e0001' 'http://127.0.0.1:8080/a2ui/requests/leave/form?lang=en'
```

```
200
["createSurface","updateDataModel","updateComponents"]
{"from":"2027-04-01","home":"/pages/?lang=en"}
{"error":"no such kind of request"} 404
```

### ほかのサイトからの送信と、だれのものでもないリクエスト

ほかのサイトの POST は、読む前に拒否します。だれのものでもないリクエストには、画面は 401 を返します。

```sh
curl -s -w ' %{http_code}\n' -H 'X-Person: e1101' -H 'Content-Type: application/json' \
  -H 'Origin: https://elsewhere.example' -d @again.json http://127.0.0.1:8080/a2ui/actions
curl -s -w ' %{http_code}\n' http://127.0.0.1:8080/a2ui/tickets/$T/card
```

```
{"error":"a request from another origin"} 403
{"error":"no one is signed in"} 401
```

### カタログと関数

だれでも取得できます。レンダラーは ETag でキャッシュします。

```sh
curl -sI http://127.0.0.1:8080/a2ui/functions.js
```

```
HTTP/1.1 200 OK
Cache-Control: no-cache
Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
Content-Type: text/javascript; charset=utf-8
Etag: "c6b49574b07eda4dc3b9320ba76b4eee19c0854e01d8584f690b797ae3964426"
Referrer-Policy: same-origin
X-Content-Type-Options: nosniff
X-Frame-Options: DENY

```

サーバーは Ctrl-C で止めます。

## 試験

`go test ./web` は、ファイルの保存先、プロキシのヘッダー、小さな部品を確かめます。`examples/` の `go test ./...` は、例でルートを確かめます。だれにどのページ、画面、ボタンが出るか、何を拒否するか、言語、ページに分けた一覧、添付ファイル、ログイン、ページと画面で行うベクタです。要るものと結果、ブラウザでの試験は、[フレームワークを直す、自分のサーバーを試験する](../CONTRIBUTING.ja.md)にあります。
