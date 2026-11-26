# 設計：flow を Temporal で動かす

このモジュールは、申請の種類が始める flow を Temporal で動かす。チケットが終わったあとに始める flow（`launch`）、エージェントのステップ（`ask_agent`）、メモ（`ask_note`）の三つである。flow は、dandori が Temporal の Go の SDK 向けに生成した物を使う（`ritsu gen --target go` が `flows/` の下に書く）。フレームワークのモジュールはどの SDK にも依存せず、これらの処理を `Flows` と `Agents` の二つのインターフェースに渡す。このモジュールは、その両方を Temporal で実装する。

英語版：[DESIGN.md](DESIGN.md)。

## 1. すること

- **launch。** キーごとに、このモジュールの小さなワークフロー（以下「launch の実行」）を始める。launch の実行は、キーをワークフロー ID にして flow を子ワークフローとして動かし、flow がどうなったか（済んだなら終わったときの値、失敗したならその理由）を、フレームワークの `Server.WorkEnded` に伝える。伝えるのはアクティビティで、Temporal が済むまでリトライする。同じキーでもう一度始めても何も始まらず、成功として返す。フレームワークは、同じ処理を二度行うことがあるからである。
- **問い合わせ。** キーごとに、このモジュールの小さなワークフロー（以下「問い合わせの実行」）を始める。問い合わせの実行は、キーをワークフロー ID にして flow を子ワークフローとして動かし、flow の結果を読み、答えをフレームワークの `Server.Answered` に渡す。渡すのはアクティビティで、Temporal が済むまでリトライする。flow が終わってから答えを渡すまでのあいだにプロセスが止まっても、答えは失われない。問い合わせの実行はまだ終わっておらず、アクティビティがもう一度試されるからである。
- **ワーカー。** 問い合わせの実行と launch の実行、それらのアクティビティは、サーバーのプロセスにある Runner 自身のワーカーで動く（アクティビティがサーバーを呼ぶため）。flow は dandori が生成したそれぞれのワーカーで動く。同じプロセスでも、別のプロセスでもよい。

承認そのもの（`next`、時計、処理の一覧）は Temporal を使わない。フレームワークは Temporal 無しで動き、Temporal を使うのは flow だけである。

## 2. API

パッケージは `temporal`（インポートパスは `github.com/i2y/ringi-server/temporal`）である。SDK のパッケージ `go.temporal.io/sdk/temporal` と同じ名前なので、両方を使うファイルでは片方に別名を付ける。

### 2.1 生成した flow を渡す

dandori が生成する flow のパッケージは、どれも同じ形をしている。定数 `WorkflowType` と `TaskQueue`、型 `WorkflowOutput`、それに `NewWorker(c, own, transport, deployment)` がある。`NewWorker` は、使う人のタスクと、エージェントが外を呼ぶための `Transport` を受け取り、そのタスクキューのワーカーを作る。申請の種類は、flow を `use flow <名前> from "<ファイル>.flow"` で付けた名前で呼ぶ。この名前は、ファイルの中のワークフローの名前とは別である。ritsu が書く申請の種類ごとの説明（`types/<種類>.json`）は、その種類が使う flow を挙げている（`flows`。ringi の DESIGN 12.2）。それぞれの名前（`alias`）、dandori が付けたワークフローの型とタスクキュー（`workflow_type`、`task_queue`）、flow が宣言した順の出力（`outputs`）である。`FlowsOf` は、そこから Runner の `Flow` を作る。

```go
p, err := server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, os.DirFS("requests"))
flows, err := temporal.FlowsOf(p)
```

