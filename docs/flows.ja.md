# 承認のあとの処理を始める

申請が承認されたら支払う、発注する、アカウントを作る。そうした処理を、サーバーに始めさせます。購買の申請が承認されたときの発注を例に説明します。社員が使っている申請に足すときは、先に[版を上げます](types.ja.md#使われている申請の決まりを変える)。英語版は [flows.md](flows.md) です。

## 二つのやり方

| やり方 | 向いているとき | 要るもの |
|---|---|---|
| [ほかのシステムに頼む](#ほかのシステムに頼む) | 発注や会計のシステムがすでにあり、依頼を一つ送れば、あとはそのシステムが行う | Go の関数一つ |
| [Temporal で動かす](#temporal-で動かす) | 何段かの処理を、失敗したらやり直しながら、自分で最後まで進める | Temporal のサーバー、一段ごとの Go の関数 |

Temporal は、処理の手順を一段ずつ記録しながら進めるサーバーです。プログラムやコンピューターが止まっても、続きから進めます。

どちらのやり方でも、始めた処理の結果は、申請のサーバーには戻りません。発注番号や失敗は、申請のページには出ません。申請した人に知らせたいときは、処理の中でメールを送るなどします。

## ほかのシステムに頼む

[1. 動かして、申請を承認する](start.ja.md)で作ったプロジェクトに足します。

### 1. 処理の名前と、渡す値を書く

`project/shared/order.flow` に、処理の名前と、渡す値を書きます。処理の手順を書く、ritsu の言語の一つ、dandori のファイルです。

```flow title="project/shared/order.flow"
workflow order v1
description "承認された購買の発注。発注のシステムが行う"

inputs
  request_id : string
  amount     : money[JPY]  range >=1JPY <=10_000_000JPY
  purpose    : string

task place_order(request_id: string, amount: money[JPY], purpose: string)
  idempotent

flow
  place_order(request_id: request_id, amount: amount, purpose: purpose)
```

`inputs` が、渡す値の名前と型と範囲です。`task` と `flow` は処理の手順で、Temporal で動かすときに使います。ほかのシステムに頼むときには使いませんが、省けないので、このとおり一段だけ書いておきます。

### 2. いつ始めるかを書く

`project/purchase.ringi` に二行を足します。`on approved` は「承認されたとき」、かっこの中は渡す値です。

```ringi title="project/purchase.ringi" hl_lines="4"
use organization 組織(org) from "shared/company.ringi"
use calendar 本社(office) from "shared/office.cal"
use gate "shared/company.gate"
use flow 発注(order) from "shared/order.flow"
```

```ringi title="project/purchase.ringi" hl_lines="4"
  send back      to the requester, at most 2 times
  resubmit       within 5 business days
  withdraw       until it is decided
  on approved    launch 発注(request_id: ticket.id, amount: 金額, purpose: 目的)
```

検査して、コードを作り直します。`ritsu check` は、渡す値が `inputs` の型と範囲に合っているかも確かめます。

```sh
ritsu check project
go generate ./...
go get go.temporal.io/sdk@v1.49.0
go mod tidy
```

`go generate` は、この処理を Temporal で動かすためのコードも `go/flows/` に作ります。使わなくてもコンパイルはされるので、`go get` で Temporal の SDK を足しておきます。

### 3. 依頼を送る関数を書く

`server.Flows` を書きます。関数は `Launch` 一つです。

```go title="webhook.go"
package shinsei

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	server "github.com/i2y/ringi-server"
)

// Webhook は、承認のあとの処理を、ほかのシステムに頼む。
type Webhook struct{ URL string }

// Launch は、処理を一つ頼む。
func (w Webhook) Launch(ctx context.Context, l server.Launch) error {
	var args []server.NamedValue
	if err := json.Unmarshal(l.Args, &args); err != nil {
		return err
	}
	values := map[string]any{}
	for _, a := range args {
		if s, ok := a.Str(); ok {
			values[a.Name] = s
		} else if n, ok := a.Num(); ok {
			values[a.Name] = n
		}
	}
	body, err := json.Marshal(values)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL+"/"+l.Flow, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", l.Key.String())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err // サーバーが、あとでもう一度呼ぶ
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("the system answered %s", res.Status)
	}
	return nil
}
```

| `Launch` のフィールド | 内容 |
|---|---|
| `Flow` | 処理の名前。`use flow 発注(order)` の `order` |
| `Args` | 渡す値。名前と値の並び。値は `Str()`（文字列）、`Num()`（数）、`Truth()`（真偽）で取り出す |
| `Key` | この依頼の番号。やり直しのときも同じ番号になる |
| `Kind`、`Ticket` | 申請の種類と、申請の番号 |

`shinsei.go` の `Parts` に、`Flows` を足します。

```go title="shinsei.go" hl_lines="3"
		Organization: Organization(),
		Notifier:     &LogNotifier{Log: log, Lang: "ja"},
		Flows:        Webhook{URL: "http://127.0.0.1:9000"},
	}, nil
```

### 4. 試す

発注のシステムの代わりに、届いた依頼を表示するだけのプログラムを作ります。

```go title="cmd/orders/main.go"
// orders は、届いた依頼を表示するだけのサーバー。発注のシステムの代わりに、手元で試すときに使う。
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
)

func main() {
	log.Fatal(http.ListenAndServe("127.0.0.1:9000", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Printf("%s %s\nIdempotency-Key: %s\n%s\n", r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"), body)
	})))
}
```

端末を二つ使い、このプログラムと、申請のサーバーを起動します。

```sh
go run ./cmd/orders
```

```sh
go run ./cmd/server -dev-login
```

営業担当 1 として 120,000 円の購買を申請し、営業一課長と営業部長として承認します。`orders` の端末に、依頼が出ます。

```
POST /order
Idempotency-Key: 01M89PB269CQ4GTPETPAEMR9QQ/1/15
{"amount":120000,"purpose":"新しいメンバーのノートパソコン","request_id":"01M89PB269CQ4GTPETPAEMR9QQ"}
```

`Launch` がエラーを返すと、サーバーは間隔を延ばしながら、成功するまで呼び直します。同じ依頼には同じ `Idempotency-Key` が付くので、頼まれる側は、同じキーの依頼を一度だけ行うように作ります。

## Temporal で動かす

`--temporal` を付けてプロジェクトを作ると、Temporal で動かす例が入ります。申請の種類は二つになります。購買の申請と、承認されると支払いを始める経費の精算です。まず、この例を動かします。

### 例を動かす

プロジェクトの外のディレクトリで、新しいプロジェクトを作ります。

```sh
cd ..
ritsu ringi init pay-example --module example.com/acme/pay-example --lang ja --temporal
cd pay-example
go mod tidy
```

動かすには、Temporal の CLI（[入れ方](https://docs.temporal.io/cli)、英語）が要ります。三つの端末で、次の三つを起動します。

1. Temporal の開発用サーバー

    ```sh
    temporal server start-dev
    ```

2. 処理を動かすプログラム。ワーカーと呼ぶ

    ```sh
    go run ./cmd/worker
    ```

3. 申請のサーバー

    ```sh
    go run ./cmd/server -dev-login
    ```

営業担当 1 で「経費の精算」を申請し、営業一課長で承認します。ワーカーの端末に、支払ったことと、申請者に知らせたことが出ます（時刻は省きました）。

```
level=INFO msg=paid request=01M89KP9C3A4YDKERR3S1Y8VY0 requester=s1201 amount=4800 purpose=お客様の所へ行くタクシー
level=INFO msg=told request=01M89KP9C3A4YDKERR3S1Y8VY0 text="申請 01M89KP9C3A4YDKERR3S1Y8VY0 の支払いが済みました（PAY-01M89KP9C3A4YDKERR3S1Y8VY0）"
```

実際に支払うわけではありません。ログに書くだけの関数を、自分の会社の処理に替えて使います。

### 処理を書く三つのファイル

いつ始めるかは、申請の種類に書きます。

```ringi title="project/expense.ringi"
use flow 支払い(pay) from "shared/pay.flow"
```

```ringi title="project/expense.ringi"
  on approved    launch 支払い(request_id: ticket.id, requester: requester.id, amount: 金額, purpose: 目的)
```

どんな手順かは、`.flow` のファイルに書きます。この例は、支払う、申請者に知らせる、の二段です。支払いには一回ごとに一日の制限時間があり、失敗したら、10 分後から間隔を倍にしながら、三回までやり直します。書き方は、[dandori のページ](https://i2y.github.io/ritsu/dandori/ja/)にあります。

```flow title="project/shared/pay.flow"
task pay_requester(request_id: string, requester: string, amount: money[JPY], purpose: string) -> Payment
  timeout 1 day
  retry 3 times every 10 minutes
  idempotent

task tell_requester(request_id: string, text: string)
  idempotent

flow
  let p = pay_requester(request_id: request_id, requester: requester, amount: amount, purpose: purpose)
  tell_requester(request_id: request_id, text: "申請 {request_id} の支払いが済みました（{p.reference}）")
  succeed reference = p.reference
```

実際に何をするかは、Go で書きます。`.flow` の `task` 一つに、関数が一つです。

```go title="payments.go"
// PayRequester pays the requester the amount of the request, and answers the payment's reference.
func (p Payments) PayRequester(_ context.Context, args map[string]any) (any, error) {
	id, _ := args["request_id"].(string)
	p.Log.Info("paid", "request", id, "requester", args["requester"], "amount", args["amount"], "purpose", args["purpose"])
	return map[string]any{"reference": "PAY-" + id}, nil
}
```

ワーカー（`cmd/worker`）が、この関数を呼びます。ワーカーは、申請のサーバーと別のプログラムです。会計システムのパスワードのようなものは、ワーカーの側だけに置けます。

### 自分のプロジェクトに足す

すでにあるプロジェクトには、例のファイルをコピーして足します。自分のプロジェクトのディレクトリで、次のコマンドを実行します。`--module` には、自分のプロジェクトと同じモジュールを書きます。

```sh
ritsu ringi init ../flow-files --module example.com/acme/shinsei --lang ja --temporal
cp ../flow-files/flows.go ../flow-files/payments.go .
cp -R ../flow-files/cmd/worker cmd/
cp ../flow-files/project/shared/pay.flow project/shared/
```

`cmd/server/main.go` は、自分で直したところを残して、次のところを書き足します。

```go title="cmd/server/main.go" hl_lines="3 5 9 11-14"
import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.temporal.io/sdk/client"
	sdklog "go.temporal.io/sdk/log"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/serve"
```

```go title="cmd/server/main.go" hl_lines="4 7 13"
func main() {
	var flags serve.Flags
	flags.Register(flag.CommandLine)
	temporal := flag.String("temporal", "localhost:7233", "the address of the Temporal server the flows run on")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(flags, *temporal, log); err != nil {
		server.LogStop(log, "the server stopped", err)
		os.Exit(1)
	}
}

func run(flags serve.Flags, temporal string, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
```

`run` の最後の `return stores.Serve(…)` の行は、次のものに替えます。

```go title="cmd/server/main.go" hl_lines="5-34"
	cfg, err := shinsei.Parts(log)
	if err != nil {
		return err
	}
	c, err := client.Dial(client.Options{HostPort: temporal, Logger: sdklog.NewStructuredLogger(log)})
	if err != nil {
		return fmt.Errorf("-temporal %s: %w", temporal, err)
	}
	defer c.Close()
	flows, err := shinsei.Flows(c, cfg.Packages...)
	if err != nil {
		return err
	}
	cfg.Flows, cfg.Agents = flows, flows
	ctx, cancel := context.WithCancelCause(ctx)
	var running sync.WaitGroup
	err = stores.Serve(ctx, cfg, func(srv *server.Server) {
		shinsei.Started(cfg, srv)
		// 答えを待つ処理（エージェントのステップなど）の答えを、サーバーに渡すワーカー
		running.Go(func() { cancel(flows.Run(ctx, srv)) })
		running.Go(func() { // 処理のワーカーがいなければ、ログに出す
			ready, stop := context.WithTimeout(ctx, 30*time.Second)
			defer stop()
			if err := flows.CheckWorkers(ready); err != nil && ctx.Err() == nil {
				log.Warn("the flows wait for their workers: go run ./cmd/worker", "err", err)
			}
		})
	})
	cancel(nil)
	running.Wait()
	if cause := context.Cause(ctx); err == nil && !errors.Is(cause, context.Canceled) {
		err = cause
	}
	return err
}
```

次に、購買の申請に二行を足します。

```ringi title="project/purchase.ringi" hl_lines="4"
use organization 組織(org) from "shared/company.ringi"
use calendar 本社(office) from "shared/office.cal"
use gate "shared/company.gate"
use flow 支払い(pay) from "shared/pay.flow"
```

```ringi title="project/purchase.ringi" hl_lines="4"
  send back      to the requester, at most 2 times
  resubmit       within 5 business days
  withdraw       until it is decided
  on approved    launch 支払い(request_id: ticket.id, requester: requester.id, amount: 金額, purpose: 目的)
```

検査すると、渡す値が合わないというエラーが出ます。

```sh
ritsu check project
```

```
…
エラー[ritsu E210]: project/purchase.ringi:37: `購買` は flow `支払い` の入力 `amount` に 10_000_000JPY を渡すことがありますが、入力が受け取るのは money[JPY] >=1JPY <=1_000_000JPY です
…
```

購買の金額は 10,000,000 円までなので、`pay.flow` の入力 `amount` の範囲を広げます。

```flow title="project/shared/pay.flow" hl_lines="4"
inputs
  request_id : string
  requester  : string
  amount     : money[JPY]  range >=1JPY <=10_000_000JPY
  purpose    : string
```

コードを作り直し、モジュールを足して、試験します。

```sh
ritsu check project
go generate ./...
go get github.com/i2y/ringi-server/temporal@v0.1.0
go mod tidy
go test ./...
```

処理の種類を増やしたら、`flows.go` の `Workers` に、そのワーカーを足します。

### 失敗したとき

| 起きること | どうなるか |
|---|---|
| ワーカーが止まっている | 処理は Temporal に残り、ワーカーを起動すると始まる。承認は済んでいる |
| Temporal が止まっている | 申請のサーバーが、始められるまで間隔を延ばしながら繰り返す。承認は済んでいる。申請のサーバーを起動するときには、Temporal が動いている必要がある |
| 申請のサーバーが、処理を始めた直後に止まった | 起動し直したサーバーが、同じキーでもう一度始める。Temporal は、同じキーの処理を一度しか動かさない |
| 一段が失敗した | `.flow` の `retry` のとおりにやり直す |
| やり直しても、最後まで失敗した | 処理は失敗で終わる。申請は承認されたままで、申請のサーバーには知らされない |

最後まで失敗した処理は、Temporal の画面か、次のコマンドで探せます。

```sh
temporal workflow list --query 'ExecutionStatus="Failed"'
```

原因を直したら、最初の段からもう一度動かせます。`-w` には、上のコマンドに出た `WorkflowId` を書きます。

```sh
temporal workflow reset -w '01M89KP9C3A4YDKERR3S1Y8VY0/1/9' --type FirstWorkflowTask --reason '会計システムが直った'
```

一段の関数は、成功したかどうか分からないまま切れたときに、もう一度呼ばれることがあります。`idempotent` は、二度呼ばれても一度だけ支払うように作った関数だ、という印です。申請の番号を会計システムに渡すなどして、二度目に気づけるようにします。

## もっと詳しく

ワーカーがそろっていることの確かめ方、Temporal の中での名前、ほかのプログラムへの組み込み方は、[モジュール temporal](../temporal/README.ja.md) にあります。承認の経路の途中でエージェントに確かめさせる方法は、[承認の経路でエージェントに確かめさせる](agent-steps.ja.md)にあります。
