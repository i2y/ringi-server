# 申請の種類を足す、変える

新しい種類の申請を足す手順と、人が使い始めた申請の決まりを変える手順です。変えるときは版を上げます。進行中の申請を、出したときの決まりのまま終わらせるためです。[2. 申請の種類を書き換える](change.ja.md)まで済ませたプロジェクトで説明します。英語版は [types.md](types.md) です。

## 申請の種類を足す

`project/` に、`.ringi` のファイルを一つ足します。例は、泊まりのある出張の申請です。費用の見込みが 10 万円を超えるときだけ、部長も承認します。

```ringi title="project/trip.ringi"
request 出張(trip) v1 "出張の申請"
description "泊まりのある出張の申請。上長が承認し、費用の見込みが 10 万円を超えるときは部長も承認する"
owner general_affairs

use organization 組織(org) from "shared/company.ringi"
use calendar 本社(office) from "shared/office.cal"
use gate "shared/company.gate"

form
  行き先(place)    : text "行き先"  count >=1 <=100
  出発日(leaving)  : date "出発する日"  range >=2026-04-01 <=2028-03-31
  泊数(nights)     : number "泊数"  range >=1 <=30
  費用(cost)       : money[JPY] "費用の見込み"  range >=1JPY <=2_000_000JPY
  目的(purpose)    : long text "目的"  count >=1 <=500

route
  上長(manager)  上長
  部長(head)     部長  when 費用 > 100_000JPY

who
  上長(manager)   = manager of requester
  部長(dept_head) = head of requester.department

every stage
  due in 3 business days at end of day
  remind after 2 business days at 09:00
  when late expire
  when empty escalate

ticket
  submitted by   everyone except position president
  self approval  not allowed
  same approver  approves once
  send back      to the requester, at most 2 times
  resubmit       within 5 business days
  withdraw       until it is decided

scenario 費用の小さい出張は上長だけが承認する
  requester s1201
  form 行き先 = "名古屋", 出発日 = 2026-12-03, 泊数 = 1, 費用 = 45_000JPY, 目的 = "展示会の見学"
| at               | event   | by    | -> stage | status      |
| 2026-11-16 10:00 | submit  | s1201 | 上長     | in progress |
| 2026-11-16 15:00 | approve | s1101 |          | approved    |

labels en
  request     "Trip request"
  行き先      "Destination"
  出発日      "Leaving on"
  泊数        "Nights"
  費用        "Estimated cost"
  目的        "Purpose"
  stage 上長  "Manager"
  stage 部長  "Head of department"
  who 上長    "Manager"
  who 部長    "Head of department"
```