```go
// Flow は、申請の種類が挙げる flow と、それを Temporal のどこで動かすか。FlowsOf が、種類の説明から作る。
type Flow struct {
	// Name は、申請の種類が flow に付けた名前。種類は、この名前で flow を呼ぶ（説明の flow の
	// `alias`）。
	Name string
	// Kind は、その申請の種類だけに使うとき、種類の名前。二つの種類が一つの名前を別々の flow に
	// 付けるときに使う。"" はすべての種類。
	Kind string
	// Type と TaskQueue は、flow のワークフローの型とタスクキュー（`workflow_type` と
	// `task_queue`。生成したパッケージの WorkflowType と TaskQueue）。
	Type      string
	TaskQueue string
	// Outputs は、メモの出力の名前を、flow が宣言した順に並べた物（`outputs`）。メモには、この順で
	// 出力を渡す（ringi の DESIGN 15）。無ければ、実行が返した順のまま。
	Outputs []string
}

// FlowsOf は、パッケージの申請の種類が動かす flow を、種類の説明が挙げるとおりに返す。
func FlowsOf(pkgs ...*server.Package) ([]Flow, error)
```

ある名前を挙げるどの種類も、その名前に同じ flow（型、タスクキュー、出力が同じ）を付けていれば、すべての種類に使う一つの `Flow`（`Kind` が `""`）にする。例の英語と日本語の種類は、どの名前にも同じ flow を付けている。種類によって別の flow を付けている名前は、種類ごとの `Flow` にする。一つの種類だけの `Flow` は、すべての種類の `Flow` より先に選ぶ。`alias`、`workflow_type`、`task_queue` のどれかが無い flow と、一つの種類が二つの版で一つの名前に別々の flow を付けているときは、エラーにする。launch と問い合わせは種類と名前だけで flow を呼ぶので、その二つを見分けられないからである。種類の版が一つの名前に同じ flow を付けていれば、一つにする。

### 2.2 Runner

```go
type Config struct {
	// Flows は、申請の種類が始め、問い合わせる flow（FlowsOf）。
	Flows []Flow
	// Workers は、このプロセスで動かす flow のワーカー（Run）。ポーリングするタスクキューごとに、生成した
	// パッケージのワーカーを作る関数（その NewWorker に、使う人のタスクと、エージェントが外へ出る
	// Transport を渡す）。ここにワーカーの無いタスクキューの flow は、別のプロセスが動かす。
	Workers map[string]func(c client.Client) worker.Worker
	// TaskQueue は、問い合わせの実行と launch の実行、それらがサーバーに伝えることのタスクキュー
	// （既定は DefaultTaskQueue の "requests"）。
	TaskQueue string
	// AnswerTimeout は、答えか、launch した flow がどうなったかをサーバーに伝える一回の試しに
	// かけてよい時間（既定は 1 分）。それを超えた試しと、ワーカーが止まった試しは、もう一度試される。
	AnswerTimeout time.Duration
	// WorkerOptions は、問い合わせの実行と launch の実行のワーカーのオプション。
	WorkerOptions worker.Options
}

func New(c client.Client, cfg Config) (*Runner, error)

func (r *Runner) Launch(ctx context.Context, l server.Launch) error // server.Flows
func (r *Runner) Ask(ctx context.Context, a server.Ask) error       // server.Agents

// Answerer は、答えと、launch した flow がどうなったかの渡し先。*server.Server がそれである。
type Answerer interface {
	Answered(ctx context.Context, key server.EffectKey, a server.Answer) (server.Reply, error)
	WorkEnded(ctx context.Context, key server.EffectKey, e server.WorkEnd) (server.Reply, error)
}

// Worker は、Config.TaskQueue で、問い合わせの実行と launch の実行、それらがサーバーに伝えることを
// 受け持つワーカー。
func (r *Runner) Worker(to Answerer) worker.Worker
// Run は、Worker(to) と Config.Workers の flow のワーカーを、ctx が終わるか、ワーカーが続けられない
// エラーで止まるまで動かす。
func (r *Runner) Run(ctx context.Context, to Answerer) error
// CheckWorkers は、Runner が実行を始めるタスクキューのそれぞれを、ワーカーがポーリングしているかを、
// どのキューにもワーカーがいるか ctx が終わるまで、Temporal に聞く。
func (r *Runner) CheckWorkers(ctx context.Context) error
```

`New` は I/O をしない。名前、型、タスクキューの無い `Flow`、一つの種類に同じ名前の `Flow` が二つあるとき、`Config.Workers` のタスクキューに nil の関数を渡したとき（「Config.Workers has no worker for the task queue …」）、`Config.Flows` のどの flow のタスクキューでもないキューのワーカーがあるとき（「Config.Workers has a worker for the task queue …, which no flow of Config.Flows is on」）は受け付けない。サーバーは Runner を受け取って作り（`Config.Flows` と `Config.Agents`）、Runner はそのサーバーに答える。そのため、サーバーは `New` ではなく、ワーカーを作るとき（`Worker`、`Run`）に渡す。

