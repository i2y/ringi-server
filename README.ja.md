# ringi-server

[ritsu](https://github.com/i2y/ritsu) が生成する申請と承認のコードを動かす、サーバーのフレームワークです。ritsu は、申請の種類（経費の精算、購買、休暇、アカウント、契約など）ごとに、チケットがどうなるかを決める純粋な関数と、サーバーとの約束（OpenAPI の記述、申請の種類ごとの説明、画面、ベクタ）を書き出します。このフレームワークはサーバーの側を受け持ちます。チケットを保管し、時計を鳴らし、知らせを送り、帳簿を呼びます。API と画面と、素の HTML のページを出し、人はそのページで申請を出し、承認し、自分の申請を見ます。使う人は生成したパッケージを渡し、自分の組織で違う所だけを差し替えます。

名前は仮です。

英語版：[README.md](README.md)。設計：[DESIGN.ja.md](DESIGN.ja.md)。

## 中身

根のモジュールに、フレームワークと付いてくる物があります。依存が別にある三つは、それぞれ別のモジュールです。

| パッケージかモジュール | 何か |
|---|---|
| `server`（根） | 本体。チケット、時計、することの一覧、管理（版の始まりの切り替え、組織のデータの確かめと取り込み、記録）、HTTP の API（`Server` は `http.Handler`）と、ページやツールが Go から呼ぶ口（`Do`、`Try`、`Waiting`、`Mine`、`Can` など） |
| `screens` | ritsu が種類ごとに生成した画面（A2UI）に、サーバーのデータを入れる。ボタンごとの操作と、断りを画面で伝える文も持つ |
| `screens/page` | 同じ画面を、スクリプトなしで動く素の HTML のページにする。ページを作る関数と、ページから送られたフォームを読む関数 |
| `web` | 画面とページと添付ファイルを、API の前で、一つの守りの後ろに置いて HTTP で出す |
| `oidc` | 組織の OpenID Connect のプロバイダーでのログイン。`oidc/oidctest` は試験のためのプロバイダー |
| `devlogin` | 手元のコンピューターでサーバーを試すためのログイン。組織の人を並べるページと、`X-Person` ヘッダー。どちらも本人を確かめないので、ループバックのアドレスに送られたリクエストにだけ答える |
| `postgres`、`memstore` | チケットと添付ファイルの保管先。PostgreSQL（pgx v5）と、試験やサーバーを試すときのためのメモリ |
| `orgfiles` | 組織のデータを、ritsu が読むファイルから読む |
| `serve` | サーバーのコマンドライン。どのサーバーにも要るフラグとその確かめ、フラグが言う保管先と添付ファイルの置き場、ログイン、ページを前に置いた HTTP のサーバー、同じ守りの後ろに置くプロジェクトのルート（たとえば `/mcp` のエージェントのツール） |
| `servertest`、`conformance` | 試験のための部品（プロジェクトのページの試験に使う、スクリプトの無いブラウザとページのフォームもある）と、サーバーを約束に照らして確かめる試験 |
| `temporal/`（モジュール） | flow とエージェントのステップを Temporal の上で動かす |
| `mcp/`（モジュール） | Model Context Protocol のサーバー。申請を、エージェントが呼ぶツールとして出す。単独でも、`serve` が動かすサーバーの `/mcp` でも出せる |
| `examples/`（モジュール。公開しない） | ritsu の五つの例をフレームワークの上で動かす。試すためのサーバー（エージェントのツールを出す物も）と、例を動かす試験（`mcp` の物も）もある |

## 五分で動かす

Go 1.26 以降が要ります。`examples/` で、開発用のログインを入れて例のサーバーを起動します。ritsu の五つの例（経費、購買、休暇、アカウント、契約。それぞれ英語版と日本語版）を、ritsu が生成した gate と一緒に動かします。チケットはメモリに保管し、帳簿はどの呼び出しにも「済んだ」と答えます（下の PostgreSQL を使う起動では、ritsu が生成した帳簿が予算と休暇を受け持ちます）。

```sh
cd examples
go run ./cmd/server -dev-login
```

ブラウザで <http://localhost:8080/> を開きます。ログインのページに組織の人が並ぶので（1,000 人を超えるときは、並べる代わりに、名前か ID の一部で探す欄を出します）、e1201（営業担当 1）を選び、領収書を付けて経費を申請します。ログアウトして e1101（営業一課長）を選ぶと、ホームに自分を待っている物が出るので、そこから承認します。ページは、ブラウザが求めるとおりに英語か日本語で出し、上の帯のリンクで切り替えられます。開発用のログインはだれの本人確認もしないので、サーバーはループバックのアドレスでしか受け付けません。本当のログインは、下の「[ログイン](#ログイン)」にあります。

`pages.sh` は、同じことを curl で行います。スクリプトを使わないブラウザと同じように、ページのフォームを読み、値を入れて送り、サーバーの答えを表示します。別のターミナルの `examples/` で走らせます。

```sh
./pages.sh
```

次のように表示されます（ID は毎回変わります）。

```
1. e1203 (Sales staff 3) signs in, opens the form of a leave request and sends it:
   303 http://127.0.0.1:8080/pages/tickets/01M7KSVDS42R2WGTT04B6HRP2B?lang=en
2. e1101 (the manager of Section 1A) signs in; the start page and the list say what waits:
   Leave request (1)
   href="/pages/tickets/01M7KSVDS42R2WGTT04B6HRP2B?lang=en"
3. e1101 opens the card and approves:
   303 http://127.0.0.1:8080/pages/tickets/01M7KSVDS42R2WGTT04B6HRP2B?lang=en
4. The same card, sent again, is refused: the request changed after it was shown:
   409 
      The request changed after this page was shown. Here it is as it is now: check it, and try again.
5. The ticket, as the API answers e1203 with the same cookie:
   approved
   submitted  e1203  Two days before the holidays.
   approved  e1101  Enjoy the holidays.
   ticket approved    
```

営業担当 3（e1203）が休暇を申請し、営業一課長（e1101）が、ホームと待っている物の一覧から申請を開いて承認します。4 では、承認する前に見ていたカードをもう一度送ったので、サーバーは 409 で断り、今のチケットを見せ直します。5 は、同じクッキーで API が答えるチケットです。

### API

`-dev-login` を付けないと、例のサーバーは、だれがリクエストを送ったかを `X-Person` ヘッダーで（ロールは `X-Roles` で）そのまま信じます。curl で API を試すためです。これも本人を確かめないので、サーバーはループバックのアドレスでだけこれを使い、ループバックのアドレスに送られたリクエストにだけ答えます。ほかのサイトのページが、自分の名前のままブラウザにこのコンピューターへリクエストを送らせても、人はいない扱いです。`drive.sh`（curl と jq を使う）は、経費の申請から決裁までを API で通します。

```sh
go run ./cmd/server
./drive.sh          # 別のターミナルで
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
   ticket 01M7PK037W235F77AGHYJWJ858: in_progress, at the stage first
3. What e1101 (the manager of Section 1A) waits for:
   01M7PK037W235F77AGHYJWJ858  expense  first
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

営業担当 1（e1201）が領収書を上げて福岡への出張を申請し、営業一課長（e1101）が承認し、営業一部長（e3101）が決裁します。5 では、部長が課長の承認より前の画面から承認したので、チケットが `stale` で断り、見せ直すよう求めています。8 は、エージェントが決める日帰りの出張です。

サーバーのログには、保存のあとに外へ出た物が出ます（ここでは時刻を省きます）。知らせ（本物のサーバーならメールを送る所）、承認された経費が始める flow `pay`、日帰りの出張を決めたエージェントです。知らせは、人ごとにその人の言語で書きます。組織のデータがその人に与える言語（e1101、e3101、e1201 は日本語）で、無ければ種類を書いた言語です。種類は、その言語での題と、その題の言語で言います。

```
level=INFO msg=notice notice=waiting to="e1101 e1101@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M7PK037W235F77AGHYJWJ858：承認待ち body=経費の申請（01M7PK037W235F77AGHYJWJ858）は、あなたの承認を待っています（ステップ：first）。 ticket=01M7PK037W235F77AGHYJWJ858 stage=first
level=INFO msg=notice notice=waiting to="e3101 e3101@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M7PK037W235F77AGHYJWJ858：承認待ち body=経費の申請（01M7PK037W235F77AGHYJWJ858）は、あなたの承認を待っています（ステップ：decide）。 ticket=01M7PK037W235F77AGHYJWJ858 stage=decide
level=INFO msg="flow launched" flow=pay kind=expense ticket=01M7PK037W235F77AGHYJWJ858 run=01M7PK037W235F77AGHYJWJ858/1/17
level=INFO msg=notice notice=decided to="e1201 e1201@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M7PK037W235F77AGHYJWJ858：結果 body=経費の申請（01M7PK037W235F77AGHYJWJ858）は、結果が出ました。 ticket=01M7PK037W235F77AGHYJWJ858 stage=""
level=INFO msg="agent asked" flow=travel_decision kind=expense ticket=01M7PK03D4HBCR491B7NSAS5A3 stage=decide note=false
level=INFO msg="agent answered" flow=travel_decision ticket=01M7PK03D4HBCR491B7NSAS5A3 verdict=ok outcome=ok
level=INFO msg="flow launched" flow=pay kind=expense ticket=01M7PK03D4HBCR491B7NSAS5A3 run=01M7PK03D4HBCR491B7NSAS5A3/1/12
level=INFO msg=notice notice=decided to="e1201 e1201@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M7PK03D4HBCR491B7NSAS5A3：結果 body=経費の申請（01M7PK03D4HBCR491B7NSAS5A3）は、結果が出ました。 ticket=01M7PK03D4HBCR491B7NSAS5A3 stage=""
```

`-verdict doubt` を付けると、エージェントは日帰りの出張を決めずに課長へ回します。ritsu が生成した画面（A2UI）も出します。[web/README.ja.md](web/README.ja.md) で、curl で一通り試せます。

一覧は、ページに分けて答えます。`GET /waiting` はその人を待っているチケットを新しい順に返し、`GET /mine` はその人の申請を返します（`?kind=` で申請の種類を一つに絞れます）。どちらも `limit`（1〜100。無ければ 50）と `before`（前のページの最後のチケットの ID）を取ります。答えは `{"tickets": […], "more": …}` で、`more` はページのあとに続きのチケットがあるかです。断りは 409 で、断りの種類、英語と日本語の理由、フォームのどこの断りか（あれば）を返します。`{"refused": "form", "text": …, "text_ja": …, "at": "items[1].price"}` の形です。空のコメントと長すぎるコメントは、種類 `comment` で、場所を持たずに断ります。ステップが理由を求めているのに理由の無い却下と差し戻し（たとえば、課長による休暇の却下）も同じです。一人がつけられるコメントは、一つのチケットに 25 までです（`Config.CommentsPerPerson`）。一人でチケットのコメントを埋められないようにするためです。

フォームが計算する物は、画面が計算するのと同じく、`POST /requests/<種類>/worked`（承認者の直しを入れるなら `POST /requests/<種類>/tickets/{id}/worked`）が答えます。条件で見せるフィールドと条件で必須になるフィールドがそれぞれそうなっているかと、フォームが計算する値（決まらないあいだは null）です。ページは、この答えでフィールドを見せ、印を付けます。

```sh
curl -s -X POST localhost:8080/requests/expense/worked -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -d '{"form": {"category": "travel", "items": [{"what": "Flight to Fukuoka and back", "price": 64000}]}}'
curl -s -X POST localhost:8080/requests/expense/worked -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -d '{"form": {"category": "purchase", "items": [{"what": "A desk lamp", "price": 4800}]}}'
```

```
{"fields":{"amount":{"value":64000},"receipt":{"required":true},"trip_to":{"shown":true}}}
{"fields":{"amount":{"value":4800},"receipt":{"required":false},"trip_to":{"shown":false}}}
```

探す操作は、フォームが選ぶ値を言葉で探し、近い順に返します。`GET /find/people`、`GET /find/teams`、`GET /find/masters/<ファイル>/<マスタ>`（種類が選ぶマスタ）で、引数は `q`（言葉）、`limit`（1〜100。無ければ 20）、`lang`（`en` か `ja`）と、マスタの行なら `on`（行を選べる日）です。人とチームは、`GET /organization` と同じく組織の人だけが探せます。ほかの人には、わけを言って 404 を返します（`{"error":"the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-18"}`）。

```
GET /find/people?q=e1203&lang=ja

{"found":[{"value":"e1203","name":"営業担当 3","label":"営業担当 3（e1203）"}],"more":false}
```

PostgreSQL を渡すと、チケットと添付ファイルと帳簿はそこに入ります。サーバーは帳簿の表を作り、どの部署にも 50,000,000 円の予算を、どの人にも 20 日の休暇を与えます。上の経費は、申請したときに営業一部の予算から 118,000 円を仮押さえし、承認されたときに確定します。

```sh
go run ./cmd/server -dev-login -db "postgres://localhost:5432/ringi"
```

`-files <ディレクトリ>` を付けると、添付ファイルはメモリの代わりにそのディレクトリに入ります。`-db` を付けないサーバーのための物です。`-db` を付けると添付ファイルはデータベースに入るので、両方を付けるとサーバーは起動しません。

### 管理

管理する人は、申請の種類の版を切り替え、組織の新しいデータを取り込み、その記録をチケットの動きと一緒に読みます。`/pages/admin/` の下のページでも、`/admin` の下の API でもできます。だれが何をできるかは gate が決めます。例では、`workflow_admin` は自分の部署が持つ申請の種類を、`system_admin` はすべてを管理し、`auditor` はどの記録も読めて、何も変えられません。`-dev-login` では、ログインのページで、人の一覧の横に役割を書いてから人を選びます。e6001（人事部長）を `workflow_admin` で選ぶと、ホームに管理へのリンクが出て、休暇の申請の版を切り替えられます（例のサーバーは、2027-04-01 から使える第 2 版も持っています）。e7001（情報システム部長）を `system_admin` で選ぶと、組織のデータ（サーバーが読むファイルか、それを zip にしてアップロードした物）を確かめ、日ごとの違いを読んで取り込めます。取り込むと自分がもう取り込めなくなるデータは、ページで確かめを求められます。どの変更も、だれが、いつ、なぜ行ったかと一緒に残ります。

API では、ヘッダーを使って、e6001 が切り替えられる物を並べ、`leave` の第 2 版を 2027-05-01 からに切り替え、記録を CSV で読みます。

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
at,line,what,ticket,kind,version,number,person,via,event,did,stage,by,proxy_of,to,comment,note,before,after,reason
2026-11-18T14:45:57+09:00,01M7PK1G6ZD6Y5YFG4CWMWE8Y6,kind,,leave,2,,e6001,api,,,,,,,,,"{""how"":""description"",""at"":1806505200,""day"":""2027-04-01""}","{""how"":""day"",""at"":1809097200,""day"":""2027-05-01""}",Start with the payroll month
```

`number` は、一覧が答えた版の `start.number`（ここでは 0）です。ほかの管理する人が切り替えたあとに、その前に読んだ番号で送ると `stale` で断り、何も変えません。CSV はバイト順マークで始まるので、表計算ソフトが UTF-8 として読みます。それぞれの操作が確かめること、gate に尋ねること、記録に残る物は、[DESIGN.ja.md](DESIGN.ja.md) の 13 にあります。

今は効いている版が無い種類（`leave` の版 1 を止め、版 2 は 2027-05-01 から）には、入れるフォームがありません。その種類の申請と、そのフォームが計算する物を尋ねると 404 です。次の版が始まる日から出せる人にはその日を言い、ほかの人には無いパスと同じに答えます。ページ、画面、エージェントのツールも同じ日を言います。

```sh
curl -s -X POST localhost:8080/admin/kinds/leave/versions/1 -H 'X-Person: e6001' -H 'X-Roles: workflow_admin' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: switch-2' \
  -d '{"start": "never", "reason": "Version 2 replaces it", "number": 0}' | jq -c '.kind.current'
curl -s -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: leave-1' -d '{"form": {"kind": "paid"}}'
curl -s -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e0001' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: leave-2' -d '{"form": {"kind": "paid"}}'
```

```
null
{"error":"no version of leave is in effect now: it may be requested from 2027-05-01","from":"2027-05-01"}
{"error":"no such path"}
```

## 自分のプロジェクトを始める

`ritsu ringi init` は、自分のプロジェクトの最初のファイルを書きます。申請の種類と、それを出す組織と gate、そこから ritsu が生成するコード、このフレームワークで動くサーバー、試験です。ritsu と Go 1.26 以降が要ります。

```sh
ritsu ringi init shinsei --module example.com/acme/shinsei --lang ja
```

```
shinsei に 49 個のファイルを書きました（モジュール example.com/acme/shinsei）。project/ に申請の種類と組織と gate、go/ と requests/ にそこから ritsu が生成したコード、cmd/server/ にフレームワーク github.com/i2y/ringi-server v0.1.0 のサーバー、それと試験です。次に:
  cd shinsei
  go mod tidy
  go test ./...
  go run ./cmd/server -dev-login
を走らせ、ブラウザで http://127.0.0.1:8080/pages/ を開きます。続きは README.ja.md にあります
```

`--module` は Go のモジュールのパスで、`go mod init` が取る形で渡します。`--lang ja` を付けると、申請の種類と組織と gate の名前が日本語になり、README.ja.md も書きます。

| パス | 中身 |
|---|---|
| `project/` | ritsu が読む規則。`purchase.ringi` は購買の申請（金額と目的を入れ、申請した人の上長、次に部長が承認する。期限、催促、差し戻し、却下がある）。組織（`shared/company.ringi` と、三つの部、二つの課、十人の `org/`）。gate の `shared/company.gate`。暦の `shared/office.cal` |
| `go/`、`requests/` | ritsu が `project/` から生成する物。Go のパッケージと、約束（OpenAPI、画面、ベクタ） |
| `generate.go` | `ritsu gen` を走らせる `//go:generate` の行 |
| `shinsei.go`、`gate.go`、`notify.go` | プロジェクトがサーバーに渡す物。生成したパッケージと組織、sekisho が書いたコードを包む gate、通知をログに書く Notifier |
| `cmd/server/` | サーバー。パッケージ `serve` に、上の物を渡す |
| `*_test.go` | 試験。ベクタを API に流す `TestVectors`、gate の `TestGate`、開発用のログインで入ってページで申請し承認する `TestPages` |

書いたファイルは自分の物です。`init` はどのファイルも一度だけ書き、一つでももうあれば、それを全部挙げて何も書きません。`project/` を変えたら、`go generate ./...` が `go/` と `requests/` を書き直します。それ以外は書きません。人に使ってもらうためのサーバーの動かし方（OpenID Connect、PostgreSQL、TLS）と、申請の種類や flow の足し方は、プロジェクトの README にあります。

二つのオプションは、プロジェクトに無くても動く物を、それが要るこのリポジトリのモジュールと一緒に足します。

- `--mcp` は、モジュール `mcp` のエージェントのツールを、ページと同じログインの後ろで、サーバーの `/mcp` に置きます（`tools.go`）。ツールで申請を出して読む試験（`TestTools`）も足します。
- `--temporal` は、二つ目の申請の種類 `expense.ringi` を足します。承認されると、申請者に払い戻す flow（`shared/pay.flow`）が始まります。flow はモジュール `temporal` で Temporal の上で動かします。flow を始める所とワーカーを作る所（`flows.go`）、flow のタスク（`payments.go`。ログに書くだけ）、ワーカーのコマンド（`cmd/worker/`）、Temporal の開発用サーバーで flow を動かす試験（`TestFlows`。Temporal の CLI が無ければ `SKIP` と書きます）を書きます。サーバーとワーカーには Temporal のサーバーが要ります。`temporal server start-dev` で起動する開発用サーバーは `localhost:7233` で待ち受け、どちらも `-temporal` を渡さなければそこへつなぎます。

```sh
ritsu ringi init shinsei --module example.com/acme/shinsei --lang ja --mcp --temporal
```

```
shinsei に 82 個のファイルを書きました（モジュール example.com/acme/shinsei）。project/ に申請の種類二つ（一つは flow を持つ）と組織と gate、go/ と requests/ にそこから ritsu が生成したコード、cmd/server/ にフレームワーク github.com/i2y/ringi-server v0.1.0 のサーバー（/mcp にエージェントのツール）、cmd/worker/ に flow のワーカー、それと試験です。次に:
  cd shinsei
  go mod tidy
  go test ./...
を走らせ、Temporal の開発用サーバーを起動し（temporal server start-dev）、別々の端末で
  go run ./cmd/worker
  go run ./cmd/server -dev-login
を走らせ、ブラウザで http://127.0.0.1:8080/pages/ を開きます。続きは README.ja.md にあります
```

ritsu のどの版も、自分が生成するコードに合う、このフレームワークのただ一つの版を `go.mod` に書きます。オプションが要るモジュール `mcp` と `temporal` も、同じ版です。

## ログイン

だれがリクエストを送ったかは、サーバーの `Authenticator` が決めます。例のサーバーには三つあり、一度に使うのは一つです。どれも、パッケージ `serve` がどのサーバーのコマンドにも用意する物です。`X-Person` ヘッダー（既定）、開発用のログイン（`-dev-login`。この二つはパッケージ `devlogin`）、組織の OpenID Connect のプロバイダー（パッケージ `oidc`）です。

```sh
export RINGI_OIDC_CLIENT_SECRET='プロバイダーが発行したシークレット'
export RINGI_SESSION_KEYS="$(head -c 32 /dev/urandom | base64)"
go run ./cmd/server -addr :8443 -tls-cert server.crt -tls-key server.key \
  -oidc-issuer https://id.example.com \
  -oidc-client-id ringi \
  -oidc-redirect-url https://ringi.example.com:8443/login/callback
```

プロバイダーには、サーバーを、シークレットを持つ認可コードフローのクライアントとして登録します。リダイレクト URI は上のもの、スコープは `openid email profile` です。ログインした人は、プロバイダーが確認済みと言うメールアドレスで、組織のデータの人から探します。セッションは、鍵で暗号化したクッキーに入れます。`-db` を付ければ、ログアウトはサーバーのどのプロセスでも効きます。詳しくは [oidc/README.ja.md](oidc/README.ja.md) にあります。人をログインさせるプロキシの後ろに置くときは、`web.ProxyHeader` がプロキシのヘッダーから人を取ります（[web/README.ja.md](web/README.ja.md)）。プロキシを通してプロバイダーに届くサーバーや、組織の認証局が署名した証明書を使うプロバイダーには、届くための HTTP のクライアントを Go で渡します。フラグではない `serve.Flags.ProviderClient` です。

サーバーは、たとえば次のような設定では、リクエストを受ける前に、何が違うかを言って止まります。プロバイダーの設定が一部だけ（足りない物の名前を言います）、セッションの鍵が 32 バイトの乱数でない、秘密の終わりに改行がある、データベースに届かない、本人を確かめないヘッダーや `-dev-login` を、人に使ってもらうサーバーの設定（環境の `RINGI_OIDC_CLIENT_SECRET` や `RINGI_SESSION_KEYS`、証明書の `-tls-cert`）と一緒に使う、です。どのサーバーでもリクエストを受ける前に確かめる物は、[DESIGN.ja.md](DESIGN.ja.md) の 2.5 にあります。

## 使い方

```go
p, err := server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, requestsDir) // what ritsu gen wrote
var srv *server.Server
signIn, err := oidc.New(ctx, oidc.Config{
	Issuer: issuer, ClientID: clientID, ClientSecret: secret, RedirectURL: redirectURL, Keys: keys,
	Map: oidc.ByEmail(func(ctx context.Context) ([]server.OrgPerson, error) {
		return oidc.PeopleOf(ctx, srv) // the people of the organization's data in effect today
	}),
	Revocations: store, // a sign-out holds in every process
})
srv, err = server.New(server.Config{
	Packages:      []*server.Package{p},
	Store:         store,     // postgres.New(ctx, pool, "")
	Authenticator: signIn,    // who sends the request
	Gate:          gate,      // around the gate sekisho wrote
	Organization:  orgfiles.New(os.DirFS("project")),
	Notifier:      notifier,  // yours: mail, chat…
	Books:         books,     // around the clients chobo wrote
	Files:         store.Files(), // the attachments
})
srv.ImportOrganization(ctx)
go srv.Run(ctx) // the timers and the things to do
h, err := web.New(srv, web.Config{ // the pages, the screens and the attachments, in front of the API
	Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout",
})
signIn.Mount(h) // /login, its callback and /logout, behind the same guards
(&http.Server{Addr: ":8443", Handler: h, ReadHeaderTimeout: 5 * time.Second}).ListenAndServeTLS(cert, key)
```

`examples/` は、そのまま動くプログラムです。`examples.go` がパッケージを渡し、`gate.go` が gate を、`books.go` が帳簿を包み、`parts.go` はログに書く通知・flow・エージェントで、`cmd/server` がそれらを組み立てます。上のコードの残りは、パッケージ `serve` に任せます。どのサーバーにも要るフラグ（アドレス、`-db`、`-files`、ログイン、証明書）とその確かめ、フラグが言う保管先と添付ファイルの置き場、ログイン、前に `web` を置いた HTTP のサーバーです。`ritsu ringi init` が書くプロジェクトも、同じ形です（「[自分のプロジェクトを始める](#自分のプロジェクトを始める)」）。

プロジェクト自身のルートは、`Stores.Handle` で同じサーバーに置きます。ページや API と同じ守りと同じログインの後ろに置かれます。`mcp/` のエージェントのツールを `/mcp` に置くのも、その一つです。

```go
st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
	tools, err := mcp.New(srv, mcp.Config{})
	if err != nil {
		return nil, err
	}
	return tools.Handler(mcp.HTTP{Person: serve.Person}) // the person the sign-in found
})
```

だれもログインしていないリクエストには、ツールに届く前に 401 を返します。エージェントがすることはその人として行われ、gate に尋ねられます。ルートはフレームワークのパス（ページ、画面、添付ファイル、API、ログイン）を取れません。取ろうとすると、サーバーは動き出す前に、どのルートかを言って止まります（[DESIGN.ja.md](DESIGN.ja.md) の 2.6）。

## 差し替える物

| 何を | インターフェース | 付いてくる物 |
|---|---|---|
| だれがログインしているか | `Authenticator` | `oidc`（組織の OpenID Connect のプロバイダー）、`web.ProxyHeader`（人をログインさせるプロキシの後ろで）、`devlogin`（手元のコンピューターでサーバーを試すときだけ）、`servertest.Header`（試験と例のためだけ） |
| チケットをどこに保管するか | `Store` | `postgres`（pgx v5）、試験には `memstore` |
| だれが何をしてよいか | `Gate` | sekisho が書いた gate を包む（`examples/gate.go`）か、`server.AllowAll` |
| 組織のデータ | `OrgSource` | ritsu が読むのと同じファイルを読む `orgfiles` |
| 知らせ | `Notifier` | 無し（ログに書く） |
| flow とエージェントのステップ | `Flows`、`Agents` | モジュール `temporal/`。試験には `servertest` |
| 帳簿 | `Books` | chobo が書いたクライアントを包む（`examples/books.go`） |
| 添付ファイル | `Files` | `web.NewDiskFiles`（ディレクトリ）、`(*postgres.Store).Files()`、`memstore.Files`（メモリ） |
| 監査ログ | `AuditLog` | `servertest` |

どれもメソッドが数個のインターフェースです。それぞれが守ること（たとえば通知は、一つのキーを一度だけ送ること）は DESIGN.ja.md に書いてあります。

知らせは、種類と、チケットが持つその種類の版を持ちます（`Notice.Kind`、`Notice.Version`）。知らせる人ごとに、知らせる言語も持ちます（`Recipient.Language`。組織のデータ、つまり `people.csv` の列 `language` にあるその人の言語で、無ければ種類の言語）。`Server.Title` は、その言語での種類の題を、ページが見せるとおりに、その題の言語と一緒に返します。メールの件名に入れる通知の例が、[DESIGN.ja.md](DESIGN.ja.md) の 2.3 にあります。

## 適合の試験

`conformance` パッケージは、サーバーを ritsu が生成した約束に照らして確かめます。ストアは何でもかまいません。`Vectors` はベクタを全部 API から流し、どの答えも約束が決めた形かを確かめます（`Package.CheckAnswer`。API のどの答えにも使えます）。`Trials` は、人の操作をそれぞれ行う前に試し、試しが何も残さず、操作と同じように答えることを確かめます。`Concurrent` は、一つのストアに置いた二つのサーバーから同じチケットへ多くの操作を同時に送り、ストアが保管した物を確かめます。`WaitingCounts` は、ホームに出す、その人を待っているチケットの数が、待っているチケットと合うかを、代理の日の前、日のうち、日のあとで確かめます。`WaitingOrder` は、そのチケットの順（待ち始めた時刻をマイクロ秒まで見た新しい順、同じ時刻なら ID の大きい順）と、コメントがチケットの場所を変えないことを確かめます。`MineOrder` は、自分の申請の順を確かめます。どちらも、API からページに分けて読んだときの順と、どのページも続きがあるか（`more`）を言うことも確かめます。`Reminded` は、ステップのリマインダーが鳴ったか（`reminded`。約束の版 3 はどのステップにも持たせます）を、止めて再開しても、ストアが保つことを確かめます。`Administration` は、管理で残す物を確かめます。読んだ番号での版の始まりの切り替え、ページに分けて読む切り替えの記録、組織のデータの下書き、同時に送った二つの取り込みのうち一つだけを受けること、一日の取り込みの数、gate が許さない人には何も変えず何も読ませないことです（ベクタは要りません）。`Keeping` は、ストアが返す時刻がどれも UTC であることと、古い操作を、古い物から（同じ時刻なら人、キーの順。Go が文字列を比べる順）上限ずつ消すことを確かめます（ベクタは要りません）。自分のストアを確かめるには、次のようなテストを書きます。

```go
func TestConformance(t *testing.T) {
	p, err := server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, os.DirFS("requests"))
	if err != nil {
		t.Fatal(err)
	}
	s := conformance.Setup{
		Packages:     []*server.Package{p},
		Store:        func(testing.TB) server.Store { return newEmptyStore() }, // yours
		Organization: orgfiles.New(os.DirFS("project")),
	}
	t.Log(conformance.Summary(conformance.Vectors(t, s)))
	conformance.Trials(t, s)
	conformance.Concurrent(t, s)
	conformance.WaitingCounts(t, s)
	conformance.WaitingOrder(t, s)
	conformance.MineOrder(t, s)
	conformance.Reminded(t, s)
	conformance.Administration(t, s)
	conformance.Keeping(t, s)
}
```

ストアは、人を待っているチケットを、読まずに数えてもかまいません（`server.WaitingCounter`。メモリのストアと PostgreSQL はそうしています）。`WaitingCounts` は、その数も確かめます。数えないストアでは、サーバーがチケットを読んで数えます。

フレームワーク自身の試験と、例の試験（メモリのストアでの適合の試験と、`RINGI_TEST_DATABASE` にデータベースを書いたときは PostgreSQL での適合の試験、例を動かすモジュール `mcp` の試験。画面の試験はボタンを Node で押すので、`PATH` に `node` が無ければ飛ばします）は、次のように回します。

```sh
go test ./...
cd examples
go test ./...
RINGI_TEST_DATABASE="postgres://localhost:5432/ringi_test" go test -race ./...
```

例では、どちらのストアでも、2,964 本のベクタが全部合います。2,140 本は HTTP のハンドラーを通り、答えはどれも約束の形です。残り（時計、エージェントの答え、ベクタに人が書いていない管理する人の操作）は `Deliver` を通ります。人の操作 2,150 本は、行う前に試しもかけます（`Server.Try`）。試しは、そのあと行った操作と同じように答え、何も残しません。`WaitingCounts` は、6 人について 19 件のチケットを入れ、五つの時刻で数を確かめます。どちらのストアでも、数えないストアでも合います。`WaitingOrder` は、2 人について 9 件のチケットを入れ、一件にコメントします。`MineOrder` は 7 件を入れ、うち 6 件は一人の申請です。順とページは、どちらのストアでも合います。`Reminded` は、ステップのリマインダーを鳴らしてから、チケットを止めて再開します。どちらのストアでも、ステップは `reminded` を持ち続けます。`Keeping` は、どちらのストアでも、そのファイルと一緒に合います。

人が作る行は、画面とページの上でも、HTTP で作り直します。画面では、ハンドラーから読んだ画面のボタンを押します。サーバーに届いた 1,940 回はベクタと合います。残りの 170 回はベクタが断る物で、168 回は画面が先に止め、2 回は画面が隠しているフィールドに値を入れる物で、画面はそのフィールドを送りません。ページでは、ハンドラーからページを読み、ブラウザと同じ形でフォームを送ります。行はページのボタンで足し、ファイルも選びます（送るのは 2,116 回。破棄はカードとフォームの両方から送ります）。サーバーに届いた 1,946 回はベクタと合い、1,654 回は受け付け、292 回はベクタと同じように断ります。残りの 170 回は、ベクタが断る物をページが先に止めます。ページ自身の確かめで 140 回、本人が開けないページで 12 回、ページが選ばせない値で 18 回です。

ページは、本物のブラウザでも使ってみます。ビルドタグ `browser` を付けたときだけ回る試験です。`RINGI_BROWSER` には、`--headless` と `--remote-debugging-pipe` を受け付けるブラウザのプログラムを書きます。試験ごとに、そのブラウザを専用のプロファイルで起こし、127.0.0.1 の試験のサーバーにだけつながせ、試験の終わりに止めます。`go test ./...` では回らず、`RINGI_BROWSER` が無ければ失敗します。

```sh
cd examples
RINGI_BROWSER=/path/to/a/browser go test -tags browser -run Browser .
```

英語と日本語、明るい配色と暗い配色、幅 1,280 px と 320 px でページを開き、どの文字も背景と 4.5:1（大きな文字は 3:1）で、入力欄やボタンの縁は 3:1 で見分けられること、ページが画面より広くならないこと、Tab で上から下へ進み、どこでも 3:1 のフォーカスの枠が見えることを確かめます。ほかに、ブラウザの文字の大きさを 2 倍にし、申請のボタンを二度続けて押し、戻るボタンのあとにもう一度押し、スクリプトなしでページを送ったあとのフォーカスの行き先を見て、入力メソッドで書いた語を Enter で確定します。申請の送信を答えが来る前に止め、すぐにもう一度押してもボタンが何も送らないこと、3 秒たてば送り、サーバーが同じチケットで答えることも確かめます。スクリプトを切ったページの入力欄で Enter を押すと、サーバーは 204 で答え、ページも、入力した文字も、フォーカスもそのまま残ることも確かめます。説明に日本語の文言が無い種類を日本語で見せ、スクリプトが書く名前が言語の印を保つことも確かめます。足した行や番号を振り直した行の見出しと、フォームの頭に言う検査の行で、スクリプトなしでページを送ったときと同じ印が付きます。開くページには管理のページも入ります。版の切り替えの日は、版を始められる日の範囲にブラウザが絞る日付の欄で、範囲より前の日は送られないこと、ファイルの欄で選んだ組織のファイルの zip が送られて取り込まれること、記録の CSV がバイト順マークで始まるファイルとして保存されることも確かめます。

## 例を作り直す

`examples/project/` は ritsu の申請の例のコピーです。ただし、エージェントを使う三つの flow のモデルだけは、仮の名前（`example-model`）にしてあります。ここでは `examples/parts.go` と試験がエージェントの代わりに答えるからで、どちらのモデルでも ritsu が書く `go/` と `requests/` は同じです。`examples/regen.sh` は、`PATH` にある `ritsu` で、このコピーから `examples/go/` と `examples/requests/` を作り直します。

## 版を公開する

フレームワークは三つのモジュールで、一つの版で公開し、タグを三つ付けます（`vX.Y.Z`、`mcp/vX.Y.Z`、`temporal/vX.Y.Z`）。タグを付ける前に、次の三つをします。

1. `mcp/go.mod` と `temporal/go.mod` で、フレームワークの require をその版に上げます（`go mod edit -require=github.com/i2y/ringi-server@vX.Y.Z`）。フレームワークを求めず `mcp` か `temporal` を求めるプロジェクトには、この二つの go.mod が求める版のフレームワークが選ばれ、このリポジトリで求めている `v0.0.0` は取れないからです。`replace github.com/i2y/ringi-server => ../` は残します。go コマンドが読む replace はメインモジュールの物だけで、`mcp` と `temporal` には `go install` で入れるコマンドも無い（`go install` は replace のあるモジュールを断る）ので、replace が効くのはこのリポジトリでの作業だけだからです。
2. `mcp/` と `temporal/` に置いた `LICENSE-APACHE` と `LICENSE-MIT` の写し（リンクではなくファイル）を残します。go コマンドがサブディレクトリのモジュールの zip に根から写すのは、`LICENSE` という名前のファイルだけだからです。`TestLicensesOfTheModules` が、根のファイルと同じかを確かめます。
3. プロジェクトが取るのと同じ形で確かめます。タグを付けるコミットからファイルのプロキシ（`GOPROXY=file://…`）を作り、ネットワークも replace も使わずに、`mcp` だけを求めるモジュールと `temporal` だけを求めるモジュールを tidy して組み、vet します。`ritsu ringi init` が `--mcp` と `--mcp --temporal` で書くプロジェクトは、tidy、generate、vet、試験まで回します。三つのモジュールの zip のそれぞれに、二つのライセンスのファイルが入っていることも見ます。

## ライセンス

MIT OR Apache-2.0（[LICENSE-MIT](LICENSE-MIT)、[LICENSE-APACHE](LICENSE-APACHE)）。
