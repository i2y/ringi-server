# flow とエージェントのステップ（temporal）

ringi のサーバーの申請が始める flow を、Temporal で動かすモジュールです。申請の種類が始める flow は三つあります。チケットが終わったあとに動かす flow（`launch`）、エージェントのステップ（`ask_agent`）、メモ（`ask_note`）です。flow は、dandori が Temporal の Go の SDK 向けに生成します。このモジュールは flow を始め、エージェントの答えとメモをサーバーに返します。フレームワークのモジュールが SDK に依存しないように、独立した Go のモジュールにしてあります。

しくみとその理由は、[設計](DESIGN.ja.md)にあります。英語版は [README.md](README.md) です。

## すること

- `Launch` は、フレームワークが渡すキーをワークフロー ID にして、flow を始めます。前に始めたキーなら、何も始めません。
- `Ask` は、キーごとに、このモジュールの小さなワークフローを始めます。そのワークフローは、flow を子ワークフローとして動かし、終わり方から答え（`ok`、`doubt`、メモの出力、`Failed`）を作り、アクティビティで `Server.Answered` に渡します。サーバーが答えを受け取るまで、Temporal がリトライします。途中でプロセスが止まっても、答えは失われません。
- `FlowsOf` は、申請の種類の説明（`types/<種類>.json`）から、Runner の flow を作ります。説明には、種類が使う flow が、ワークフローの型、タスクキュー、出力と一緒に並んでいます。
- `Run` は、そのワークフローのワーカーと、このプロセスで動かす flow のワーカー（`Config.Workers`。タスクキューごと）を動かします。
- `CheckWorkers` は、Runner が実行を始めるすべてのタスクキューについて、ポーリングしているワーカーがいるかを Temporal に問い合わせ、ワーカーのいないキューの名前を返します。そのキューでの実行とそのチケットは、ワーカーが来るまで待つことになるためです。

`Runner` は、`server.Flows` と `server.Agents` の両方として使えます。

## つなぎ方

Temporal のサーバー、ritsu が生成したパッケージ（`ritsu gen --target go` が `flows/` の下に flow を書きます）、フレームワークを用意します。申請の種類が使う flow は、`temporal.FlowsOf` が種類の説明から読みます。その flow と、このプロセスで動かす flow のワーカー（タスクキューごと）を Runner に渡し、その Runner をサーバーに渡します。`testdata/examples` の試験 `TestOneProcess` は、次のコードを動かしています。

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

このコードの中身は、次のとおりです。

- `p` は、ritsu が生成したパッケージです。申請の種類（`ticketkinds`）と requests のディレクトリから読みます。
- `c` は、Temporal のサーバーのクライアントです（`client.Dial(client.Options{HostPort: "localhost:7233"})`）。
- `ctx` と `running` は、試験が二つのループを止めるのに使います。`t.Fatal` のところは、自分のエラーの扱いに置き換えてください。
- メモリのストア、人をヘッダーで渡すログイン（`servertest.Header`）、`server.AllowAll`、`servertest.Books`、組織のファイルは、自分のサーバーのものに置き換えてください。
- ここのパッケージの種類は経費と契約なので、`FlowsOf` は `precheck_agent`、`travel_decision`、`pay`、`summarize` の四つの flow を返します。このプロセスが動かすのは、そのうちの二つ、日帰りの出張の決裁と支払いです。
- `travelAgents{}` は、試験用の偽の `Transport` です。`nil` を渡すと、flow のパッケージの既定の `Transport` になり、環境変数の鍵でエージェントのプロバイダーを呼びます。`own` は、自分で書くタスク（`expense_payment.OwnTasks`）です。

サーバーの `Run` は、`ctx` が終わるまで戻りません。Runner の `Run` は、ワーカーが始められなければすぐに、続けられないエラーでワーカーが止まればそのときに戻ります。このためワーカーを待つところは、`CheckWorkers` の 30 秒を待たずに、そのエラーで終わります。受け付けを始めたあとも、`stopped` にエラーが来るのを待ち続けます。`CheckWorkers` がどのキューにもワーカーを見つけたら、サーバーが受け付けを始めます（`http.ListenAndServe(":8080", srv)`）。試験では、日帰りの出張が、申請からエージェントの決裁を経て、支払いまで進みます。