`Worker` は、`Config.TaskQueue` で問い合わせの実行と launch の実行、それらがサーバーに伝えることを受け持つワーカー（以下「Runner 自身のワーカー」）を返す。`Run` はそのワーカーと、`Workers` の関数が作るワーカー（タスクキューごとに一つ）を始め、ctx が終わるか、ワーカーが続けられないエラーで止まるまで戻らない。始められないワーカーがあれば、それまでに始めたワーカーを止めて、そのエラーを返す。ctx が終われば `ctx.Err()` を、Runner 自身のワーカーがエラーで止まればそのエラーを返す。

`CheckWorkers` は、Runner が実行を始めるタスクキューのそれぞれについて、ポーリングしているワーカーがいるかを Temporal に聞く（`DescribeTaskQueue`）。問い合わせの実行と launch の実行を受け持つ `Config.TaskQueue`（Runner 自身のワーカーのキュー）と、`Config.Flows` のそれぞれの flow のタスクキューである。flow をどのプロセスが動かすかは問わない。ワーカーのいないタスクキューの実行は、ワーカーが来るまで待ち、そのチケットも待つ。`CheckWorkers` は、250 ミリ秒ごとにすべてのキューについて聞き直し、どのキューにもワーカーがいると Temporal が答えたら nil を返す。失敗した問いは、エラーの種類によらず、次の回にもう一度聞く。Temporal がまだ起動の途中のことも、一回の呼び出しだけが失敗することもあるからである。その前に ctx が終われば、問いと問いのあいだでも問いの途中でも、エラーを一つ返す。このエラーは、ワーカーが見つからなかったキューを、それぞれについて Temporal が最後に答えた内容で挙げる（「no worker polls the task queues …: their runs wait until one does」）。ワーカーがいないと答えたキューは名前だけ、最後の問いが失敗したキューはそのエラーを付けて（「(the last question to Temporal failed: …)」）、Temporal がまだ何も答えていないキューはそのことを付けて（「(Temporal has not answered)」）挙げる。途中で ctx が終わった問いは答えとして扱わず、それより前の答えをそのまま使う。返すエラーは、最後の問いが失敗したキューのエラーをラップする。呼ぶのはワーカーがそろってから、つまり、このプロセスのワーカーを `Run` が始め、ほかのワーカーを動かすプロセスも起動したあとである。

`CheckWorkers` が確かめるのは、それぞれのキューを最近ポーリングしたワーカーがいることで、いまポーリングしていることではない。Temporal は、止まったワーカーも、最後のポーリングからしばらくのあいだは、そのキューをポーリングしているワーカーとして挙げる（開発用サーバーでは、止めたワーカーが 5 分ほど挙がっていた）。そのため、止めたばかりのワーカーでも確かめは通り、そのキューの実行は、ワーカーのいないキューと同じように待つ。

### 2.3 ワーカーをどこで動かすか

`Config.Workers` は、このプロセスで flow を動かすタスクキューを挙げる。`Run` は、`Workers` のワーカーと並べて、Runner 自身のワーカーをいつも動かす。ここにワーカーの無いタスクキューの flow は、別のプロセスが動かす。そのプロセスは、エージェントのプロバイダーの鍵を持ち、パッケージのワーカーを動かす。

```go
w := expense_precheck.NewWorker(c, nil, nil, "")
if err := w.Run(worker.InterruptCh()); err != nil {
	log.Fatal(err)
}
```

- **一つのプロセスで。** `FlowsOf` が挙げるすべての flow のタスクキューのワーカーを `Workers` に渡すと、`Run` が Runner 自身のワーカーと並べて全部を動かす。小さな配置の形である。
- **二つ以上のプロセスで。** サーバーのプロセスは、一部の flow のワーカーを動かすか、どれも動かさない。`Workers` が無ければ、問い合わせの実行と launch の実行だけを受け持つ。ほかの flow は、そのワーカーを動かすプロセスで動く。README の例は、日帰りの出張の決裁（`travel_decision`）と支払い（`expense_payment`）をサーバーのプロセスで動かし、経費の確かめ（`expense_precheck`）と契約の要約（`contract_summary`）を別のプロセスに任せる。

