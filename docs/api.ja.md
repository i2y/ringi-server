# API を curl で試す

申請を出してから決まるまでを HTTP API で一通り行い、そのあと、応答、一覧、検索、管理を順に見ます。コマンドは例のサーバーで動かします。自分のサーバーにも、自分の申請の種類について同じパスがあります。一つ一つの説明は、`ritsu gen` が書く `requests/openapi.json` にあります。英語版は [api.md](api.md) です。

API では、出された申請をチケット（ticket）、申請の種類を kind と呼びます。

## サーバーを起動する

`examples/` で実行します。Go 1.26 以降、curl、jq が要ります。

```sh
go run ./cmd/server
```

申請はメモリに置きます。だれのリクエストかは、ヘッダー `X-Person`（組織のデータにある人の ID）で決まり、役割は `X-Roles` で渡します。本人を確かめないので、サーバーは、自分のコンピューターのアドレスで、そこへ送られたリクエストにだけ使います。ほかのサイトのページがブラウザにリクエストを送らせても、だれのリクエストにもなりません。人に使ってもらうサーバーでは、同じ呼び出しにセッションのクッキーを付けます（[ログイン](../README.ja.md#ログイン)）。

## 申請から決定まで

[drive.sh](../examples/drive.sh) は、経費を申請して、決まるまで進めます。別の端末で、`examples/` で実行します。

```sh
./drive.sh
```

```
1. What e1201 (on the sales staff of Section 1A) may submit:
   account  Account request
   account_ja  アカウントの申請
   contract  Contract review
   contract_ja  契約の確認
   expense  Expense request
   expense_ja  経費の申請
   leave  Leave request
   leave_ja  休暇の申請
   purchase  Purchase ringi
   purchase_ja  購買の稟議
2. e1201 attaches the receipts, and asks for a trip to Fukuoka, 118,000 yen:
   ticket 01M89783QJ6FRF0K259NXW4W9H: in_progress, at the stage first
3. What e1101 (the manager of Section 1A) waits for:
   01M89783QJ6FRF0K259NXW4W9H  expense  first
4. e1101 approves it:
   in_progress, at the stage decide
5. e3101 (the head of Sales Department 1) approves what was shown before e1101's approval:
   refused, stale: `approve` was made on the ticket at round 1, stage 1 of the ticket, 0 edits, and it is at round 1, stage 2 of the ticket, 0 edits now: the ticket is to be shown again
6. e3101 decides it on what is shown now:
   approved
7. The ticket's history, as e1201 sees it:
   submitted  e1201  
   approved  e1101  
   approved  e3101  Have a good trip
   ticket approved    
8. e1201 asks for a day trip of 12,000 yen, which the agent decides:
   approved: submitted, agent asked, agent ok, ticket approved
```

営業担当 1（e1201）が領収書を付けて福岡への出張を申請し、営業一課長（e1101）が承認し、営業一部長（e3101）が決めます。5 では、部長が、課長の承認より前に見た内容で承認しようとしたので、`stale` で拒否されています。8 は、エージェントが決める日帰りの出張です。

サーバーのログには、保存のあとに外へ出たものが残ります。通知（自分のサーバーならメールを送るところ）、承認された経費が始める flow `pay`、日帰りの出張を決めたエージェントです。通知は、受け取る人の言語で書きます。組織のデータにその人の言語があればそれで、なければ申請の種類の言語です。

```
level=INFO msg=notice notice=waiting to="e1101 e1101@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M89783QJ6FRF0K259NXW4W9H：承認待ち body=経費の申請（01M89783QJ6FRF0K259NXW4W9H）は、あなたの承認を待っています（ステップ：first）。 ticket=01M89783QJ6FRF0K259NXW4W9H stage=first
level=INFO msg=notice notice=waiting to="e3101 e3101@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M89783QJ6FRF0K259NXW4W9H：承認待ち body=経費の申請（01M89783QJ6FRF0K259NXW4W9H）は、あなたの承認を待っています（ステップ：decide）。 ticket=01M89783QJ6FRF0K259NXW4W9H stage=decide
level=INFO msg="flow launched" flow=pay kind=expense ticket=01M89783QJ6FRF0K259NXW4W9H run=01M89783QJ6FRF0K259NXW4W9H/1/17
level=INFO msg=notice notice=decided to="e1201 e1201@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M89783QJ6FRF0K259NXW4W9H：結果 body=経費の申請（01M89783QJ6FRF0K259NXW4W9H）は、結果が出ました。 ticket=01M89783QJ6FRF0K259NXW4W9H stage=""
level=INFO msg="agent asked" flow=travel_decision kind=expense ticket=01M89783SSWWJ5M5EA80VAPNNH stage=decide note=false
level=INFO msg="agent answered" flow=travel_decision ticket=01M89783SSWWJ5M5EA80VAPNNH verdict=ok outcome=ok
level=INFO msg="flow launched" flow=pay kind=expense ticket=01M89783SSWWJ5M5EA80VAPNNH run=01M89783SSWWJ5M5EA80VAPNNH/1/12
level=INFO msg=notice notice=decided to="e1201 e1201@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M89783SSWWJ5M5EA80VAPNNH：結果 body=経費の申請（01M89783SSWWJ5M5EA80VAPNNH）は、結果が出ました。 ticket=01M89783SSWWJ5M5EA80VAPNNH stage=""
```

サーバーを `-verdict doubt` で起動すると、エージェントは日帰りの出張を決めずに、課長へ回します。

## 一つずつ呼ぶ

e1203 が休暇を申請します。POST には、`application/json` の本文と、その操作のキー（`Idempotency-Key`）を付けます。同じキーでもう一度送っても、実行は一度だけで、同じ応答が返ります。

```sh
curl -s -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e1203' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: one-1' \
  -d '{"form": {"kind": "paid", "first_day": "2026-12-28", "days": 2, "reason": "A trip home"}}' > made.json
jq -c '.ticket | {id, status, stage, revision}' made.json
TICKET=$(jq -r '.ticket.id' made.json)
REVISION=$(jq -c '.ticket.revision' made.json)
```

```
{"id":"01M89785T0X2VT6RJDS6BBBKV7","status":"in_progress","stage":"manager","revision":{"edits":0,"round":1,"stage":0}}
```

e1101 は、自分を待っているものの中にその申請を見つけ、見た内容（`revision`）のまま承認します。

```sh
curl -s localhost:8080/waiting -H 'X-Person: e1101' | jq -c '.tickets[] | {id, request, stage}'
curl -s -X POST localhost:8080/tickets/$TICKET/approve -H 'X-Person: e1101' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: one-2' \
  -d "{\"revision\": $REVISION, \"comment\": \"Enjoy\"}" | jq -c '.ticket | {status, revision}'
```

```
{"id":"01M89785T0X2VT6RJDS6BBBKV7","request":"leave","stage":"manager"}
{"status":"approved","revision":{"edits":0,"round":1,"stage":null}}
```

同じ呼び出しを同じキーで送ると、最初と同じ応答が返り、何も変わりません。

```sh
curl -s -X POST localhost:8080/tickets/$TICKET/approve -H 'X-Person: e1101' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: one-2' \
  -d "{\"revision\": $REVISION, \"comment\": \"Enjoy\"}" | jq -c '.ticket | {status, revision}'
```

```
{"status":"approved","revision":{"edits":0,"round":1,"stage":null}}
```

規則に合わないものは、409 で拒否されます。どの規則か、その理由、フォームのどこかが返ります。

```sh
curl -s -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e1203' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: one-3' \
  -d '{"form": {"kind": "paid", "first_day": "2026-12-28", "days": 0}}' | jq .
```

```json
{
  "refused": "form",
  "text": "`0` is outside the range of `days` (`>=1 <=20`)",
  "text_ja": "`0` は、`days` の範囲（`>=1 <=20`）の外です",
  "at": "days"
}
```

### 応答

| ステータス | 本文 | いつ |
|---|---|---|
| 200 | チケットの操作なら `{"ticket": …}` | 実行した |
| 400 | `{"error": …}` | 本文が読めない。POST にキーがない。キーが別の操作に使われている |
| 401 | `{"error": …}` | だれもログインしていない |
| 404 | `{"error": …}` | そのチケットがない。または、その人は見られない |
| 409 | `{"refused": …, "text": …, "text_ja": …, "at": …}` | 規則に合わない。`at` はフォームのどこか（行の中なら `items[1].price`）、または null |
| 413、415 | `{"error": …}` | 本文が大きすぎる。JSON でない |
| 429 | `{"error": …}` | 多すぎる。待てば済むときは `Retry-After` に時間がある |
| 500 | `{"error": …}` | サーバーの失敗。理由はログにある |

拒否も、キーと一緒に残ります。同じキーで送ると同じ拒否が返るので、直して送るときは別のキーを付けます。すべての場合は、[設計](../DESIGN.ja.md)の 7.1 にあります。

### 操作

| すること | 呼び出し |
|---|---|
| 出せる申請の種類、自分を待っているもの、自分の申請を並べる | `GET /requests`、`GET /waiting`、`GET /mine` |
| チケットとその履歴を読む | `GET /tickets/{id}` |
| 申請を出す、差し戻された申請を出し直す | `POST /requests/{kind}/tickets`、`POST /requests/{kind}/tickets/{id}/resubmit` |
| 承認者が入れるフィールドに入力する、直す | `POST /requests/{kind}/tickets/{id}/edit` |
| ステップで答える | `POST /tickets/{id}/approve`、`…/reject`、`…/send_back` |
| 自分の承認を取り消す（次のステップがまだ答えていないあいだ） | `POST /tickets/{id}/retract` |
| 承認者を足す、自分の番をほかの人に渡す | `POST /tickets/{id}/add_approver`、`…/hand_over` |
| コメントする | `POST /tickets/{id}/comment` |
| チケットの期限を止める、また動かす | `POST /tickets/{id}/pause`、`…/resume` |
| 申請を取り下げる、差し戻された申請を破棄する | `POST /tickets/{id}/withdraw`、`…/discard` |
| 組織、マスタの行を読む | `GET /organization`、`GET /masters/{file}/{master}` |
| ファイルを添付する | `POST /files`（[curl で試す](../web/README.ja.md#curl-で試す)） |

それぞれをだれができるかは、gate と申請の種類が決めます。[ページと画面](../web/README.ja.md)も、[エージェントのツール](../mcp/README.ja.md)も、同じ操作を実行します。

## 一覧

`GET /waiting` は自分を待っているチケットを、`GET /mine` は自分の申請を、新しい順に返します。`kind` で申請の種類を、`/mine` では `status` で状態を一つに絞れます。一ページは `limit` 件（1〜100、指定がなければ 50）です。続きがあるかは `more` に、次のページは、最後のチケットの ID を `before` に入れて読みます。

```sh
curl -s 'localhost:8080/mine?limit=1' -H 'X-Person: e1201' | jq -c '{shown: [.tickets[] | [.id, .request, .status]], more}'
LAST=$(curl -s 'localhost:8080/mine?limit=1' -H 'X-Person: e1201' | jq -r '.tickets[-1].id')
curl -s "localhost:8080/mine?limit=1&before=$LAST" -H 'X-Person: e1201' | jq -c '{shown: [.tickets[] | [.id, .request, .status]], more}'
```

```
{"shown":[["01M89783SSWWJ5M5EA80VAPNNH","expense","approved"]],"more":true}
{"shown":[["01M89783QJ6FRF0K259NXW4W9H","expense","approved"]],"more":false}
```

## フォームが計算するもの

`POST /requests/{kind}/worked` は、ここまでに入力した値から、フォームが計算するものを返します。ほかの値しだいで出るフィールドや必須になるフィールドが今どうか、合計などの値（まだ決まらないあいだは null）です。ページと画面も同じものを使います。何も保存しません。承認者が直すときは `POST /requests/{kind}/tickets/{id}/worked` です。

```sh
curl -s -w '\n' -X POST localhost:8080/requests/expense/worked -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -d '{"form": {"category": "travel", "items": [{"what": "Flight to Fukuoka and back", "price": 64000}]}}'
curl -s -w '\n' -X POST localhost:8080/requests/expense/worked -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -d '{"form": {"category": "purchase", "items": [{"what": "A desk lamp", "price": 4800}]}}'
```

```
{"fields":{"amount":{"value":64000},"receipt":{"required":true},"trip_to":{"shown":true}}}
{"fields":{"amount":{"value":4800},"receipt":{"required":false},"trip_to":{"shown":false}}}
```

## 人、チーム、行を探す

`GET /find/people`、`GET /find/teams`、`GET /find/masters/{file}/{master}` は、フォームで選ぶ値を言葉で探し、近い順に返します。引数は `q`（言葉）、`limit`（1〜100、指定がなければ 20）、`lang`（`en` か `ja`）です。マスタの行には、選ぶ日の `on` もあります。

```sh
curl -s -w '\n' -G localhost:8080/find/people --data-urlencode 'q=営業' -d 'limit=3&lang=ja' -H 'X-Person: e1201'
curl -s -w ' %{http_code}\n' 'localhost:8080/find/people?q=sales' -H 'X-Person: x9999'
```

```
{"found":[{"value":"e0101","name":"営業本部長","label":"営業本部長（e0101）"},{"value":"e1101","name":"営業一課長","label":"営業一課長（e1101）"},{"value":"e1201","name":"営業担当 1","label":"営業担当 1（e1201）"}],"more":true}
{"error":"the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-25"} 404
```

人とチームを探せるのは、二つ目の応答のとおり、組織の人だけです。

## 管理

管理者は、申請の種類の版を切り替え、組織の新しいデータを取り込み、その記録を、チケットごとの操作と一緒に読みます。`/pages/admin/` の下のページでも、API でもできます。だれが何をできるかは gate が決めます。例では、`workflow_admin` は自分の部署が持つ申請の種類を、`system_admin` はすべてを管理し、`auditor` はすべての記録を読めて、何も変えられません。下の番号が最初の状態になるよう、サーバーを起動し直します。

```sh
go run ./cmd/server
```

| すること | 呼び出し |
|---|---|
| 切り替えられる申請の種類と、その版を並べる | `GET /admin/kinds` |
| 版を切り替える | `POST /admin/kinds/{kind}/versions/{version}` |
| 組織の新しいデータを確かめ、変わるところを読む | `POST /admin/organization/drafts`、`GET /admin/organization/drafts/{id}` |
| 確かめたデータを取り込む | `POST /admin/organization/imports` |
| 記録を読む | `GET /admin/records`、`GET /admin/records.csv` |

人事部長の e6001 が、切り替えられるものを並べ、`leave` の第 2 版を 2027-05-01 からにし、記録を CSV で読みます。

```sh
curl -s localhost:8080/admin/kinds -H 'X-Person: e6001' -H 'X-Roles: workflow_admin' |
  jq -c '.kinds[] | {name, current, versions: [.versions[] | {version, usable, start: .start.how}]}'
curl -s -X POST localhost:8080/admin/kinds/leave/versions/2 -H 'X-Person: e6001' -H 'X-Roles: workflow_admin' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: switch-1' \
  -d '{"start": "2027-05-01", "reason": "Start with the payroll month", "number": 0}' |
  jq -c '.record | {what, kind, version, before, after, person, reason}'
curl -s localhost:8080/admin/records.csv -H 'X-Person: e6001' -H 'X-Roles: workflow_admin'
```

```
{"name":"leave","current":1,"versions":[{"version":1,"usable":{"from":"2026-04-01","through":null},"start":"description"},{"version":2,"usable":{"from":"2027-04-01","through":null},"start":"description"}]}
{"name":"leave_ja","current":1,"versions":[{"version":1,"usable":{"from":"2026-04-01","through":null},"start":"description"},{"version":2,"usable":{"from":"2027-04-01","through":null},"start":"description"}]}
{"what":"kind","kind":"leave","version":2,"before":{"how":"description","at":1806505200,"day":"2027-04-01"},"after":{"how":"day","at":1809097200,"day":"2027-05-01"},"person":"e6001","reason":"Start with the payroll month"}
﻿at,line,what,ticket,kind,version,number,person,via,event,did,stage,by,proxy_of,to,comment,note,before,after,reason
2026-11-25T10:00:00+09:00,01M89785ZNRFX2Y9DKT0137W94,kind,,leave,2,,e6001,api,,,,,,,,,"{""how"":""description"",""at"":1806505200,""day"":""2027-04-01""}","{""how"":""day"",""at"":1809097200,""day"":""2027-05-01""}",Start with the payroll month
```

`number` は、一覧にある版の `start.number` です（ここでは 0）。ほかの管理者が切り替える前に読んだ番号で送ると、`stale` で拒否され、何も変わりません。CSV はバイト順マークで始まるので、表計算ソフトは UTF-8 として読みます。

`-dev-login` では、ログインのページに、例の役割ごとのリンクがあります。それを押してから、人を選びます。

### 有効な版がないあいだ

有効な版がない申請の種類には、入力するフォームがありません。第 2 版は 2027-05-01 からなので、e6001 が `leave` の第 1 版を止めると、その申請は 404 になります。その日から出せる人には日付を返し、ほかの人には、存在しないパスと同じ応答を返します。

```sh
curl -s -X POST localhost:8080/admin/kinds/leave/versions/1 -H 'X-Person: e6001' -H 'X-Roles: workflow_admin' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: switch-2' \
  -d '{"start": "never", "reason": "Version 2 replaces it", "number": 0}' | jq -c '.kind.current'
curl -s -w '\n' -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: leave-1' -d '{"form": {"kind": "paid"}}'
curl -s -w '\n' -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e0001' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: leave-2' -d '{"form": {"kind": "paid"}}'
```

```
null
{"error":"no version of leave is in effect now: it may be requested from 2027-05-01","from":"2027-05-01"}
{"error":"no such path"}
```

ページ、画面、エージェントのツールも、同じ日を示します。管理の操作が確かめること、gate に尋ねること、記録に残ることは、[設計](../DESIGN.ja.md)の 13 にあります。

## PostgreSQL を使う

`-db` を付けると、チケットと添付ファイルはデータベースに入ります。例の帳簿もそこに入ります。サーバーは帳簿の表を作り、どの部署にも 50,000,000 円の予算を、どの人にも 20 日の休暇を与えます。上の経費は、申請したときに営業一部の予算から 118,000 円を仮押さえし、承認されたときに確定します。

```sh
go run ./cmd/server -dev-login -db "postgres://localhost:5432/ringi"
```

`-db` を付けないときは、`-files <ディレクトリ>` で、添付ファイルをメモリの代わりにディレクトリに置けます。二つは一緒に使えません。
