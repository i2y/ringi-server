# temporal

このリポジトリのフレームワークのために、申請の種類が始める flow を Temporal で動かすモジュールです。ritsu の申請の種類が始める flow は三つあります。チケットが終わったあとに始める flow（`launch`）、エージェントのステップ（`ask_agent`）、メモ（`ask_note`）です。flow は dandori が Temporal の Go の SDK 向けに生成します。このモジュールはそれを始め、エージェントの答えとメモをサーバーに返します。フレームワークのモジュールがどの SDK にも依存しないよう、別のモジュールにしています。

名前は仮です。

英語版：[README.md](README.md)。設計：[DESIGN.ja.md](DESIGN.ja.md)。

## すること

- `Launch` は、することのキーをワークフロー ID にして flow を始めます。前に始めたキーなら何も始めません。
- `Ask` は、キーごとにこのモジュールの小さなワークフローを始めます。そのワークフローは flow を子ワークフローとして動かし、終わり方から答え（`ok`、`doubt`、メモの出力、`Failed`）を作り、アクティビティで `Server.Answered` に渡します。サーバーが受け取るまで、Temporal がリトライします。途中でプロセスが止まっても、答えは失われません。
- `FlowsOf` は、申請の種類の説明（`types/<種類>.json`）から Runner の flow を作ります。説明は、種類が使う flow を、ワークフローの型、タスクキュー、出力と一緒に挙げています。
- `Run` は、そのワークフローのワーカーと、このプロセスで動かす flow のワーカー（`Config.Workers`。タスクキューごと）を動かします。
- `CheckWorkers` は、Runner が実行を始めるすべてのタスクキューについて、ポーリングしているワーカーがいるかを Temporal に聞き、ワーカーのいないキューを挙げます。そのキューでの実行とそのチケットは、ワーカーが来るまで待つことになるからです。

`Runner` は `server.Flows` でもあり、`server.Agents` でもあります。

## つなぎ方

Temporal のサーバー、ritsu が生成したパッケージ（`ritsu gen --target go` が `flows/` の下に flow を書きます）、フレームワークが要ります。申請の種類が使う flow は、`temporal.FlowsOf` が種類の説明から読みます。その flow と、このプロセスで動かす flow のワーカー（タスクキューごと）を Runner に渡し、できた Runner をサーバーに渡します。`testdata/examples` の試験 `TestOneProcess` は、次のコードを動かしています。

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

`p` は ritsu が生成したパッケージで、申請の種類（`ticketkinds`）と requests のディレクトリから読みます。`c` は Temporal のサーバーのクライアントです（`client.Dial(client.Options{HostPort: "localhost:7233"})`）。`ctx` と `running` は、試験が二つのループを止めるための物で、`t.Fatal` の所では、使う人が自分の方法でエラーを扱います。サーバーのループは `ctx` が終わるまで返りません。Runner の `Run` は、ワーカーが始められなければすぐに、続けられない誤りでワーカーが止まればそのときに返ります。そのため、ワーカーを待つ所は、`CheckWorkers` の 30 秒を待たずに、そのエラーで終わります。受け付けを始めたあとも、サーバーは `stopped` を見張ります。ここのパッケージの種類は経費と契約なので、`FlowsOf` は `precheck_agent`、`travel_decision`、`pay`、`summarize` の四つの flow を挙げます。このプロセスが動かすのは、そのうちの二つ、日帰りの出張の決裁と支払いです。`travelAgents{}` は試験の偽の `Transport` です。flow の `Transport` に `nil` を渡すと、そのパッケージの既定の `Transport` になります。これは環境変数の鍵で、エージェントのプロバイダーを呼びます。`own` は使う人のタスク（`expense_payment.OwnTasks`）です。メモリの保管先、ヘッダーでのサインイン（`servertest.Header`）、`server.AllowAll`、`servertest.Books`、組織のファイルは、使う人が用意する物の代わりです。`CheckWorkers` がどのキューにもワーカーを見つけたら、サーバーが受け付けを始めます（`http.ListenAndServe(":8080", srv)`）。試験では、日帰りの出張が、申請からエージェントの決裁を経て支払いまで進みます。

`Workers` は、このプロセスで flow を動かすタスクキューを挙げます。ここにワーカーの無いタスクキューの flow は、別のプロセスが動かします。そのプロセスは、エージェントのプロバイダーの鍵を持ち、パッケージのワーカーを動かします。ここでは経費の確かめと契約の要約で、`expense_precheck.NewWorker(c, nil, nil, "").Run(worker.InterruptCh())` と、`contract_summary` の同じ物です。試験では、`TestOneProcess` が、そのようなプロセスと同じに、この二つのワーカーを例の前に動かします。ワーカーを動かすのは、このモジュールの試験が開発用サーバーに作る、この試験だけのネームスペースです。`c` も、そのネームスペースのクライアントです。そこをポーリングするほかの試験のワーカーはいないので、`CheckWorkers` が日帰りの出張のキューに見つけるワーカーは、このプロセスのワーカーです。出張を動かすのも、そのワーカーです。