Runner 自身のワーカーは、`Answered` と `WorkEnded` を呼べる所、つまりサーバーのプロセスで動かす（一つの保管先に複数のプロセスがあるなら、どれでもよい）。サーバーが受け付けを始める前に、どのタスクキューにも、このプロセスか別のプロセスのワーカーがいるかを `CheckWorkers`（2.2）で確かめる。

クライアントは使う人の物で、Runner はすべての flow と、問い合わせの実行、launch の実行をそのクライアントで始める。そのため、ワーカーは、そのクライアントが書いたペイロードを読めなければならない。flow が `history encrypted` と書いているときは、そのパッケージの `Dial` が作るクライアント（ペイロードがコーデックを通る `client.Client`）を Runner に渡し、ワーカーも同じコーデックのクライアントで動かす。

## 3. flow を始める（launch）

`Launch` は、処理に書かれた flow（`Launch.Flow` を、`Launch.Kind` の一覧から探す）の launch の実行を始める。型は `requests.launch`、ワークフロー ID は `<キー>/launch`（`LaunchID(key)`）、タスクキューは `Config.TaskQueue` で、ID が重なれば断り、使われている ID ならエラーにする（`WorkflowExecutionErrorWhenAlreadyStarted`）。入力には、問い合わせの実行と同じく（4.1）、実行に要る物をすべて入れる。キー、種類、flow の名前と型とタスクキュー、flow の入力、出力の順、`AnswerTimeout` である。

ID が使われているために断られたときは、成功として返す。前に始めた実行があるからである。それ以外のエラーは返し、フレームワークが処理を繰り返す（2 秒後、それから倍にして 10 分まで、終わり無く）。そのため、始められなかった flow（Temporal に届かない、`Config.Flows` に無い）も、始められるようになった時点で始まる。そのあいだ、チケットのカードには、処理を始められずにやり直していると出る。

launch の実行は、次の二つをする。

1. flow を子ワークフローとして動かす。ワークフロー ID はキー（`<チケット>/<回>/<通し番号>`、`EffectKey.String`）、型とタスクキューはその `Flow` の物、入力は引数を一つの JSON オブジェクトにした物（5 章）である。同じ ID を使い直せるのは、その ID の実行が失敗したあとだけにする（`WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY`）。flow が失敗したあとで launch の実行をリセットすると、flow がもう一度動くようにするためである。生成したパッケージの `Start` は、flow だけの実行のオプションを付けるが、Runner はそれを呼ばない。launch する flow と問い合わせる flow を、一つの一覧から始めるためである。
2. flow がどうなったかを、アクティビティ `requests.ended` で `WorkEnded` に伝える。リトライは、問い合わせの実行が答えを渡すときと同じである（4.1）。

| flow の終わり方 | サーバーに伝えること |
|---|---|
| 合う出力で終わった | 済んだ。出力は、`Flow.Outputs` の順に並べた名前つきの値（5 章） |
| 合わない出力で終わった | 失敗した。理由（`the outputs do not fit: …`）を付ける |
| エラーで終わった（一段が最後まで失敗した、時間切れ、実行がキャンセルや強制終了された） | 失敗した。理由は、エラーの連なりのいちばん内側のアプリケーションのエラー（一段か flow が失敗したときのエラー）の文。それが無ければ、そのエラー自身の文 |

理由は、サーバーが受け付ける形（`server.WorkEnd`）にする。サーバーが理由に許さない文字は空白にし、`server.MaxReason` 文字より長ければ切って「…」を付ける。launch の実行がキャンセルされても、サーバーには伝える。キャンセルすると flow もキャンセルされ、カードには失敗したと出る。

