# 予算や休暇の残りを確かめる

申請が出たときに予算や休暇の残りを押さえ、足りなければ受け付けず、承認されたら使ったことにします。残りを数える部分を**帳簿**と呼び、ritsu の言語の一つ、chobo で書きます。英語版は [books.md](books.md) です。

購買の申請と部の予算を例にします。要るものは、[1. 動かして、申請を承認する](start.ja.md)で作ったプロジェクトと、PostgreSQL です。社員が使っている申請に足すときは、先に[版を上げます](types.ja.md#使われている申請の決まりを変える)。

## 帳簿がすること

| 申請に起きたこと | 帳簿がすること | `.ringi` に書く言葉 | Go のコードに届く言葉 |
|---|---|---|---|
| 出された、再申請された | 金額を押さえる。残りが足りなければ、申請を受け付けない | `hold` | `hold` |
| 承認された | 押さえた金額を使ったことにする | `post` | `post` |
| 差し戻された、取り下げられて申請者に戻った、却下された、期限が切れた、申請者が申請をやめた | 押さえた金額を戻す | `void` | `release` |

申請の保存と帳簿の呼び出しは、一つのトランザクションで行われます。申請だけが残ったり、予算だけが減ったりすることはありません。

## 1. 帳簿を書く

```book title="project/shared/budget.book"
book budget v1
description "部の予算。購買の申請が出たら金額を押さえ、承認されたら使い、終わらなければ戻す"

unit JPY

account dept_budget(dept: string) : JPY
  description "部が使える残り"
  at least 0 refused as over_budget
account allocations : JPY outside
account payees : JPY outside

transfer allocate(dept: string, period: string, amount: JPY)
  description "部に配る、期間の予算"
  key dept, period
  move amount from allocations to dept_budget(dept)

transfer reserve(request: string, attempt: string, dept: string, amount: JPY)
  description "申請が出たら押さえ、承認されたら使い、却下、期限切れ、取り下げ、差し戻しなら戻す"
  key request, attempt
  pending expires after 365 days
  move amount from dept_budget(dept) to payees
```

| 書いたもの | 意味 |
|---|---|
| `unit JPY` | 数える単位。ここでは円 |
| `account dept_budget(dept: string)` | 部ごとの、使える残り |
| `at least 0 refused as over_budget` | 残りは 0 より小さくならない。小さくなるときは、`over_budget` という理由で拒否する |
| `account … outside` | 残りを数えない相手。`allocations` は予算の出どころ、`payees` は支払い先 |
| `transfer allocate(…)` | 予算を配る。`key dept, period` は、同じ部の同じ期間には一度だけ配る、という意味 |
| `transfer reserve(…)` | 申請のために押さえる。`key request, attempt` は、一つの申請の一回の提出につき一度だけ押さえる、という意味。同じ操作がもう一度届いても、二重には押さえない |
| `pending expires after 365 days` | まず押さえておき、あとで確定するか戻す。365 日たっても決まらなければ、期限切れになる。申請が終わるまでの最長の日数より、長くしておく |
| `move amount from … to …` | 金額を、左の勘定から右の勘定に動かす |

書き方の全部は、[chobo のページ](https://i2y.github.io/ritsu/chobo/ja/)にあります。

## 2. 申請の種類から呼ぶ

`project/purchase.ringi` に、帳簿を読む行と、`ticket` の中の三行を足します。

```ringi title="project/purchase.ringi" hl_lines="4"
use organization 組織(org) from "shared/company.ringi"
use calendar 本社(office) from "shared/office.cal"
use gate "shared/company.gate"
use book 予算(budget) from "shared/budget.book"
```

```ringi title="project/purchase.ringi" hl_lines="4 5 6"
  send back      to the requester, at most 2 times
  resubmit       within 5 business days
  withdraw       until it is decided
  on submitted   hold 予算.reserve(request: ticket.id, attempt: ticket.attempt, dept: requester.department, amount: 金額)
  on approved    post 予算.reserve(request: ticket.id, attempt: ticket.attempt)
  on sent back, rejected, withdrawn, expired  void 予算.reserve(request: ticket.id, attempt: ticket.attempt)
```

| 書いたもの | 意味 |
|---|---|
| `on submitted hold …` | 申請が出たときに押さえる。再申請のときも同じ |
| `on approved post …` | 承認されたら、使ったことにする |
| `on sent back, … void …` | 申請が申請者に戻ったときと、承認されずに終わったときに、押さえた金額を戻す。`sent back` には、申請者が取り下げたときも入る。`withdrawn` は、申請をやめたとき |
| `ticket.id`、`ticket.attempt` | 申請の番号と、その申請の何回目の提出かを表す番号 |
| `requester.department` | 申請者の部 |

社長のように部の上にいる人には、部がありません。部のない人が申請すると、押さえる部が決まらないので、申請は受け付けられません。`on submitted hold` の行の終わりに `when requester.department is not none` と書くと、部のない人の申請は、押さえずに受け付けます。`post` と `void` の行には書かなくてかまいません。

予算がないと、シナリオでも申請できなくなります。どのシナリオにも、`requester` の次の行に、前もって配っておく予算を書きます。

```ringi title="project/purchase.ringi" hl_lines="2"
  requester s1201
  given 予算.allocate(dept: "sales", period: "2026", amount: 10_000_000JPY)
```

検査して、コードを生成します。帳簿の Go のコードと SQL が `go/books/` にできます。

```sh
ritsu check project
go generate ./...
```

## 3. サーバーから呼ぶ

帳簿を呼ぶ `server.Books` を書きます。

```go title="books.go"
package shinsei

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"

	"example.com/acme/shinsei/go/books/budget"
	"example.com/acme/shinsei/go/units"
)

//go:embed go/books/budget.sql
var budgetSQL string

// reasons は、帳簿が受け付けなかった理由を、人が読む文にする。
var reasons = map[string][2]string{
	"over_budget": {"the department's budget is short", "部の予算が足りません"},
}

// Books は、申請の操作に合わせて、予算の帳簿を呼ぶ。
type Books struct{ Pool *pgxpool.Pool }

// Call は、帳簿を一度呼ぶ。tx は、申請を保存するトランザクション。
func (b Books) Call(ctx context.Context, tx any, c server.BookCall) (server.BookAnswer, error) {
	var q budget.Querier = b.Pool
	if t, ok := tx.(pgx.Tx); ok {
		q = t
	}
	reserve := budget.Postgres(q, "").Reserve
	str := func(name string) string { v, _ := c.Arg(name); s, _ := v.Str(); return s }
	num := func(name string) int64 { v, _ := c.Arg(name); n, _ := v.Num(); return n }
	key := budget.ReserveKey{Request: str("request"), Attempt: str("attempt")}
	var r budget.Result
	var err error
	switch c.Book + "." + c.Transfer + "." + c.Call {
	case "budget.reserve.hold":
		r, err = reserve.Hold(ctx, budget.ReserveArgs{Request: key.Request, Attempt: key.Attempt, Dept: str("dept"), Amount: units.JPY(num("amount"))})
	case "budget.reserve.post":
		r, err = reserve.Post(ctx, key, nil)
	case "budget.reserve.release": // .ringi の void
		r, err = reserve.Void(ctx, key)
	default:
		return server.BookAnswer{}, fmt.Errorf("no book call %s.%s.%s", c.Book, c.Transfer, c.Call)
	}
	if words, ok := reasons[r.Reason]; ok {
		return server.BookAnswer{Outcome: r.Outcome, Reason: words[0], ReasonJa: words[1]}, err
	}
	return server.BookAnswer{Outcome: r.Outcome, Reason: r.Reason}, err
}

// SetUpBooks は、帳簿の表を作り、部に予算を配る。何度呼んでも、配るのは一度。
func SetUpBooks(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, budgetSQL); err != nil {
		return err
	}
	for _, dept := range []string{"sales", "engineering", "general_affairs"} {
		if _, err := budget.Postgres(pool, "").Allocate.Do(ctx, budget.AllocateArgs{Dept: dept, Period: "2026", Amount: 1_000_000}); err != nil {
			return err
		}
	}
	return nil
}
```

次に、サーバーに渡します。データベースを使わないとき（`go test` や、`-db` のない起動）は、どの呼び出しも受け付ける試験用の帳簿にします。`shinsei.go` に、その帳簿を読み込む行と、`Books` の行を足します。

```go title="shinsei.go" hl_lines="3"
	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/orgfiles"
	"github.com/i2y/ringi-server/servertest"
```

```go title="shinsei.go" hl_lines="3"
		Organization: Organization(),
		Notifier:     &LogNotifier{Log: log, Lang: "ja"},
		Books:        &servertest.Books{},
	}, nil
```

`cmd/server/main.go` では、`-db` があるときに、本物の帳簿に替えます。

```go title="cmd/server/main.go" hl_lines="5 6 7 8 9 10"
	cfg, err := shinsei.Parts(log)
	if err != nil {
		return err
	}
	if stores.Pool != nil {
		if err := shinsei.SetUpBooks(ctx, stores.Pool); err != nil {
			return err
		}
		cfg.Books = shinsei.Books{Pool: stores.Pool}
	}
```

PostgreSQL のライブラリを直接使うようになったので、`go mod tidy` で `go.mod` に書き込んでから、試験します。

```sh
go mod tidy
go test ./...
```

データベースを渡して起動すると、予算が押さえられるようになります。

```sh
createdb shinsei
go run ./cmd/server -dev-login -db 'postgres:///shinsei'
```

## 次の年の予算と、増額

`SetUpBooks` は、起動のたびに `Allocate` を呼びますが、同じ部の同じ期間に配られるのは一度だけです。`Amount` を書き換えて起動し直しても、残りは変わりません。

| したいこと | 書き方 |
|---|---|
| 次の年の予算を配る | `Period` を `"2027"` にした `Allocate` を足す。使い残しは、次の年に繰り越される |
| 予算を増やす | `Period` を `"2026-add-1"` のような別の名前にして、増やす分だけを配る |

## 予算が足りないとき

申請は受け付けられず、何も残りません。API は 409 と理由を返します。

```
{"refused":"book","text":"the department's budget is short","text_ja":"部の予算が足りません","at":null}
```

`text` と `text_ja` は、`Books.Call` が `Reason` と `ReasonJa` に入れた文です。ページには、帳簿が受け付けなかったことと、そのすぐ下にこの文が出ます。

## 帳簿を別のデータベースに置く

帳簿がすでにほかのデータベースにあるなら、`server.Config` の `BooksApart` を `true` にします。トランザクションを一つにはできないので、サーバーは、申請を保存する前に押さえ、保存できなければ取り消します。詳しくは[設計書](https://github.com/i2y/ringi-server/blob/main/DESIGN.ja.md)の 3.2 にあります。

休暇の残りを使う例は、例のプロジェクトの `leave.ja.ringi` と `leave.book` です。