`Workers` には、このプロセスで flow を動かすタスクキューを書きます。ここにないタスクキューの flow は、別のプロセスが動かします。そのプロセスは、エージェントのプロバイダーの鍵を持ち、パッケージのワーカーを動かします。この例では経費の確認と契約の要約で、`expense_precheck.NewWorker(c, nil, nil, "").Run(worker.InterruptCh())` と、`contract_summary` の同じものです。`TestOneProcess` は、この二つのワーカーを、別のプロセスの代わりに例の前に起動します。場所は、このモジュールの試験が開発用サーバーに作る、この試験だけのネームスペースです。`c` も、そのネームスペースのクライアントです。そこをポーリングするほかの試験のワーカーはないので、`CheckWorkers` が日帰りの出張のキューに見つけるのはこのプロセスのワーカーで、出張を動かすのもそのワーカーです。

`New` は、どの flow のものでもないタスクキューのワーカーを受け付けません。`CheckWorkers` は、Runner が実行を始めるすべてのキュー（答えを求めるワークフローの `Config.TaskQueue` も含みます）をワーカーがポーリングするまで待ちます。Temporal への問い合わせが失敗したら、もう一度問い合わせます。先にコンテキストが終わったときは、ワーカーが見つからなかったキューの名前を返します。最後の問い合わせが失敗したキューには、そのエラーも付けます。Temporal は、止まったワーカーも、最後のポーリングからしばらく（開発用サーバーでは 5 分ほど）一覧に残します。そのため `CheckWorkers` は、止まったばかりのワーカーもいるものとして数えます。メモの出力は、flow が宣言した順に渡します。順番は、`FlowsOf` が種類の説明（`outputs`）から読みます。

## Temporal の中の名前

| 何か | 名前 |
|---|---|
| `launch` や `ask_agent`、`ask_note` で始めた flow | ワークフロー ID `<チケット>/<回>/<通し番号>`、flow の型とタスクキュー |
| 答えを求めるワークフロー | ワークフロー ID `<チケット>/<回>/<通し番号>/ask`、型 `requests.ask`、タスクキュー `requests.ask`（`Config.TaskQueue`） |
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
| `TEMPORAL_CLI` | 開発用サーバーを動かす Temporal の CLI | SDK が OS の一時ディレクトリに置くファイル（`temporal-cli-go-sdk-<版>`）。それもなければ、サーバーの要る試験は `SKIP` と書きます |
| `RITSU` | 例を生成する `ritsu` | `PATH` の `ritsu`。それもなければ、`TestExamples` は `SKIP` と書きます |
| `RINGI_EXAMPLES` | 例のプロジェクト | `../examples/project` |
| `RINGI_EXAMPLES_GOMOD` | 例のモジュールに要件（と go.sum）を足す go.mod。モジュールのキャッシュだけでビルドする環境で、キャッシュにある版のものを指定します | 生成した flow が書いている要件だけを使います |

`TestFlowsOf` と `TestFlowsOfVersions` は、例の種類の説明（`../examples/requests`。なければ `SKIP` と書きます）から flow を読みます。`TestCheckWorkers` は、どのキューもワーカーがポーリングしているかを、開発用サーバーに問い合わせます。問い合わせの途中でコンテキストを終わらせるクライアントや、問い合わせを失敗させるクライアントも使います。コンテキストがどこで終わっても、ワーカーのいないキューは同じように返り、失敗した問い合わせはやり直します。

`TestExamples` は、経費と契約の種類とその flow を一時的なモジュールに生成し、そこに `testdata/examples` をコピーして、次の試験を動かします。

- README の例を、日帰りの出張のキューを自分のワーカーだけがポーリングするネームスペースで動かす
- エージェントが、通す、疑う、失敗する、形の合わない答えを返す
- 契約のメモが付く、期限に遅れる
- 支払いを二度 launch しても、一度だけ動く
- サーバーが答えを受け取ってから返事を返すまでのあいだに、ワーカーが止まる

エージェントは偽の `Transport` で答えるので、ネットワークには何も出ません。生成したものは残りません。下の実行では、`TestExamples` に 82 秒かかり、そのうち 31 秒が生成したモジュールの試験でした。大半は `TestAgentFails` で、この試験の flow は、エージェントを試し直すあいだに 10 秒、次に 20 秒待ちます。残りは、モジュールの生成とビルドです。`go test -v -run TestExamples .` で見た、生成したモジュールの試験は次のとおりです。

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