| `WorkEnded` の返事 | アクティビティ | launch の実行の終わり方 |
|---|---|---|
| `OK` | 済む | 済む |
| `NotFound`（そのキーで launch した物が無い）か `Invalid` | 失敗し、リトライしない（`requests.not_found`、`requests.invalid`） | 失敗する。管理者が見るため |
| エラー（保管先の失敗） | リトライする | （試しが成功したとき） |

最後まで失敗した flow は、原因を直してから、launch の実行を最初のワークフロータスクまでリセットすると、もう一度動く（`temporal workflow reset -w '<キー>/launch' --type FirstWorkflowTask`）。flow は同じキーでもう一度動き、サーバーにももう一度伝える。処理が最後に伝えたことが残る（`Server.WorkEnded`）。

Temporal が使われた ID を断るのは、終わった実行を持っているあいだ（ネームスペースのリテンション）だけである。フレームワークは `Launch` が戻るとすぐに処理を済みにするので、二度目の開始はその繰り返しのうちに来る。リテンションが切れるよりずっと前である。

## 4. flow に問い合わせ、答えを返す

### 4.1 問い合わせの実行

`Ask` は問い合わせの実行を始める。型は `requests.ask`、ワークフロー ID は `<キー>/ask`、タスクキューは `Config.TaskQueue` で、ID の扱いは launch と同じである（同じキーの二度目の `Ask` は何も始めず、成功として返す）。入力には、実行に要る物をすべて入れる。キー、種類、ステップ、メモかどうか、flow の名前と型とタスクキュー、flow の入力、メモの出力の順、`AnswerTimeout` である。そのため、動いている実行は、あとのプロセスの設定に左右されない。問い合わせの実行は、次の三つをする。

1. flow を子ワークフローとして動かす。ワークフロー ID はキー、型とタスクキューは flow の物で、ID が重なれば断る。flow のタスクは、それぞれのタイムアウトとリトライを持ったままである（dandori の `agent` タスクの `timeout`、`retry`、`on failure`）。
2. 子ワークフローの終わり方から答えを作る（4.2）。これはワークフローの中のコードで、履歴にある物だけを読む。
3. アクティビティ `requests.answer` で、答えを `Answered` に渡す。Temporal は済むまでリトライする。2 秒後、それから倍にして 10 分おきまで、終わり無く試す。一回の試しは `AnswerTimeout` までである。

| `Answered` の返事 | アクティビティ | 問い合わせの実行の終わり方 |
|---|---|---|
| `OK`（チケットが動いた） | 済み | `{"outcome": "moved"}` で終わる |
| `Refused`（問い合わせのあとでチケットが先へ進んでいた。`stale`、`state`） | 済み。断りも答えである | `{"outcome": "refused", "refused": "stale"}` で終わる |
| `NotFound`（そのキーで何も問い合わせていない）、`Invalid` | 失敗し、リトライしない（`requests.not_found`、`requests.invalid`） | 失敗で終わる。管理者が見る |
| エラー（保管の失敗） | リトライする | （試しが成功したとき） |

問い合わせの実行が返す型は `AskResult`、そのワークフロー ID は `AskID(key)` である。

### 4.2 `Answered` に渡す物

| flow の終わり方 | エージェントのステップ（`ask_agent`） | メモ（`ask_note`） |
|---|---|---|
| 合う出力で終わった | `Verdict`（`ok` か `doubt`）と `Reason`（無ければ `""`） | `Outputs`：出力を名前つきの値にした物（5 章） |
| 合わない出力で終わった（`ok` でも `doubt` でもない、または無い判定、文字列でない理由、メモに持てない出力） | `Failed` | `Failed` |
| 失敗した（エラー、タイムアウト、キャンセル、強制終了、ID が使われていた） | `Failed` | `Failed` |
| まだ終わっていない | 何も渡さない | 何も渡さない |

flow が答えなかった理由は、`requests.answer` の入力とワーカーのログに書く。管理者のためで、サーバーには `Failed` だけを渡す。フレームワークは、これをチケットのイベントにする（ringi の DESIGN 15.1 と 15.2）。