`every stage` と `ticket` の行は、購買の申請と同じ意味です（[書いてあることを読む](change.ja.md#書いてあることを読む)）。

`use` の三行は、購買の申請と同じファイルを指します。組織、カレンダー、権限の決まりは、どの種類の申請も同じものを使います。`owner` は、この申請を受け持つチームの ID です。組織のデータの `teams.csv` にあるものを書きます。

ファイルを検査し、Go のコードを生成し、試験します。

```sh
ritsu check project
go generate ./...
go test ./...
```

Go のコードに書き足すものはありません。サーバーを起動し直すと、ホームの「申請する」に「出張の申請」が並びます。

## よく使う書き方

| したいこと | 書き方の例 |
|---|---|
| 選択肢から選ばせる | `種別(category) : choice "種別"` の下に、一行に一つずつ値を書く |
| 取引先のような、ときどき変わる一覧から選ばせる | [フォームの選択肢を一覧から読む](lists.ja.md) |
| ファイルを付けさせる | `領収書(receipt) : attachment "領収書"` |
| 条件が成り立つときだけ必須にする | `required when 合計 > 30_000JPY` |
| 条件が成り立つときだけ表示する | `shown when 種別 is 出張` |
| 明細の行を入力させ、合計を計算する | `明細(items) : rows "明細"  count >=1 <=20` と `合計(amount) : money[JPY] "合計" = sum(明細.金額)` |
| 営業日だけを選ばせる | `open in 本社` |
| 承認者が入力する項目にする | `approvers only` |
| 条件が成り立つときだけ通るステップにする | ステップの行に `when 金額 > 5_000_000JPY` |
| 二人以上の承認を求める | `at least 2 of 法務`、`all of 法務` |
| 読んでもらうだけにする | `circulate to 総務部` |
| 申請者に承認者を選ばせる | `確認者(reviewer) = chosen by requester in 技術の確認者` |
| 却下と差し戻しにコメントを求める | `comment required  for reject, send back` |
| 承認者が保留にできるようにする | `pause  at most 10 business days` |
| 承認したあとで取り消せるようにする | `retract approval until the next stage answers` |
| 予算や休暇の残りを押さえる | [予算や休暇の残りを確かめる](books.ja.md) |
| 承認のあとに処理を始める | [承認のあとの処理を始める](flows.ja.md) |
| 経路の途中で AI エージェントに確かめさせる | [承認の経路でエージェントに確かめさせる](agent-steps.ja.md) |

書き方の全部は [ringi の言語のページ](https://i2y.github.io/ritsu/ja/ringi/language/)に、これらを使った五つの申請は[例のプロジェクト](../examples/project)にあります。

## 使われている申請の決まりを変える

例として、社長の承認が要る金額を、100 万円を超えるときから 200 万円を超えるときに変えます。版 1 で出された申請は、版 1 のまま終わらせます。

### 1. 版 1 のファイルを取っておく

```sh
mkdir -p v1/project
cp -R project/purchase.ringi project/shared project/org v1/project/
```

取っておくのは、変える申請の種類と、それが読むファイルだけです。ほかの `.ringi` のファイルを入れると、同じ種類の同じ版が二つになり、サーバーが起動しません。

`v1/project` に入れた組織のデータと `company.gate` は、版 1 のコードを作るためだけに使います。サーバーの組織のデータと権限の決まりは一つずつで、版 1 の申請でも、承認者はいまの組織のデータから、してよいかはいまの決まりから決まります。異動があっても、`v1/project` は直しません。

### 2. 版を上げて、決まりを変える

`v1` を `v2` にして、ステップの条件と、それを確かめるシナリオの金額を変えます。

```ringi title="project/purchase.ringi" hl_lines="1 6 10"
request 購買(purchase) v2 "購買の申請"
…
route
  上長(manager)  上長
  部長(head)     部長
  社長(president) 社長  when 金額 > 2_000_000JPY
…
scenario 社長まで承認される
  requester s1201
  form 金額 = 2_500_000JPY, 目的 = "展示会のブース", 見積書 = "見積書.pdf"
```

### 3. 変わるところを読む

```sh
ritsu ringi diff v1/project/purchase.ringi project/purchase.ringi
```

```
  申請の種類 `購買`（v1 → v2）：`purchase.ringi` → `project/purchase.ringi`
経路：
  「金額」が1,000,001円から2,000,000円までの申請：ステップ「社長」（社長）が来なくなります。
  2026-04-01 からの組織で、9 人のうち 9 人の承認者が変わります。
    s1001（営業部長）、社長：社長：p0001（社長） → 来ません
    s1101（営業一課長）、社長：社長：p0001（社長） → 来ません
…
  ほかの申請は、どれも同じステップに進みます。
進行中のチケット：
  チケットは、出したときの申請の種類の版で終わります。版 1 で出したチケットは版 1 のとおりに終わり、ここに並べた変更は、版 2 で出す申請に効きます。
```

違いがあれば、このコマンドは終了コード 1 で終わります。変更を求めた人に、この出力を読んで確かめてもらいます。

### 4. サーバーに二つの版を渡す

`generate.go` に、版 1 のコードを生成する行を足します。

`--catalog` と `--module` は、版 2 と違うものにします。カタログは、画面の部品を並べたファイルです。`--catalog` の URL は、その名前として使われるだけで、開けなくてもかまいません。二つの版のカタログが同じ名前だと、どちらの画面の部品か決められないので、サーバーは起動しません。

```go title="generate.go" hl_lines="2"
//go:generate ritsu gen project --root project --target go --target requests --catalog https://example.com/acme/shinsei/a2ui/catalog.json --module example.com/acme/shinsei/go --out . --lang ja
//go:generate ritsu gen v1/project --root v1/project --target go --target requests --catalog https://example.com/acme/shinsei/v1/a2ui/catalog.json --module example.com/acme/shinsei/v1/go --out v1 --lang ja
```

`shinsei.go` では、版 1 のコードを読み込み、版 1 を返す関数を足して、`Parts` の `Packages` に並べます。

```go title="shinsei.go" hl_lines="5"
	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/orgfiles"

	"example.com/acme/shinsei/go/ticketkinds"
	ticketkinds1 "example.com/acme/shinsei/v1/go/ticketkinds"
)
```

```go title="shinsei.go" hl_lines="6 7 8 9 11"
func Parts(log *slog.Logger) (server.Config, error) {
	p, err := Package()
	if err != nil {
		return server.Config{}, err
	}
	p1, err := Package1()
	if err != nil {
		return server.Config{}, err
	}
	return server.Config{
		Packages:     []*server.Package{p, p1},
		Gate:         &Gate{Log: log},
```

```go title="shinsei.go"
//go:embed v1/requests/openapi.json v1/requests/types v1/requests/a2ui v1/requests/mcp
var requests1 embed.FS

// Package1 は、購買の申請の版 1。
func Package1() (*server.Package, error) {
	dir, err := fs.Sub(requests1, "v1/requests")
	if err != nil {
		return nil, err
	}
	return server.Load(ticketkinds1.Contract, ticketkinds1.Lang, ticketkinds1.Kinds, dir)
}
```

### 5. 生成し、試験し、入れ替える

```sh
go generate ./...
go test ./...
```

新しいサーバーに入れ替えると、新しい申請は版 2 になり、進行中の申請は版 1 のまま進みます。

## 新しい版を始める日を決める

何も書かなければ、入れ替えたときから新しい版が使われます。日を決める方法は二つあります。

| 方法 | 内容 |
|---|---|
| `.ringi` に書く | `owner` の次の行に `effective from 2026-12-01` と書くと、その日から始まる。シナリオの日付も、その日以後にする |
| 管理する人が切り替える | [管理のページ](use/admin.ja.md#申請の決まりの版を切り替える)で、始める日を決める。`effective from` の日より前にはできない。新しい版を「使わない」にして、前の版に戻すこともできる |

## 前の版のコードを残す

前の版のコードは、その版の申請がすべて終わっても残します。終わった申請を開くときにも、その版のコードを使うからです。申請を消す操作はないので、一度でも使われた版は外せません。外すと、その版の申請を開いたときに、サーバーが 500 を返します。

どの版の申請がいくつあるかは、管理のページの「チケット（進行中／全部）」に出ます。一件もない版は外せます。

## 版を上げなくてよいとき

まだだれも使っていない申請は、版を上げずに変えられます。画面の言葉だけを直すときも同じです。

進行中の申請があるのに版を上げずに変えると、進行中の申請に新しい決まりが混ざります。たとえば、ステップの ID を変えると、そのステップより前にいる申請は、承認できなくなります（[受け付けない理由](refusals.ja.md)は `unread`）。

## 申請の種類をやめる

[管理のページ](use/admin.ja.md#申請の決まりの版を切り替える)で、その申請の種類の、使える版の「始まり」をどれも「使わない」にします。新しい申請は出せなくなり、進行中の申請は最後まで進みます。`.ringi` のファイルは消さずに残します。
