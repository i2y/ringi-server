# 承認の経路でエージェントに確かめさせる

承認の経路のステップを一つ、AI エージェントに答えさせます。エージェントが問題なしと答えれば次のステップへ進み、疑いがあれば、決めておいた人に回ります。出張の申請を、上長が承認する前に確かめる例で説明します。英語版は [agent-steps.md](agent-steps.md) です。

エージェントは、Temporal で動く処理の中で呼びます。先に[承認のあとの処理を始める](flows.ja.md#temporal-で動かす)の「Temporal で動かす」を読み、Temporal の CLI を用意してください。

## 1. プロジェクトを作る

`--temporal` を付けて、例のプロジェクトを作ります。

```sh
ritsu ringi init agent-example --module example.com/acme/agent-example --lang ja --temporal
cd agent-example
go mod tidy
```

すでに Temporal を使っているプロジェクトなら、そこに次のファイルを足します。

## 2. エージェントに聞くことを書く

`project/shared/check.flow` に、エージェントに渡す値と、聞くことを書きます。

```flow title="project/shared/check.flow"
workflow trip_check v1
description "出張の申請を、上長が承認する前にエージェントが確かめる"

enum Verdict = ok | doubt

record Reading
  verdict : Verdict
  reason  : string

inputs
  amount  : money[JPY]  range >=1JPY <=1_000_000JPY
  purpose : string

outputs
  verdict : Verdict
  reason  : string

task read(amount: money[JPY], purpose: string) -> Reading
  agent "出張の申請を、上長が承認する前に確かめます。目的から、どこへ何をしに行くのかが分かり、金額に無理がなければ ok、そうでなければ doubt と答えてください。doubt のときは、理由を一文で書いてください。"
  model "checker"
  url "http://127.0.0.1:9000/v1"
  timeout 60 seconds
  retry 2 times every 10 seconds

flow
  let r = read(amount: amount, purpose: purpose)
    on failure, timeout => let r = {verdict: doubt, reason: "エージェントが答えませんでした"}
  succeed verdict = r.verdict, reason = r.reason
```

| 書いたもの | 意味 |
|---|---|
| `inputs` | 申請から受け取る値。フォームの項目を、ID（`amount`、`purpose`）で受け取る。申請の番号が要るときは `request_id : string` を足す |
| `outputs` | 答え。確かめるステップでは、`verdict`（`ok` か `doubt`）と `reason`（理由の文）に決まっている |
| `agent "…"` | エージェントへの指示 |
| `model`、`url` | モデルの名前と、モデルを動かしているサーバー。[本物のモデルにつなぐ](#本物のモデルにつなぐ)で替える |
| `on failure, timeout => …` | エージェントが失敗したか、時間内に答えなかったときの答え。ここでは、疑いがあることにして人に回す |

## 3. 申請の経路に入れる

出張の申請を書きます。色の付いた行が、エージェントのためのものです。

```ringi title="project/trip.ringi" hl_lines="7 14 18 19 39 40 47-49 56 58 59"
request 出張(trip) v1 "出張の申請"
owner general_affairs

use organization 組織(org) from "shared/company.ringi"
use calendar 本社(office) from "shared/office.cal"
use gate "shared/company.gate"
use flow 確かめる(check) from "shared/check.flow"

form
  金額(amount)  : money[JPY] "金額"  range >=1JPY <=1_000_000JPY
  目的(purpose) : long text "目的"  count >=1 <=500

route
  確かめ(check)  エージェント
  上長(manager)  上長

who
  エージェント(agent)   = agent 確かめる, else 総務
  総務(general_affairs) = team general_affairs
  上長(manager)         = manager of requester

every stage
  due in 3 business days at end of day
  remind after 2 business days at 09:00
  when late expire
  when empty escalate

ticket
  submitted by   everyone except position president
  self approval  not allowed
  send back      to the requester, at most 2 times
  resubmit       within 5 business days
  withdraw       until it is decided

scenario エージェントが確かめて上長が承認する
  requester s1201
  form 金額 = 28_000JPY, 目的 = "大阪の取引先へ、新しい製品の説明に行く"
| at               | event                       | by    | -> stage | status      |
| 2026-11-16 10:00 | submit                      | s1201 | 確かめ   | in progress |
| 2026-11-16 10:01 | agent answered(verdict: ok) |       | 上長     | in progress |
| 2026-11-16 15:00 | approve                     | s1101 |          | approved    |

scenario エージェントが疑い総務部が確かめる
  requester s1201
  form 金額 = 300_000JPY, 目的 = "出張"
| at               | event                                                                 | by    | -> stage | status      |
| 2026-11-16 10:00 | submit                                                                | s1201 | 確かめ   | in progress |
| 2026-11-16 10:01 | agent answered(verdict: doubt, reason: "どこへ行くのかが分かりません") |       | 確かめ   | in progress |
| 2026-11-16 14:00 | approve                                                               | g1201 | 上長     | in progress |
| 2026-11-16 15:00 | approve                                                               | s1101 |          | approved    |

labels en
  request          "Trip request"
  金額             "Amount"
  目的             "Purpose"
  stage 確かめ     "Check"
  stage 上長       "Manager"
  who エージェント "Agent"
  who 総務         "General Affairs"
  who 上長         "Manager"
```

`agent 確かめる, else 総務` は、「`確かめる` の処理に答えさせ、答えが `ok` でなければ、総務部の人が答える」という意味です。

| エージェントの答え | 申請はどうなるか |
|---|---|
| `ok` | 次のステップに進む |
| `doubt` | `else` に書いた人が、そのステップに答える。理由は、申請のページのそのステップの下と、申請の履歴に出る |
| 処理が失敗した | `doubt` と同じ |
| 期限までに答えない | `when late` に書いたとおりになる。この例では期限切れで終わる |

`else` のあとが一人に決まるときは、`ticket` に `same approver approves once` と書いてあれば、エージェントが疑ったあとのそのステップを、その人のステップとして数えます。その人は疑いを見て答え、あいだにほかの人のステップが来なければ、そのあとの、その人だけに当たるステップは、その承認で済みます。`same approver approves once, at the last stage` なら、そのステップは、その人だけに当たるあとのステップのうち、最後のものを待ちます。エージェントが先に答えるステップを前の承認で済ませることはなく、ほかのステップがそれを待つこともありません。

検査します。`ritsu check` は、`.flow` の `inputs` と `outputs` が申請に合っているかも確かめます。

```sh
ritsu check project
```

## 4. ワーカーに足す

コードを作り直します。

```sh
go generate ./...
```

処理を動かすワーカーを、`flows.go` に足します。`NewWorker` の二番目の `nil` は、自分で書く関数がないこと、三番目の `nil` は、モデルの呼び方を決まったものにすることを表します。

```go title="flows.go" hl_lines="1"
	"example.com/acme/agent-example/go/flows/trip_check"
)

// Flows makes what starts the flows of the kinds of request on Temporal, through c: the server's
```

```go title="flows.go" hl_lines="3"
	return []worker.Worker{
		expense_payment.NewWorker(c, Payments{Log: log}, nil, ""),
		trip_check.NewWorker(c, nil, nil, ""),
	}
```

```sh
go test ./...
```

## 5. 試す

本物のモデルの代わりに、目的が 10 文字より短ければ疑う、小さなサーバーを作ります。

```go title="cmd/model/main.go"
// model は、モデルの代わりに答えるサーバー。手元で試すときに使う。
package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func main() {
	http.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		var asked struct{ Input string }
		var trip struct{ Purpose string }
		if json.NewDecoder(r.Body).Decode(&asked) != nil || json.Unmarshal([]byte(asked.Input), &trip) != nil {
			http.Error(w, "cannot read it", http.StatusBadRequest)
			return
		}
		answer := `{"answer":{"verdict":"ok","reason":""}}`
		if len([]rune(trip.Purpose)) < 10 {
			answer = `{"answer":{"verdict":"doubt","reason":"どこへ何をしに行くのかが分かりません"}}`
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"output": []any{map[string]any{
			"type": "message", "content": []any{map[string]any{"type": "output_text", "text": answer}},
		}}})
	})
	log.Fatal(http.ListenAndServe("127.0.0.1:9000", nil))
}
```

四つの端末で、次の四つを起動します。

1. Temporal の開発用サーバー

    ```sh
    temporal server start-dev
    ```

2. モデルの代わり

    ```sh
    go run ./cmd/model
    ```

3. ワーカー

    ```sh
    go run ./cmd/worker
    ```

4. 申請のサーバー

    ```sh
    go run ./cmd/server -dev-login
    ```

営業担当 1 で「出張の申請」を出します。目的を「大阪の取引先へ、新しい製品の説明に行く」と書くと、確かめのステップはすぐに終わり、営業一課長に通知が出ます（時刻は省きました）。

```
level=INFO msg=notice notice=waiting to="s1101 s1101@example.com" kind=trip title=出張の申請 title_lang=ja ticket=01M89R0QCFM6S9K1YQSMVVXB6R stage=manager
```

もう一つ、目的を「出張」とだけ書いて出します。今度は、総務部の二人に通知が出ます。

```
level=INFO msg=notice notice=waiting to="g1001 g1001@example.com, g1201 g1201@example.com" kind=trip title=出張の申請 title_lang=ja ticket=01M89R0TCPEP3XTSK6TJ6BT9QS stage=check
```

総務担当でログインし、申請のページを開くと、ステップの下に、エージェントが疑問を出したことと、その理由が出ています。承認すると、営業一課長に進みます。

## 本物のモデルにつなぐ

モデルを自分の会社のサーバーで動かしているなら、`model` にモデルの名前を、`url` にサーバーのアドレスを書きます。つなげるのは、Open Responses という公開の仕様に沿ったサーバーです。

モデルを提供している会社の API を使うなら、`url` を書かずに、`agent` の書き方で送り先を選びます。書き方、API キーを入れる環境変数、足すモジュールは、[dandori のページの「エージェント」](https://i2y.github.io/ritsu/dandori/ja/agents/)にあります。API キーが要るのはワーカーだけで、申請のサーバーには置きません。

## 承認者が読む要約を書かせる

エージェントに答えさせずに、承認者が読む要約だけを書かせることもできます。`route` に、承認者の代わりに `note from` と処理の名前を書きます。

```ringi
use flow 要約する(summarize) from "shared/summary.flow"

route
  要約(summary)  note from 要約する
  上長(manager)  上長
```

`.flow` の `outputs` が、あとのステップの承認者のページに出ます。期限までに要約が届かなければ、要約のないまま次のステップに進みます。[契約の例](../examples/project/contract.ja.ringi)と、その[処理](../examples/project/shared/summary.flow)に全体があります。