- `ok` はエージェントのステップを通す（履歴に `agent ok`。理由はコメントになる）。
- `doubt` と `Failed` は、そのステップを `else` の人に回す（理由を付けた `agent doubt`、`agent failed`）。エージェントは却下しない。
- メモの `Outputs` は、そのあとの承認カードに載る（`note attached`）。`Failed` なら、チケットはメモ無しで先へ進む（`note missing`。理由は `flow failed`）。
- ステップの期限までに来ないメモは、答えが無いのと同じである。チケット自身の時計（`late`）が、メモ無しで先へ進める（`note missing`）。そのあとで flow が終われば、その答えは `stale` で断られ、問い合わせの実行はその断りで終わる。

タスクの失敗を受け止める flow（例の `on failure, timeout => …`）は、失敗ではなく `doubt` で終わる。エージェントの失敗をどう扱うかは flow が決め、Runner は flow の結果をそのまま渡す。

### 4.3 答えを失わない

flow の結果は、Temporal が問い合わせの実行の履歴に持っている。`Answered` が受け取るまで、問い合わせの実行は終わらず、アクティビティが予定されたまま残る。試しの途中でワーカーが止まれば、その試しはタイムアウト（`AnswerTimeout`）になり、あとで動いたワーカーがもう一度試す。試しがサーバーに届いたあとで返事だけが失われることもある。このとき、次の試しは同じキーで二度目を渡す。`Answered` はこれを受け止める。答えたキーの二度目は何も変えず、一度目の返事（もう一度 `OK`、または同じ断り）を返す。

### 4.4 チケットが動いたとき、キャンセルしたとき

フレームワークは、問い合わせた動きが返したチケットの版を付けて答えを渡す。そのあとでチケットが動いていれば（人の編集でエージェントにもう一度問い合わせた、申請者が取り下げた、メモの期限が過ぎた）、`next` は答えを `stale` か `state` で断る。アクティビティはその断りを済みとして扱う。答えが要らなくなっても flow はキャンセルしない。flow のしたことは履歴に残っており、編集による新しい問い合わせは新しいキーで、別の実行になる。

管理者は問い合わせの実行をキャンセルできる。そのとき flow もキャンセルされ、flow が終わったあとで、サーバーに `Failed`（flow が答えを返して終わっていればその答え）を渡す。チケットは待つのをやめる。答えは、キャンセルの届かないコンテキストから渡す。問い合わせの実行を強制終了すると、答えは渡らず、flow も強制終了する。チケットはステップの期限まで待つ。

## 5. 値

処理は引数を、メモは出力を、約束の名前つきの値で持つ。`[{"name": …, "value": …}]` の形で、値は `{"str":{"v":…}}`、`{"num":{"v":…}}`、`{"truth":{"v":…}}`、`{"rows":{"v":[[…], …]}}`（名前つきの値の行）、`"nothing"` のどれかである。

| 約束 | flow の入力（JSON） | flow の出力（JSON）→ 約束 |
|---|---|---|
| `str` | 文字列（テキスト、選択肢、日付） | 文字列 → `str` |
| `num` | 数（書かれたまま） | 整数 → `num`（小数は合わない） |
| `truth` | `true` か `false` | → `truth` |
| `rows` | 行ごとに一つのオブジェクトを並べた配列 | オブジェクトの配列 → `rows` |
| `"nothing"` | `null` | `null` → `"nothing"` |
| | | 単独のオブジェクトと、オブジェクト以外を並べた配列は合わない |

入力は一つの JSON オブジェクトで、引数の順に並べる。申請の種類は、flow が宣言したより多くの引数を渡すことがある（メモの flow にはフォーム全体を渡す）。生成したワークフローは、宣言した入力だけを読み、ほかは読まない。

メモの出力は、`Flow.Outputs` の順（`FlowsOf` が読む、種類の説明の `outputs` の順）に並べ、そのあとにほかの出力を実行が返した順に並べる。この順は意味を持つ。`next` は渡された順に出力を持ち、画面もその順に見せるからである。行の中の値は、実行が返した順のままである。

## 6. Temporal の中の名前

