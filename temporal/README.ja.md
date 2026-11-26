# Temporal で動かす flow（temporal）

パッケージ `temporal` は、申請が始める処理を Temporal で動かします。承認のあとの支払い、ステップに答えるエージェント、承認者のために要約を書くエージェントなどです。こうした処理を flow と呼びます。flow は ritsu の言語の一つ dandori で書き、ritsu が Go のコードを作ります。このパッケージは flow を始め、エージェントの答えをサーバーに返します。英語版は [README.md](README.md) です。

| したいこと | 読むもの |
|---|---|
| 承認のあとに処理をする | [承認のあとの処理を始める](../docs/flows.ja.md) |
| ステップをエージェントに答えさせる | [承認の経路でエージェントに確かめさせる](../docs/agent-steps.ja.md) |
| Go で組み立てたサーバーで使う | 下の[使い方](#使い方) |
| 動いているものを Temporal で探す | 下の[Temporal の中の名前](#temporal-の中の名前) |
| しくみとその理由を知る | [設計](DESIGN.ja.md) |

`ritsu ringi init` に `--temporal` を付けて作ったプロジェクトには、同じことをするコード（`flows.go` と `cmd/worker`）が入っています。フレームワークが SDK に依存しないように、独立した Go のモジュール（`github.com/i2y/ringi-server/temporal`）にしてあります。

## すること

`Runner` は、`server.Config` の `Flows` と `Agents` の両方になります。

| 関数 | すること |
|---|---|
| `FlowsOf` | 申請の種類が使う flow を、種類の説明（`types/<種類>.json`）から読む。ワークフローの型、タスクキュー、出力が付いている |
| `New` | flow と Temporal のクライアントから `Runner` を作る |
| `Runner.Run` | このプロセスのワーカーを、コンテキストが終わるまで動かす。ワーカーが始められなければすぐに、続けられないエラーで止まればそのときに戻る |
| `Runner.CheckWorkers` | flow が動くどのタスクキューにもワーカーが付くまで待つ。先にコンテキストが終われば、ワーカーのいないキューの名前を返す |
| `Runner.Launch` | 承認のあとの flow を始め、終わったら、どうなったかをサーバーに伝える。サーバーが呼ぶ |
| `Runner.Ask` | エージェントの flow を始め、終わったら答えをサーバーに返す。サーバーが呼ぶ |

flow は、サーバーが渡すキーをワークフロー ID にして始めます。同じキーがもう一度来ても、何も始めません。`Launch` と `Ask` は、それぞれこのパッケージの小さなワークフローを始め、それが flow を子ワークフローとして動かします。`Launch` のワークフローは、flow がどうなったか（済んだなら終わったときの値、失敗したならその理由）を `Server.WorkEnded` に伝え、申請のページにそれが出ます。`Ask` のワークフローは、flow の終わり方から答え（`ok`、`doubt`、要約の出力、失敗）を作り、`Server.Answered` に渡します。どちらもアクティビティで伝え、サーバーが受け取るまで Temporal がアクティビティをやり直すので、途中でプロセスが止まっても、伝えることは失われません。

## 使い方

Temporal のサーバーと、ritsu が作った Go のコード（`ritsu gen --target go` が `flows/` の下に flow を書きます）を用意します。flow と、このプロセスで動かすワーカー（タスクキューごと）を Runner に渡し、その Runner をサーバーに渡します。`testdata/examples` の試験 `TestOneProcess` は、次のコードを動かしています。

```go
p, err := server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, os.DirFS(filepath.Join("..", "..", "requests")))
if err != nil {
	t.Fatal(err)
}
listed, err := temporal.FlowsOf(p) // the flows the kinds name, as their descriptions list them
if err != nil {
	t.Fatal(err)
}
flows, err := temporal.New(c, temporal.Config{
	Flows: listed,
	// the workers this process runs, by their task queues: the others run elsewhere
	Workers: map[string]func(client.Client) worker.Worker{
		travel_decision.TaskQueue: func(c client.Client) worker.Worker { return travel_decision.NewWorker(c, nil, travelAgents{}, "") },
		expense_payment.TaskQueue: func(c client.Client) worker.Worker { return expense_payment.NewWorker(c, own, nil, "") },
	},
})
if err != nil {
	t.Fatal(err)
}
srv, err := server.New(server.Config{
	Packages:      []*server.Package{p},
	Store:         memstore.New(),
	Authenticator: servertest.Header{},
	Gate:          server.AllowAll,
	Organization:  orgfiles.New(os.DirFS(filepath.Join("..", "..", "project"))),
	Books:         &servertest.Books{},
	Flows:         flows,
	Agents:        flows,
})
if err != nil {
	t.Fatal(err)
}
if err := srv.ImportOrganization(ctx); err != nil {
	t.Fatal(err)
}
running.Add(2)
go func() { defer running.Done(); srv.Run(ctx) }()
stopped := make(chan error, 1) // why the flows' workers stopped: one did not start, or cannot go on
go func() { defer running.Done(); stopped <- flows.Run(ctx, srv) }()
// Before it serves: a worker polls every task queue the flows run on.
ready, stop := context.WithTimeout(ctx, 30*time.Second)
defer stop()
checked := make(chan error, 1)
go func() { checked <- flows.CheckWorkers(ready) }()
select {
case err := <-stopped:
	t.Fatal(err)
case err := <-checked:
	if err != nil {
		t.Fatal(err)
	}
}
```

### 自分のものに置き換えるところ

| コードの中 | 何か |
|---|---|
| `p` | ritsu が作ったパッケージ。申請の種類（`ticketkinds`）と requests のディレクトリから読む。ここでは経費と契約で、使う flow は `precheck_agent`、`travel_decision`、`pay`、`summarize` の四つ |
| `c` | Temporal のクライアント。`client.Dial(client.Options{HostPort: "localhost:7233"})` |
| `own` | 自分で書く、支払いのタスク（`expense_payment.OwnTasks`） |
| `travelAgents{}` | エージェントの代わりに答える、試験の `Transport`。`nil` を渡すと、flow は環境変数の鍵でエージェントのプロバイダーを呼ぶ |
| `memstore.New()`、`servertest.Header{}`、`server.AllowAll`、`servertest.Books{}`、組織のファイル | 試験のための部品。自分のものを渡す（[部品とパッケージ](../docs/parts.ja.md)） |
| `ctx`、`running`、`t.Fatal` | 試験が二つのループを止めるのに使うものと、エラーのときの止め方 |

### どのプロセスがどの flow を動かすか

`Workers` には、このプロセスで flow を動かすタスクキューを書きます。ここにないタスクキューの flow は、別のプロセスが動かします。そのプロセスは、flow のパッケージのワーカーと、エージェントのプロバイダーの鍵を持ちます。この例では、残りの二つは経費の確認と契約の要約です。そのプロセスは `expense_precheck.NewWorker(c, nil, nil, "").Run(worker.InterruptCh())` を、契約の要約では `contract_summary` の同じものを動かします。`New` は、どの flow のものでもないタスクキューのワーカーを受け付けません。

### 受け付けを始める前に、ワーカーを待つ

タスクキューにワーカーがいなければ、flow は待ち、その申請も待つことになります。上のコードは、`CheckWorkers` が済んでから受け付けを始めます（`http.ListenAndServe(":8080", srv)`）。`Run` がエラーで戻れば、30 秒を待たずに止まります。受け付けを始めたあとも、`stopped` を見続けます。

- `CheckWorkers` は、Runner が処理を始めるすべてのキューと、このパッケージのワークフローのキュー（`Config.TaskQueue`）を見ます。Temporal への問い合わせが失敗したら、もう一度問い合わせます。
- Temporal は、止まったワーカーも、最後のポーリングからしばらく（開発用サーバーでは 5 分ほど）一覧に残します。止まったばかりのワーカーは、いるものとして数えます。

## Temporal の中の名前

| 何か | 名前 |
|---|---|
| `launch` や `ask_agent`、`ask_note` で始めた flow | ワークフロー ID `<チケット>/<回>/<通し番号>`、flow の型とタスクキュー |
| flow を launch するワークフロー | ワークフロー ID `<チケット>/<回>/<通し番号>/launch`、型 `requests.launch`、タスクキュー `requests`（`Config.TaskQueue`） |
| flow がどうなったかをサーバーに伝える | アクティビティ `requests.ended` |
| 答えを求めるワークフロー | ワークフロー ID `<チケット>/<回>/<通し番号>/ask`、型 `requests.ask`、タスクキュー `requests`（`Config.TaskQueue`） |
| 答えをサーバーに渡す | アクティビティ `requests.answer` |

## 試験

```sh
cd temporal
go test ./...
go test -race ./...
```

試験は、Temporal の開発用サーバーを自分で起動します。ダウンロードはしません。

| 環境変数 | 中身 | ないとき |
|---|---|---|
| `TEMPORAL_CLI` | 開発用サーバーを動かす Temporal の CLI | SDK が OS の一時ディレクトリに置くファイル（`temporal-cli-go-sdk-<版>`）。それもなければ、サーバーの要る試験は `SKIP` と書く |
| `RITSU` | 例を生成する `ritsu` | `PATH` の `ritsu`。それもなければ、`TestExamples` は `SKIP` と書く |
| `RINGI_EXAMPLES` | 例のプロジェクト | `../examples/project` |
| `RINGI_EXAMPLES_GOMOD` | 例のモジュールに要件（と go.sum）を足す go.mod。モジュールのキャッシュだけでビルドする環境で、キャッシュにある版のものを指定する | 生成した flow が書いている要件だけを使う |

| 試験 | 確かめること |
|---|---|
| `TestFlowsOf`、`TestFlowsOfVersions` | 例の種類の説明（`../examples/requests`。なければ `SKIP` と書く）から読んだ flow |
| `TestCheckWorkers` | どのキューにもワーカーがいるかを、開発用サーバーに問い合わせる。問い合わせの途中でコンテキストが終わるとき、問い合わせが失敗するときも |
| `TestExamples` | 経費と契約とその flow を一時的なモジュールに生成し、`testdata/examples` をコピーして、下の試験を動かす |

- `TestOneProcess`：この文書のコードを、ほかの試験のワーカーがいないネームスペースで動かす。日帰りの出張が、申請からエージェントの決裁を経て、支払いまで進む。
- エージェントが、通す、疑う、失敗する、形の合わない答えを返す。
- 契約の要約が付く。遅すぎて付かない。
- 支払いを二度始めても、一度だけ動き、申請のページに支払いが済んだことと、その番号が出る。
- サーバーが答えを受け取ってから返事を返すまでのあいだに、ワーカーが止まる。

エージェントの代わりに試験の `Transport` が答えるので、ネットワークには何も出ません。生成したものは残りません。`go test -v -run TestExamples .` の出力は次のとおりです。いちばん長いのは `TestAgentFails` で、その flow は、エージェントを試し直すまでに 10 秒、次に 20 秒待ちます。

```
--- PASS: TestOneProcess (0.33s)
--- PASS: TestSummaryAttached (0.32s)
--- PASS: TestAgentPasses (0.32s)
--- PASS: TestAgentAnswersOutOfShape (0.32s)
--- PASS: TestSummaryLate (0.37s)
--- PASS: TestAgentDoubts (0.37s)
--- PASS: TestPaymentStartsOnce (0.73s)
--- PASS: TestWorkerStopsWhileAnswering (4.34s)
--- PASS: TestAgentFails (30.21s)
ok  	example.test/requests/driver	31.167s
--- PASS: TestExamples (52.32s)
```