`New` は、どの flow のタスクキューでもないキューのワーカーを受け付けません。`CheckWorkers` は、Runner が実行を始めるすべてのキュー（問い合わせの実行の `Config.TaskQueue` も含みます）をワーカーがポーリングするまで待ちます。Temporal への問いが失敗しても、もう一度聞きます。先にコンテキストが終われば、ワーカーが見つからなかったキューを挙げます。最後の問いが失敗したキューには、そのエラーを付けます。Temporal は、止まったワーカーも、最後のポーリングからしばらくのあいだ（開発用サーバーでは 5 分ほど）挙げるので、`CheckWorkers` は、止まったばかりのワーカーもいるものとして数えます。メモの出力は、flow が宣言した順に渡します。その順は、`FlowsOf` が種類の説明（`outputs`）から読みます。

## Temporal の中の名前

| 何か | 名前 |
|---|---|
| launch した flow、問い合わせた flow | ワークフロー ID `<チケット>/<回>/<通し番号>`、flow の型とタスクキュー |
| 問い合わせて答えるワークフロー | ワークフロー ID `<チケット>/<回>/<通し番号>/ask`、型 `requests.ask`、タスクキュー `requests.ask`（`Config.TaskQueue`） |
| 答えを渡す | アクティビティ `requests.answer` |

## 試験

```sh
cd temporal
go test ./...
go test -race ./...
```

試験は Temporal の開発用サーバーを自分で起動します。ダウンロードはしません。

| 環境変数 | 中身 | 無いとき |
|---|---|---|
| `TEMPORAL_CLI` | 開発用サーバーを動かす Temporal の CLI | SDK が OS の一時ディレクトリに置くファイル（`temporal-cli-go-sdk-<版>`）。それも無ければ、サーバーの要る試験は `SKIP` と書きます |
| `RITSU` | 例を生成する `ritsu` | `PATH` の `ritsu`。それも無ければ、`TestExamples` は `SKIP` と書きます |
| `RINGI_EXAMPLES` | 例のプロジェクト | `../examples/project` |
| `RINGI_EXAMPLES_GOMOD` | 例のモジュールに要件（と go.sum）を足す go.mod。モジュールのキャッシュだけで組む機械で、キャッシュにある版を使う物を指定します | 生成した flow が書いている要件だけを使います |

`TestFlowsOf` と `TestFlowsOfVersions` は、例の種類の説明（`../examples/requests`。無ければ `SKIP` と書きます）から flow を読みます。`TestCheckWorkers` は、それぞれのキューをワーカーがポーリングしているかを開発用サーバーに聞きます。問いの途中でコンテキストを終わらせたり、問いを失敗させたりするクライアントも通します。コンテキストがどこで終わっても、ワーカーのいないキューは同じように挙がり、失敗した問いはもう一度聞きます。

`TestExamples` は、経費と契約の種類とその flow を一時的なモジュールに生成し、そこへ `testdata/examples` を写して、その試験を動かします。README の例が、日帰りの出張のキューを自分のワーカーだけがポーリングするネームスペースで動く。エージェントが通す、疑う、失敗する、形の合わない答えを返す。契約のメモが付く、期限に遅れる。支払いを二度 launch しても一度だけ動く。サーバーが答えを受け取ってから返事を返すまでのあいだに、ワーカーが止まる。エージェントは偽の `Transport` で答えるので、ネットワークには何も出ません。生成した物は残しません。下の実行では、`TestExamples` は 82 秒かかり、そのうち 31 秒が生成したモジュールの試験でした。その大半は `TestAgentFails` です。この試験の flow は、エージェントを試し直すあいだに 10 秒、次に 20 秒待つからです。残りは、モジュールの生成とビルドです。`go test -v -run TestExamples .` で見た、生成したモジュールの試験は次のとおりです。

```
--- PASS: TestOneProcess (0.32s)
--- PASS: TestSummaryAttached (0.30s)
--- PASS: TestAgentAnswersOutOfShape (0.30s)
--- PASS: TestAgentPasses (0.30s)
--- PASS: TestSummaryLate (0.30s)
--- PASS: TestAgentDoubts (0.30s)
--- PASS: TestPaymentStartsOnce (0.45s)
--- PASS: TestWorkerStopsWhileAnswering (4.26s)
--- PASS: TestAgentFails (30.18s)
ok  	example.test/requests/driver	31.008s
--- PASS: TestExamples (81.94s)
```