| 何か | 名前 |
|---|---|
| launch した flow の実行 | ワークフロー ID `<チケット>/<回>/<通し番号>`、flow の型とタスクキュー。launch の実行の子ワークフロー |
| 問い合わせた flow の実行 | 同じ。問い合わせの実行の子ワークフロー |
| launch の実行 | ワークフロー ID `<チケット>/<回>/<通し番号>/launch`、型 `requests.launch`、`Config.TaskQueue`（`requests`） |
| launch した flow がどうなったかを伝える | アクティビティ `requests.ended`。同じタスクキュー |
| 問い合わせの実行 | ワークフロー ID `<チケット>/<回>/<通し番号>/ask`、型 `requests.ask`、`Config.TaskQueue`（`requests`） |
| 答えを渡す | アクティビティ `requests.answer`。同じタスクキュー |

問い合わせの実行と launch の実行のコードは、このモジュールの一部である。その履歴は、このモジュールの後の版がリプレイする。予定する物を変えるときは `workflow.GetVersion` を通す。

## 7. 試験

`go test ./...` は、パッケージの試験の初めに Temporal の開発用サーバー（`testsuite.StartDevServer`）を一つ起動する。CLI は `TEMPORAL_CLI` で指定したファイルを使い、無ければ、SDK が OS の一時ディレクトリに置くファイル（`temporal-cli-go-sdk-<版>`）を使う。試験は CLI をダウンロードしない。ファイルが無ければ、サーバーの要る試験は `SKIP` と書いて通る。開発用サーバーは、`default` のほかに、README の例（`TestOneProcess`）のためのネームスペース `one-process` を作る。エージェントは偽の `Transport` で答え、ネットワークには何も出ない。

