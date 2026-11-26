# プログラムから API を呼ぶ

ページでできることは、HTTP API でもできます。社内のほかのシステムから申請を出す、承認が済んだかを読む、といったときに使います。英語版は [api.md](api.md) です。

承認が済んだときに知らせを受けるには、API を繰り返し読む代わりに、[承認のあとの処理を始める](flows.ja.md)か[通知の送り方を変える](notices.ja.md)方法を使います。

このページでは、[チュートリアル](start.ja.md)のプロジェクトを使い、申請から承認までを curl で行います。curl と jq が要ります。

申請の操作は、`requests/openapi.json` にすべて書いてあります（OpenAPI 3.1）。ritsu が申請の種類から作るファイルで、入力する項目の型や範囲も入っています。

API では、一件の申請をチケット（`ticket`）と呼びます。申請の種類は、URL や引数では `kind`、応答のチケットの中では `request` です。

## だれの操作になるか

API の操作は、いつも、だれか一人の操作です。サーバーがその人をどう見つけるかは、サーバーの起動のしかたで変わります。

| サーバー | だれの操作になるか |
|---|---|
| フラグなしで起動したサーバー | ヘッダー `X-Person` に書いた人。役割は `X-Roles` にコンマで区切って書く。自分のコンピューターからだけ使える |
| `-dev-login` のサーバー | 試用のログインのページで選んだ人（クッキー） |
| 会社のログインを使うサーバー | ログインした人（ブラウザのクッキー） |
| 自分で組み立てたサーバー | 自分で書いた方法で見つけた人 |
| どれでも、`-api-keys` を付けたとき | プログラムがヘッダー `Authorization` に付けた鍵の人。鍵の無いリクエストは、上のとおりに見つける |

プログラムには、ログインする人がいません。サーバーを `-api-keys` を付けて起動し、プログラムに鍵を渡します（[プログラムから呼ぶ](#プログラムから呼ぶ)）。

ここからは、一つ目の方法で試します。プロジェクトのディレクトリで、フラグなしで起動します。

```sh
go run ./cmd/server
```

## 申請から承認まで

### 申請を出す

営業担当 1（s1201）として、購買を申請します。POST には、JSON の本文と、その操作のための番号（`Idempotency-Key`）を付けます。番号は、操作ごとに違う文字列なら何でもかまいません。

```sh
curl -s -X POST localhost:8080/requests/purchase/tickets -H 'X-Person: s1201' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: buy-1' \
  -d '{"form": {"amount": 120000, "purpose": "新しいメンバーのノートパソコン"}}' > made.json
jq -c '.ticket | {id, status, stage, revision}' made.json
TICKET=$(jq -r '.ticket.id' made.json)
REVISION=$(jq -c '.ticket.revision' made.json)
```

```
{"id":"01M89M0RBPS2H5M8HF082BPSN9","status":"in_progress","stage":"manager","revision":{"edits":0,"round":1,"stage":0}}
```

申請の番号が `id`、いまのステップが `stage` です。`revision` は、申請がどこまで進んだかを表します。次の操作に使います。

### 待っている申請を読んで、承認する

営業一課長（s1101）は、自分を待っている申請の一覧でそれを見つけ、承認します。承認には、読んだときの `revision` を付けます。

```sh
curl -s localhost:8080/waiting -H 'X-Person: s1101' | jq -c '.tickets[] | {id, request, stage}'
curl -s -X POST localhost:8080/tickets/$TICKET/approve -H 'X-Person: s1101' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: ok-1' \
  -d "{\"revision\": $REVISION, \"comment\": \"お願いします\"}" | jq -c '.ticket | {status, stage, revision}'
```

```
{"id":"01M89M0RBPS2H5M8HF082BPSN9","request":"purchase","stage":"manager"}
{"status":"in_progress","stage":"head","revision":{"edits":0,"round":1,"stage":1}}
```

次のステップの営業部長（s1001）に回りました。

### 同じ操作をもう一度送る

通信が切れて、操作が済んだかどうか分からないときは、同じ `Idempotency-Key` でもう一度送ります。実行は一度だけで、最初と同じ応答が返ります。

```sh
curl -s -X POST localhost:8080/tickets/$TICKET/approve -H 'X-Person: s1101' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: ok-1' \
  -d "{\"revision\": $REVISION, \"comment\": \"お願いします\"}" | jq -c '.ticket | {status, stage, revision}'
```

```
{"status":"in_progress","stage":"head","revision":{"edits":0,"round":1,"stage":1}}
```

### 受け付けられないとき

決まりに合わない操作には、409 と理由が返ります。たとえば、営業部長が、課長の承認より前に読んだ `revision` で承認したときです。

```sh
curl -s -X POST localhost:8080/tickets/$TICKET/approve -H 'X-Person: s1001' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: ok-2' \
  -d "{\"revision\": $REVISION}" | jq .
```

```json
{
  "refused": "stale",
  "text": "`approve` was made on the ticket at round 1, stage 1 of the ticket, 0 edits, and it is at round 1, stage 2 of the ticket, 0 edits now: the ticket is to be shown again",
  "text_ja": "`approve` はチケットの 1 回目、1 番目のステップ、直し 0 回のときのものですが、チケットは今 1 回目、2 番目のステップ、直し 0 回です。チケットを見せ直してください",
  "at": null
}
```

`refused` は理由の種類です（[受け付けない理由](refusals.ja.md)）。このときは、申請を読み直し、新しい `revision` で、新しい `Idempotency-Key` を付けて送ります。フォームの値が原因のときは、`at` にその項目が入ります。

```sh
curl -s -X POST localhost:8080/requests/purchase/tickets -H 'X-Person: s1201' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: buy-2' \
  -d '{"form": {"amount": 0, "purpose": "新しいメンバーのノートパソコン"}}' | jq -c '{refused, text_ja, at}'
```

```
{"refused":"form","text_ja":"`0JPY` は、`金額` の範囲（`>=1JPY <=10_000_000JPY`）の外です","at":"amount"}
```

### 申請と履歴を読む

申請を見られる人なら、`GET /tickets/{id}` で、申請と履歴を読めます。ほかの人には 404 を返します。

```sh
curl -s localhost:8080/tickets/$TICKET -H 'X-Person: s1201' | jq -c '.history[] | {did, stage, by, comment}'
curl -s -w ' %{http_code}\n' localhost:8080/tickets/$TICKET -H 'X-Person: s1202'
```

```
{"did":"submitted","stage":null,"by":"s1201","comment":null}
{"did":"approved","stage":"manager","by":"s1101","comment":"お願いします"}
{"error":"no such ticket"} 404
```

## 操作の一覧

| すること | 呼び出し |
|---|---|
| 申請できる種類を並べる | `GET /requests` |
| 自分を待っている申請、自分の申請を並べる | `GET /waiting`、`GET /mine` |
| 受け持つ申請の種類の、すべての申請を並べる | `GET /tickets?kind={kind}` |
| 申請と履歴を読む | `GET /tickets/{id}` |
| 申請する、再申請する | `POST /requests/{kind}/tickets`、`POST /requests/{kind}/tickets/{id}/resubmit` |
| 承認者が入力する項目を書く | `POST /requests/{kind}/tickets/{id}/edit` |
| 承認する、却下する、差し戻す | `POST /tickets/{id}/approve`、`…/reject`、`…/send_back` |
| 承認を取り消す（次のステップがまだ答えていないあいだ） | `POST /tickets/{id}/retract` |
| 承認者を加える、引き継ぐ | `POST /tickets/{id}/add_approver`、`…/hand_over` |
| 保留にする、また進める | `POST /tickets/{id}/pause`、`…/resume` |
| コメントする | `POST /tickets/{id}/comment` |
| 取り下げる、申請をやめる | `POST /tickets/{id}/withdraw`、`…/discard` |
| 組織のデータ、選択肢の一覧を読む | `GET /organization`、`GET /masters/{file}/{master}` |
| 人、チーム、選択肢を言葉で探す | `GET /find/people`、`GET /find/teams`、`GET /find/masters/{file}/{master}` |
| ファイルを添付する | `POST /files`（[ページと画面の README](../web/README.ja.md#curl-で試す)に例がある） |

本文に書くものは `requests/openapi.json` にあります。どの申請の種類も使わない操作は、そこにはありません。だれがどれをできるかは、ページと同じです。

サーバーは、API の全体を `GET /openapi.json` で、サインインしている人に見せます。サーバーが持つすべての申請の種類の操作（いちばん新しい版の物）と、フレームワークが自分で足す操作（ファイルの添付、[種類のすべての申請の一覧](#受け持つ種類のすべての申請を並べる)、[管理](#管理する)）が入っています。サーバーを呼ぶプログラムは、ここから読めます。

```sh
curl -s localhost:8080/openapi.json -H 'X-Person: s1201' | jq -r '.paths | keys[] | select(test("^/admin|/files|^/tickets$"))'
```

```
/admin/kinds
/admin/kinds/{kind}/versions/{version}
/admin/organization
/admin/organization/drafts
/admin/organization/drafts/{id}
/admin/organization/imports
/admin/records
/admin/records.csv
/files
/files/{id}
/tickets
/tickets/{ticket}/files/{id}
```

## 応答

| ステータス | 本文 | いつ |
|---|---|---|
| 200 | 申請の操作なら `{"ticket": …}` | 実行した |
| 400 | `{"error": …}` | 本文が読めない。POST に `Idempotency-Key` がない。同じ番号を、別の操作に使った |
| 401 | `{"error": …}` | だれもログインしていない |
| 403 | `{"error": …}` | ほかのサイトのページを開いたブラウザから送られた |
| 404 | `{"error": …}` | その申請がない、その人は見られない、その申請の種類にいま使える版がない、その URL がない |
| 409 | `{"refused": …, "text": …, "text_ja": …, "at": …}` | 決まりに合わない。`at` は、フォームの原因の項目（行の中なら `items[1].price`）か null |
| 413、415 | `{"error": …}` | 本文が大きすぎる。JSON でない |
| 429 | `{"error": …}` | 多すぎる。待てばよいときは、`Retry-After` に待つ時間がある |
| 500 | `{"error": …}` | サーバーの失敗。理由はサーバーのログにある。同じ番号で送り直してよい |

409 の応答も、番号と一緒に残ります。直して送り直すときは、新しい番号を付けます。番号と応答は 30 日のあいだ残ります。

## 一覧の続きを読む

`GET /waiting`、`GET /mine`、`GET /tickets` は、新しい順に返します。`limit` を付けなければ、一度に 50 件までです。

| 引数 | 意味 |
|---|---|
| `kind` | 申請の種類を一つに絞る（`/tickets` には要る） |
| `status` | 状態を一つに絞る（`/mine` と `/tickets`） |
| `limit` | 一度に返す件数（1〜100） |
| `before` | この番号の申請より古いものを返す |

応答の `more` が `true` なら続きがあります。最後の申請の番号を `before` に入れて、もう一度読みます。

```sh
curl -s 'localhost:8080/mine?limit=1' -H 'X-Person: s1201' | jq -c '{shown: [.tickets[] | [.id, .request, .status]], more}'
```

```
{"shown":[["01M89M0RBPS2H5M8HF082BPSN9","purchase","in_progress"]],"more":false}
```

## 受け持つ種類のすべての申請を並べる

`GET /tickets?kind=` は、ある申請の種類の申請を、だれが出した物でもすべて並べます。並べてもらえるのは、決まり（gate）がまとめて見ることを許す人です。チュートリアルのプロジェクトでは、その種類を受け持つ部の人で、購買の申請は総務部が受け持ちます（`owner general_affairs`）。

```sh
curl -s 'localhost:8080/tickets?kind=purchase' -H 'X-Person: g1201' | jq -c '{shown: [.tickets[] | [.id, .requester, .status]], more}'
curl -s 'localhost:8080/tickets?kind=purchase' -H 'X-Person: s1202' | jq -c '{shown: [.tickets[] | [.id, .requester, .status]], more}'
```

```
{"shown":[["01M89M0RBPS2H5M8HF082BPSN9","s1201","in_progress"]],"more":false}
{"shown":[],"more":false}
```

営業担当 2（s1202）には、空の一覧が返ります。自分の申請と、自分が承認を頼まれた申請は見られますが、ほかの申請は見られません。だれがすべてを見られるかは、gate に書いてあります（[だれが何をできるかを決める](permissions.ja.md)）。この操作はフレームワークが自分で足す物なので、`GET /openapi.json` にあり、`requests/openapi.json` にはありません。

## フォームの途中の値を計算させる

合計の金額、ほかの値によって出る項目、必須になる項目を、申請の前に計算させられます。何も保存しないので、`Idempotency-Key` は要りません。

```sh
curl -s -w '\n' -X POST localhost:8080/requests/purchase/worked -H 'X-Person: s1201' \
  -H 'Content-Type: application/json' -d '{"form": {"amount": 120000}}'
```

```
{"fields":{}}
```

購買の申請には計算する項目がないので、空です。[合計のある経費の例](../examples/project/expense.ja.ringi)では、`{"fields":{"amount":{"value":64000},"receipt":{"required":true},"trip_to":{"shown":true}}}` のように返ります。承認者が入力するときは、`POST /requests/{kind}/tickets/{id}/worked` を使います。

## 人やチームを探す

人を選ぶ項目に入れる値を、名前の一部から探せます。近い順に返ります。

```sh
curl -s -w '\n' -G localhost:8080/find/people --data-urlencode 'q=営業' -d 'limit=2&lang=ja' -H 'X-Person: s1201'
```

```
{"found":[{"value":"s1001","name":"営業部長","label":"営業部長（s1001）"},{"value":"s1101","name":"営業一課長","label":"営業一課長（s1101）"}],"more":true}
```

## 社員の代わりに申請する

申請の代理になっている人は、本文の `requester` に本人の ID を書いて、代わりに申請できます。代理は、組織のデータの `proxies.csv` に書きます（[代理を決める](organization.ja.md#代理を決める)）。ここでは、営業担当 2 を営業担当 1 の代理にします。

```csv title="project/org/proxies.csv"
person,proxy,kind,from,to,requests
s1201,s1202,submit,2026-04-01,,purchase
```

サーバーを起動し直し、営業担当 2 として申請します。

```sh
go run ./cmd/server
```

```sh
curl -s -X POST localhost:8080/requests/purchase/tickets -H 'X-Person: s1202' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: for-1' \
  -d '{"requester": "s1201", "form": {"amount": 30000, "purpose": "課の本"}}' | jq -c '.ticket | {requester, made_by, stage}'
```

```
{"requester":"s1201","made_by":"s1202","stage":"manager"}
```

承認者は、申請者（`requester`）の所属から決まります。プログラムに代わりをさせるときは、プログラムのための人を組織のデータに作り、その人を代理にします。

## 管理する

管理のページですることも、API でできます。役割を持つ人として操作します。

| すること | 呼び出し |
|---|---|
| 申請の種類と、その版を並べる | `GET /admin/kinds` |
| 版を始める日を変える、止める | `POST /admin/kinds/{kind}/versions/{version}` |
| 組織のデータを送って確かめる、確かめた結果を読む | `POST /admin/organization/drafts`、`GET /admin/organization/drafts/{id}` |
| 確かめたデータを取り込む | `POST /admin/organization/imports` |
| 記録を読む | `GET /admin/records`、`GET /admin/records.csv` |

購買の申請を止めてみます。`number` には、一覧にある版の `start.number` を書きます。だれかが先に切り替えていたら番号が合わないので、受け付けません。

```sh
curl -s localhost:8080/admin/kinds -H 'X-Person: s1001' -H 'X-Roles: system_admin' |
  jq -c '.kinds[] | {name, current, versions: [.versions[] | {version, start: .start.how, number: .start.number}]}'
curl -s -X POST localhost:8080/admin/kinds/purchase/versions/1 -H 'X-Person: s1001' -H 'X-Roles: system_admin' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: switch-1' \
  -d '{"start": "never", "reason": "決まりを見直す", "number": 0}' | jq -c '.record | {what, kind, version, person, reason}'
curl -s -w ' %{http_code}\n' -X POST localhost:8080/requests/purchase/tickets -H 'X-Person: s1201' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: buy-3' -d '{"form": {"amount": 120000, "purpose": "机"}}'
```

```
{"name":"purchase","current":1,"versions":[{"version":1,"start":"description","number":0}]}
{"what":"kind","kind":"purchase","version":1,"person":"s1001","reason":"決まりを見直す"}
{"error":"no version of purchase is in effect now, and none is to start"} 404
```

進行中の申請は、そのまま最後まで進みます。このサーバーは何もかもメモリに置いているので、起動し直すと元に戻ります。

## プログラムから呼ぶ

プログラムには、ログインする人がいません。また、会社のログインを使うサーバーは `X-Person` を受け付けません。そこで、プログラムごとに鍵を渡します。鍵をヘッダー `Authorization` に付けたリクエストは、その鍵の人の操作になり、役割は鍵の行に書いたものになります。サーバーが持つのは鍵の SHA-256 だけで、`-api-keys` に渡すファイルに書きます。

鍵を作ります。一行目に出るのが鍵で、プログラムに渡します。二行目はその SHA-256 で、サーバーに渡します。

```sh
KEY=$(head -c 32 /dev/urandom | base64 | tr -d '=+/')
echo "$KEY"
printf %s "$KEY" | shasum -a 256 | cut -d ' ' -f 1
```

Linux では、`shasum -a 256` の代わりに `sha256sum` も使えます。

ファイルには、鍵ごとに一行ずつ、鍵の人の ID、鍵の SHA-256、役割があればコンマで区切った役割を、空白で区切って書きます。`#` で始まる語から行の終わりまでは、コメントです。この鍵は、営業担当 1 の物にします。

```sh
printf 's1201 %s # 購買のシステム\n' "$(printf %s "$KEY" | shasum -a 256 | cut -d ' ' -f 1)" > api-keys.txt
```

このファイルを付けて、サーバーを起動します。鍵は、人がどの方法でログインするサーバーでも同じように使えます。ここでは、このページのほかの所と同じく、`X-Person` も受け付けます。

```sh
go run ./cmd/server -api-keys api-keys.txt
```

```
level=INFO msg="programs may call the API with the keys of -api-keys" keys=1
```

プログラムは、鍵を付けて送ります。ファイルに無い鍵には、401 を返します。

```sh
curl -s -X POST localhost:8080/requests/purchase/tickets -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: buy-4' \
  -d '{"form": {"amount": 120000, "purpose": "新しいメンバーのノートパソコン"}}' | jq -c '.ticket | {requester, status, stage}'
curl -s -w ' %{http_code}\n' localhost:8080/mine -H 'Authorization: Bearer not-a-key'
```

```
{"requester":"s1201","status":"in_progress","stage":"manager"}
{"error":"no one is signed in"} 401
```

鍵がその人の物として通るのは、その日の組織のデータにその人がいるあいだだけです。組織を離れた人の鍵にも、401 を返します。プログラムができるのは、鍵の人ができることだけです。プログラムに専用の人を持たせるなら、[社員の代わりに申請する](#社員の代わりに申請する)ときと同じく、組織のデータに足します。サーバーは、ファイルを起動のときに読みます。鍵を替えるときは、新しい鍵の行を足して起動し直し、プログラムが新しい鍵を使うようになったら、古い行を消して、もう一度起動し直します。