- `values_test.go`（サーバー無し）：値の両方向の変換、メモの出力の、宣言の順の並びと宣言が無いときの並び、出力から作る答え。launch した flow の終わり方からサーバーに伝えること（`TestEndedOf`）。宣言の順の出力、合わない出力、エラーのいちばん内側の文、サーバーが受け付ける形にした理由。
- `flows_test.go`：`TestFlowsOf` は、例の種類の説明（`../examples/requests`。無ければ `SKIP` と書く）から flow を読む。六つの flow（`provision`、`summarize`、`precheck_agent`、`travel_decision`、`pay`、`order`）が、それぞれ英語と日本語の種類に共通の一つの `Flow` になり、出力が宣言の順に並び、`New` がそれを受け付ける。日本語の経費が `pay` に別の flow を付けると、二つの種類それぞれの `Flow` になり、どちらの種類も自分の flow を選ぶ。タスクキューの無い flow はエラーになる。`TestFlowsOfVersions`：一つの種類の二つの版が、一つの名前に同じ flow を付けていれば一つになり、別々の flow ならエラーになる。`TestCheckWorkers`（開発用サーバーで）：ワーカーのいない flow のタスクキューを、ワーカーが来るまで挙げる。来れば、すべてのキューにワーカーがいる。`DescribeTaskQueue` が開発用サーバーに聞く前に ctx を終えたり失敗したりできるクライアントでも試す。ワーカーのいるキューについての二つ目の問いの途中で ctx が終わっても、問いと問いのあいだで終わったときと同じエラーになる。最初の問いの途中で ctx が終われば、すべてのキューを、まだ答えていないキューとして挙げる。失敗する問いは聞き直し、最後の問いのエラーを付けて挙げ、そのエラーをラップする。一度だけ失敗し、そのあと答えが返る問いなら、確かめは通る。
- `runner_test.go`：試験のために書いた、生成した物と同じ形のワークフローと、キーごとに答えを覚える偽のサーバーで、Runner を動かす。`New` が受け付けない設定（flow の設定、`Workers` で nil の関数を渡したタスクキュー、どの flow のタスクキューでもないキューのワーカー）。二度始めても、実行が終わったあとに始めても、launch は一度だけ動き、終わったときの値をサーバーに一度だけ伝える。最後まで失敗した flow は、エラーの文を付けて失敗と伝え、launch の実行をリセットすると、同じキーで flow がもう一度動き、済んだと伝える。そのキーで launch した物の無い launch の実行は、一度試して失敗で終わる。問い合わせが `ok`、`doubt`、`Failed`、メモの出力を宣言の順で（宣言が無ければそのままの順で）渡す。断りで問い合わせの実行が終わり、エラーはリトライし、`NotFound` で失敗する。二度問い合わせても一度だけ動く。キャンセルした問い合わせの実行が `Failed` を渡す。答えを渡す途中でワーカーを止め、別のワーカーを起こしても、答えは一度だけ受け取られる。`Run` が問い合わせの実行のワーカーと `Workers` のワーカーを動かし、ctx で終わる。
- `examples_test.go`：例をフレームワークの上で動かす。`ritsu`（`RITSU`、または `PATH` の `ritsu`）で、例（`RINGI_EXAMPLES`、無ければフレームワークの `examples/project`）から経費と契約の種類とその flow を、試験のために作ったモジュールに生成する。そこへ `testdata/examples` を写し、その試験を動かす。外の試験が `-race` なら `-race` を付け、開発用サーバーのアドレスとネームスペース `one-process` を、環境変数（`RINGI_TEMPORAL_ADDRESS`、`RINGI_TEMPORAL_NAMESPACE`）で渡す。`ritsu` か例が無ければ `SKIP` と書く。モジュールには、生成した flow が `doc.go` に書いているモジュールを要件として入れる。モジュールのキャッシュだけで組む機械では、`RINGI_EXAMPLES_GOMOD` に、キャッシュにある版を使う go.mod を指定する。その要件と、隣の go.sum も入れる。生成した物は残さない。生成した flow には、エージェントのプロバイダーとモデルの名前が入っているからである（例の flow が選んでいる）。
- `testdata/examples`：メモリの保管先と、試験が設定する時計で、二つの種類を動かす。flow は、生成した種類の説明から `FlowsOf` が作り、そのワーカーは、別のプロセスが動かすのと同じように試験が動かす。エージェントの確かめが通る（チケットは上長へ進む）、疑う、プロバイダーが失敗する（flow がリトライのあとで `doubt` と答える）、形の合わない答えを返す（flow が失敗し、`agent failed`）。契約のメモが宣言の順で付く、期限のあとに来る（チケットはメモ無しで進み、遅れた答えは `stale` で断られる）。エージェントが承認した日帰りの出張の支払いが、もう一度 launch しても一度だけ動き、カードに支払いが済んだことと支払いの番号が出る。サーバーが答えを受け取ってから返事を返すまでのあいだに、問い合わせの実行のワーカーが止まる。README の例（`TestOneProcess`）は、ネームスペース `one-process` で動く。そこをポーリングするほかのワーカーはおらず、残りの二つの flow のワーカーは、別のプロセスと同じように試験が動かす。サーバーのプロセスは、日帰りの出張の決裁と支払いを自分のワーカーで動かし、`CheckWorkers` がそのワーカーを見つけ、サーバー自身のループが処理を行う。

## 8. 採らなかった方法

- **Temporal に答えを聞きに行く。** サーバーのプロセスが、起動したときと定期的に、答えを待っているキーを探し、その実行がどう終わったかを Temporal に聞く方法。フレームワークに、答えを待つキーの一覧が要る。結果はネームスペースのリテンションが切れる前に読まなければならない。問い合わせの実行や launch の実行なら、伝えることは一か所に残り、リトライは Temporal がする。
- **flow 自身の実行から答えを渡す。** flow のワーカーのインターセプターが、flow の終わりに `Answered` を呼ぶ方法。答えが flow の履歴の一部になり、dandori の `Replay` はインターセプター無しでリプレイする。それに、エージェントの鍵を持つプロセスとサーバーのプロセスを分けられない。
- **`Ask` の中で待つ。** `Ask` が flow を始めて終わるのを待つ方法。フレームワークの処理のリースは 1 分で、flow はそれより長くかかることがある。待っているあいだにプロセスが止まれば、答えは Temporal にしか残らない。
- **flow にタイムアウトを付ける。** ステップが遅れたことはチケットの時計が言い、遅れた答えは断られる。ここでタイムアウトを付けると、申請の種類が書いていない二つ目の期限になる。

## 9. あとで

- 答えが要らなくなった flow のキャンセル。
- フレームワークが TypeScript と Python を持ったら、それらの flow のための同じモジュール。
