# 設計

ritsu が生成する申請と承認のコードを動かす、サーバーのフレームワークの設計である。ritsu は、申請の種類（経費の精算、購買、休暇の申請など）ごとに、二つの純粋な関数 `start` と `next` を書き出し、サーバーとの約束も書き出す。約束は、API の OpenAPI の記述、申請の種類ごとの説明、関数が何を返すかを並べたベクタからなる。チケットを保管すること、時計を鳴らすこと、知らせを送ることは、わざと生成するコードの外に置いてある。その部分がこのフレームワークである。使う人は生成したパッケージを渡し、組織ごとに違う所（だれがログインするか、チケットをどこに保管するか、知らせをどう送るか など）だけを差し替える。

最初の言語は Go である。フレームワークは `http.Handler` で、PostgreSQL だけで動く。

英語版：[DESIGN.md](DESIGN.md)。

## 1. フレームワークが持つ物と、差し替える物

フレームワークは、申請の種類によらないコードを持つ。

- `openapi.json` の API（画面とエージェントのツールを除く、すべての操作）
- 申請の種類の画面（A2UI）と、同じ画面を素の HTML にしたページと、添付ファイルを HTTP で出すこと。API と同じ守りの後ろに置く（`web`、`screens`、`screens/page`。それぞれに DESIGN.md がある）
- 組織の OpenID Connect のプロバイダーでのログイン（`oidc`。別に DESIGN.md がある）
- サーバーのコマンドライン。どのサーバーにも要るフラグとその確かめ、チケットと添付ファイルの置き場、ログイン、HTTP のサーバー（`serve`）。手元のコンピューターでサーバーを試すための、本人を確かめない二つのログインもある（`devlogin`）
- 一つのイベントを決まった順で通すこと。だれか、gate、`start` か `next`、一度の保存、返事、そのあとにすること
- チケットは一度に一つずつ動くこと。保存は、読んだときのチケットの上にだけ書く
- チケットの時計。いくつのプロセスが回しても、一つの時計は一度だけ鳴る
- することの一覧（`notify`、`launch`、`ask_agent`、`ask_note`）を、キーで、済むまで行うこと
- 帳簿。保存と同じデータベースにあるときは保存のトランザクションの中で、別のデータベースにあるときはチケットのロックを取って呼ぶ
- 組織の版と、イベントに渡す組織の部分
- 一覧。その人が出せる申請の種類、その人を待っているチケット、その人の申請
- 操作の試し。操作をしたらどう答えるかを返し、何も保存しない
- 適合の試験。API を通してベクタを流す試験と、同じチケットへ同時に操作を送る試験

フレームワークは、どの申請の種類の型も知らない。API と生成した入口のあいだで約束の JSON をそのまま受け渡し、その JSON のうち、どの種類にも共通する所（チケットの状態、ステップとその承認者、することの一覧）だけを読む。フォームは、種類の説明が、人、チーム、マスタの行を選ぶ（9.2）か、ファイルを持つ（2.3）とするフィールドの値だけを読む。

差し替えるのは次の物である。

| 何を | インターフェース | 渡さないとき |
|---|---|---|
| だれがログインしているか | `Authenticator` | 必須。`oidc` は組織の OpenID Connect のプロバイダーでログインさせる。`web.ProxyHeader` は、人をログインさせるプロキシを信じる。`devlogin` は、人をページで選ばせるか、ヘッダーで受け取る。手元のコンピューターでサーバーを試すときだけ使う。`servertest.Header` はヘッダーを信じるので、試験と例だけで使う |
| チケットをどこに保管するか | `Store` | 必須。`postgres.Store`。試験には `memstore.Store` |
| だれが何をしてよいか | `Gate` | 必須。sekisho が書いたコードを包むか、`server.AllowAll` |
| 組織のデータをどこから読むか | `OrgSource` | 組織が無いので、どのイベントも `data` で断る |
| 知らせをどう送るか | `Notifier` | 知らせはログに書くだけで送らない |
| flow をどう動かすか | `Flows` | flow の開始はすることの一覧に残り、`Flows` を渡すまで繰り返し試す |
| エージェントのステップをどう動かすか | `Agents` | 同じく、問い合わせが残る |
| 帳簿 | `Books` | 帳簿を呼ぶ動きは失敗する |
| 添付ファイルをどこに保管するか | `Files` | 添付ファイルを持たない。`web` は保管せず、フォームのファイルの ID も確かめない。`web.NewDiskFiles` はディレクトリに、`(*postgres.Store).Files()` はデータベースに、`memstore.Files` はメモリに保管する |
| 監査ログをどこへ送るか | `AuditLog` | 送らない。ストアが持つ動きの記録が記録である |

`orgfiles` は、ritsu が読むのと同じ組織のファイルを読む。`servertest` には、渡された物を覚えておく通知・flow・エージェント・帳簿・ファイル・監査ログと、ページの試験に使うブラウザとページのフォームがある。`serve` は、サーバーをコマンドラインから動かす（下の「コマンドラインからサーバーを動かす」）。プロジェクトで ritsu が生成した物のうち、gate（sekisho が書いたコード）と帳簿（chobo が書いたクライアント）は、包んでつなぐ。例のサーバーは両方をつなぎ（`examples/gate.go`、`examples/books.go`）、残りは `serve` に任せている。`ritsu ringi init` が書くプロジェクトも同じ形で、gate をつなぎ、帳簿は持たない（下の「プロジェクトの最初のファイル」）。

## 2. プログラムから使う API

根のパッケージは `server`（import path は `github.com/i2y/ringi-server`）である。

### 2.1 生成したパッケージを渡す

`ritsu gen --target go --target requests` は、Go のパッケージとファイルのディレクトリを書き出す。Go のパッケージは、申請の種類の一覧 `ticketkinds.Kinds`、守っている約束の版 `ticketkinds.Contract`、入口がエラーの文を書く言語 `ticketkinds.Lang`（`en` か `ja`。`ritsu gen --lang` で渡した言語）を持つ。この三つを、ディレクトリと一緒に渡す。

```go
//go:embed requests/openapi.json requests/types requests/a2ui requests/mcp
var requests embed.FS

func pkg() (*server.Package, error) {
	dir, err := fs.Sub(requests, "requests")
	if err != nil {
		return nil, err
	}
	return server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, dir)
}
```

サーバーが読むのは `openapi.json` と `types/` である。画面（`web`）は `a2ui/` も読み、エージェント向けの道具（`mcp`）は `mcp/tools.json` を読む。読む物が無いパッケージは、その部分を作るときに受け付けない。

`Load` は一覧をそのまま受け取る。型の引数は、申請の種類の五つのフィールド（`Name`、`Version`、`Start`、`Next`、`Worked`）を持つ構造体ならどれでも受け、それぞれを `server.Kind` に変換する。後の ritsu が種類の形を変えれば、読み違えるのではなく、組めなくなる。手で書いた種類には、`server.Kind` の一覧を受ける `LoadKinds` を使う（フレームワーク自身の試験が使っている）。種類の `Worked` は、フォームが計算する物を答える（下の「フォームが計算する物」）。手で書いた種類は nil にしてよい。そのときは、`openapi.json` にそれで答える操作のあるパッケージを、`Load` が断る。

`Load` は、知らない約束の版のパッケージを受け付けない（読める版は `server.Contracts` にあり、3 である）。入口が `en` と `ja` のほかの言語で書くパッケージも受け付けない。ファイルが一覧と合わないパッケージも受け付けない。合わないのは、別の版の約束の `openapi.json` や説明、`types/<名前>.json` の無い種類、別の版を説明している説明、一覧に無い種類の操作、フレームワークが受け持たない操作、チェッカーの知らないスキーマのキーワード（下の「本文を確かめる」）である。

一つの種類が、いくつかのパッケージに、それぞれ別の版で入っていてよい（`Config.Packages`）。チケットは、出したときの版のまま終わりまで動く。新しい申請は、その日に効いている版（説明の `effective_from`）のうち、いちばん新しい版を取る。どのパッケージにも無い版のチケットは、サーバーのエラー（500）として報告し、別の版では動かさない。

パッケージは読むだけである。同じパッケージを、いくつかのサーバーが別の設定で同時に使ってよい。種類について決める値（下の「UTC オフセットと自己承認」）は、サーバーごとに、種類の自分のコピーに持つ。

### 2.2 サーバーを作る

```go
srv, err := server.New(server.Config{
	Packages:      []*server.Package{p},
	Store:         store,                              // postgres.New(ctx, pool, "")
	Authenticator: auth,                               // yours
	Gate:          gate,                               // around the code sekisho wrote
	Organization:  orgfiles.New(os.DirFS("project")),  // the directory ritsu gen was given as --root
	Notifier:      mail,                               // yours; with none, notices are logged
	Books:         books,                              // around the clients chobo wrote
})
if err != nil {
	return err
}
if err := srv.ImportOrganization(ctx); err != nil {
	return err
}
go srv.Run(ctx) // rings the timers and does the things to do
hs := &http.Server{
	Addr:              ":8080",
	Handler:           srv,
	ReadHeaderTimeout: 5 * time.Second,
	ReadTimeout:       30 * time.Second,
	WriteTimeout:      30 * time.Second,
	IdleTimeout:       2 * time.Minute,
}
return hs.ListenAndServe()
```

`New` は I/O をしない。次の設定はエラーにする。パッケージ・ストア・Authenticator・gate のどれかが無い。`Checker` である部品がエラーを返す（下の「リクエストを受ける前に止まる設定」）。同じ種類の同じ版が二つのパッケージにある。種類が二つの組織を読む（一つのサーバーが受け持つ組織は一つ）。外側の `http.Server` は使う人の物である。フレームワークは本文の大きさを制限し（下の「本文の大きさ」）、タイムアウトはサーバーの側で決める。

`srv` だけをハンドラーにするのは、ブラウザがひとりでには送らない物で人を決める Authenticator のときである。ベアラートークンや、プロキシのヘッダー（`web.ProxyHeader`）である。API は本文を `application/json` でしか受けない（ほかは 415）ので、ほかのサイトのフォームは送れない。クッキー（ログインのセッション）で人を決めるなら、代わりに `web.Handler` を前に置く（`h, err := web.New(srv, web.Config{…})` として `Handler: h`）。`web.Handler` は、ページと同じ守り（ほかのサイトから送られた物を断ることも含む）の後ろで API を出す。

`*Server` は `http.Handler` である。その下に操作の層があり、HTTP のハンドラー、画面（`web`）、ほかの入り口が、同じように呼ぶ。

```go
// Do runs one operation: who, on which ticket, what.
func (s *Server) Do(ctx context.Context, c server.Call) (server.Reply, error)

type Call struct {
	Operation string     // the operationId of openapi.json: "submit_leave", "approve", "get_ticket"…
	Person    Person     // who, as the authenticator found them
	Ticket    string     // the ticket's ID, for an operation on one
	Body      []byte     // the body, as the API takes it
	Key       string     // the operation's idempotency key (every POST has one)
	Query     url.Values // `on` of the masters; `limit`, `before` and `kind` of the lists; `q`, `limit`, `lang`, `on` of the finds
	Languages []string   // the caller's languages, the most wanted first: a find's names without `lang`
	Via       string     // the way in, for the audit log: "api" (the default), "screens", "pages", "mcp"
	Kind      string     // the administration's: the version of a kind a switch switches (13.6)
	Version   int
	Draft     string     // the draft admin_get_draft reads
	Upload    io.Reader  // the zip archive a check reads, only once the gate allows it (13.3)
}

type Reply struct {
	Outcome    Outcome       // OK, Refused, NotFound, Invalid, Limited or TooLarge
	Body       []byte        // what the API answers: a ticket, a list, a refusal
	Problem    string        // why, for NotFound, Invalid, Limited and TooLarge
	RetryAfter time.Duration // for Limited: how long to wait; 0 when waiting does not help
	NotAsked   []BookCall    // the books' calls a trial did not ask
	Problems   []OrgProblem  // for Invalid: the problems of the organization's data checked (13.3)
	Next       string        // for a part of the record as CSV: the line the next part follows (13.5)
}
```

HTTP の層がするのは、ログインしている人を確かめ、パスと本文と `Idempotency-Key` ヘッダーを読み、`Do` を呼び、返事を書くことだけである（下の「答え」）。画面のボタンもページのフォームも同じ `Call`（`operationId` と本文）になるので、ボタン、ページ、API が食い違うことはない。

```go
// Try runs an operation as Do would, and keeps nothing.
func (s *Server) Try(ctx context.Context, c server.Call) (server.Reply, error)
```

`Try` は、`Do` が今ならどう答えるか（動いたチケットか、断りとその理由）を返す。ストア、帳簿、することの一覧、知らせ、監査ログには何も残さない。`Do` と同じ物を読み、期限の来た時計をメモリの上のチケットに先に渡す。帳簿には、最後に取り消すトランザクションの中で尋ねる（ストアが `TrialStore` のとき。PostgreSQL とメモリのストアはそうである）。そのため、予算を使い切ったときのような帳簿の断りも、試しでわかる。それができないストアのときや、帳簿が別のデータベースにあるとき（下の「帳簿が別のデータベースにあるとき」）は、帳簿には尋ねず、尋ねなかった呼び出しを `Reply.NotAsked` に並べる。試しにはキーが要らない。エージェントのツール（`mcp/`）は、人が確かめる前に操作を試す。適合の試験は、試しが操作と同じように答えることを確かめる（下の「適合の試験」）。

サーバー自身のイベントは人から来ないので、gate に尋ねない。

```go
// Deliver gives a ticket an event as it is (the JSON of the contract), with no gate: for the
// server's own events and for tests. A submit names its kind and its ticket's ID.
func (s *Server) Deliver(ctx context.Context, d server.Delivery) (server.Reply, error)

type Delivery struct {
	Ticket string          // the ticket's ID; "" for a submit
	Kind   string          // the kind of request of a submit
	Event  json.RawMessage // the event, as the contract writes it
}

// Answered gives a ticket the answer of the agent's step or the note it asked for by key.
func (s *Server) Answered(ctx context.Context, key server.EffectKey, a server.Answer) (server.Reply, error)
```

`*Server` には、ほかに次の物がある。

- `ImportOrganization(ctx)` は `Config.Organization` を読んで確かめ、ソースが前に取り込んだときと違うデータを返すときに、それを最新のスナップショットとして保管する（下の「スナップショット」、13.3）。`CheckOrganization(data)` は、どこから読んだデータでも、その問題を返す（`*OrgProblems`）。`server.ReadOrgFiles(fsys, layout)` は、パッケージ `orgfiles` と同じく、ritsu が読むファイルを読む。
- `Run(ctx)` は、コンテキストが終わるまで回り、期限の来た時計を鳴らし、期限の来たすることを行う。間隔はおよそ一秒で、保存の直後にも回る。始めたときと、`Config.SweepEvery` ごとに、片づけもする。`RunTimers`、`RunToDo`、`Sweep` は、それぞれ一回分だけ回る。自分で予定を組む配置のためにある。
- `Upload(ctx, person, info, r)` は、人がアップロードするファイルを保管し、`UploadOnce(ctx, person, key, info, r)` は、一つのキーにつき一度だけ保管する（下の「差し替える物」の Files）。`Sweep(ctx)` は、期限までにどのチケットも持たなかったアップロードを手放し、期限を過ぎた操作の記録を、始めたまま答えなかった操作の記録も含めて消す（下の「行をいつまで持つか」）。
- `Replay(ctx, ticket)` は、チケットの動きを、それぞれが渡された組織のスナップショットと一緒に、順に種類の入口へもう一度渡す。ストアが持つ動きと違う答えが出たところで失敗する。
- `Import(ctx, ticket)` は、チケットをそのままの形で、最初の動きとしてストアに入れる。よそから持ってくるチケットのためにある。時計は掛けず、することも行わない。
- `Operations()` は、サーバーが受け持つ operationId の一覧を返す。
- `Current(kind, at)` は、ある時刻に新しい申請が使う種類の版を返す（上の「生成したパッケージを渡す」）。
- `Waiting(ctx, person, query)` と `Mine(ctx, person, query)` は、その人を待っているチケット（下の「その人を待っているチケット」）と、その人の申請（下の「その人の申請」）を、`list_waiting` と `list_mine` と同じように、ページに分けて返す。`CountWaiting(ctx, person)` は、その人を待っているチケットの数を種類ごとに返す（下の「その人を待っているチケット」）。`CountMine(ctx, person)` は、その人の申請の数を、種類ごと、状態ごとに返す（下の「その人の申請」）。どちらも、ページのホームが使う。
- `People(ctx)` と `Teams(ctx)` は、今日効いている組織の版の人とチームを返す（下の「スナップショット」）。
- `Zone(kind, version)` は種類が日を読む UTC オフセットを返し（下の「UTC オフセットと自己承認」）、`Now()` はサーバーの時計の時刻を返す。
- `Title(kind, version, lang)` は、ある言語を読む人に向けた種類の題と、その題の言語を返す。通知が知らせの種類を言うのに使う（下の「差し替える物」の Notifier）。
- `AdminParts(ctx, person)` は、その人が使える管理の部分を返し、`Administers(ctx, person)` は、そういう部分があるかを返す。ホームのリンクのためにある（13.4、13.7）。`MaxOrganization()` は、サーバーが受け取る、組織のデータのアップロードのいちばん大きい大きさである（13.3）。
- 画面とページが使う呼び出し（`Authenticate`、`Can` と `Replied` と `FormAt`（8.1）、`Submitting`、`Names`、`MasterRows`、`TicketFiles`、`Files`、`MaxBody`、`Packages`）は、`web` の DESIGN.md にある。

### 2.3 差し替える物

どのメソッドもコンテキストを受け、自分の失敗にはエラーを返し、多くのゴルーチンから同時に呼ばれる。フレームワークは、失敗した `Authenticate` と `Allow` を繰り返さない。することの一覧は済むまで繰り返す（下の「することの一覧」）。

**Authenticator。** リクエストを送ったのがだれかを確かめる。フレームワーク自身は、セッションもトークンもクッキーも読まない。

```go
type Authenticator interface {
	// Authenticate answers the person who sends r, or ErrNoPerson (the answer is 401). Any other
	// error is a failure of its own (500).
	Authenticate(r *http.Request) (Person, error)
}

type Person struct {
	ID    string   // their ID in the organization's data
	Roles []string // what the sign-in says of them, for the gate
}
```

別の所で確かめたベアラートークンを信じる例：

```go
type tokens struct{ verify func(token string) (id string, roles []string, err error) }

func (t tokens) Authenticate(r *http.Request) (server.Person, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return server.Person{}, server.ErrNoPerson
	}
	id, roles, err := t.verify(token)
	if err != nil {
		return server.Person{}, server.ErrNoPerson
	}
	return server.Person{ID: id, Roles: roles}, nil
}
```

**Gate。** その人がその操作をしてよいかを決める（下の「gate に尋ねる」）。フレームワークは、`start` か `next` の前に、ritsu がチケットについて決める値を添えて尋ねる。

```go
type Gate interface {
	// Allow answers whether the question's person may do its action. An error is a failure, not a no.
	Allow(ctx context.Context, q Question) (bool, error)
}
```

**Notifier。** 知らせを一つ送る。だれに、どのチケットについて、どの知らせかを受け取る。成功か失敗か分からなかったとき、また呼び出しの途中でリースが切れたとき（下の「することの一覧」）には、同じキーで二度目が呼ばれる。だから、一つのキーは一度だけ送るように作る。

```go
type Notifier interface {
	Notify(ctx context.Context, n Notice) error
}

type Notice struct {
	Key     EffectKey // the same key again is the same notice
	Kind    string    // the kind of request
	Version int       // the version of the kind the ticket keeps
	Ticket  string
	Notice  string      // waiting, reminder, went_up, returned, decided, circulation, paused, resumed, stopped, comment
	Stage   string      // the stage it is about; "" for none
	To      []Recipient // the people to tell, with their names and addresses from the organization's data
	Owner   string      // the team that owns the kind; a `stopped` notice is for its administrators
}

type Recipient struct {
	ID, Name, Email string
	Language        string // theirs in the organization's data (en or ja), else the kind's own language
}
```

知らせは、種類を名前で言う。`Server.Title(kind, version, lang)` は、`lang` を読む人に向けた種類の題を、その言語のページが見せるとおりに返す。種類の `labels` にその言語があればその題、無ければ書いたままの題である。あわせて、その題の言語を返す。`lang` は `Accept-Language` ヘッダーと同じように読み（`server.AcceptLanguages`）、ページが言語を選ぶのと同じく、挙がった言語のうち種類が題を持つ最初の言語を使う（`ja-JP` は `ja`。フランス語の題の無い種類なら、`fr, ja` は `ja`）。フレームワークは、人が読む言語を組織のデータから知り（9.1）、`Recipient.Language` で渡す。その人の言語で、無ければ種類の説明を書いた言語である。種類の題を件名に入れ、送ったキーを覚えておき、メールで送る例：

```go
type mail struct {
	srv  *server.Server // set it after server.New
	send func(to, subject, body string) error
	sent sync.Map // the keys sent; a real one keeps them in a table
}

func (m *mail) Notify(ctx context.Context, n server.Notice) error {
	if _, done := m.sent.Load(n.Key); done {
		return nil
	}
	for _, r := range n.To {
		if r.Email == "" {
			continue
		}
		title, _ := m.srv.Title(n.Kind, n.Version, r.Language) // "Expense request", or "経費の申請" for ja
		subject := fmt.Sprintf("[%s] %s: %s", title, n.Ticket, n.Notice)
		if err := m.send(r.Email, subject, "https://requests.example.com/tickets/"+n.Ticket); err != nil {
			return err // tried again later
		}
	}
	m.sent.Store(n.Key, true)
	return nil
}
```

題の言語がその人の言語と違うのは、種類にその人の言語の題が無く、書いたままの題で言うときである。HTML のメールなら、ページと同じように、その部分に言語の印を付けられる（`<span lang="ja">…</span>`）。読み上げのソフトが、その言語で読む。

**Flows と Agents。** チケットが終わったあとに始める flow（`launch`）は `Flows` に渡す。何も返ってこない。エージェントのステップ（`ask_agent`）とメモ（`ask_note`）は `Agents` に渡し、答えは同じキーで `Server.Answered` から返す。キーは実行の ID である。同じキーで二度始めても、実行は一つにする。

```go
type EffectKey struct { Ticket string; Round, Serial int64 } // JSON {"ticket","round","serial"}; String() "T/1/4"

type Launch struct {
	Key          EffectKey
	Kind, Ticket string
	Flow         string
	Args         json.RawMessage // the flow's arguments, as named values of the contract
}

type Ask struct {
	Key          EffectKey
	Kind, Ticket string
	Note         bool // true for ask_note, false for ask_agent
	Flow, Stage  string
	Args         json.RawMessage
}

type Answer struct {
	Verdict, Reason string          // an agent's step's answer
	Outputs         json.RawMessage // a note's values, as named values of the contract (a JSON array)
	Failed          bool            // the flow failed; the ticket goes on as its kind says
}

type Flows interface{ Launch(ctx context.Context, l Launch) error }
type Agents interface{ Ask(ctx context.Context, a Ask) error }
```

`Answered` は、約束が書くイベント（`agent_answered`、`note_ready`、`flow_failed`）を作る。そのイベントには、問い合わせた動きが返したチケットの版を付ける（フレームワークが問い合わせと一緒に保管している）。そのあとでチケットが動いていれば、チケットは `stale` で断る。返事は次のどれかである。

- `OK`：チケットが答えを受け取った。
- `Refused`：問い合わせたあとでチケットが先へ進んでいて、答えを断った。これは答えであって失敗ではないので、もう一度渡さない。
- `NotFound`：そのキーでは何も問い合わせていない。
- エラー：失敗（ストアの失敗など）。もう一度呼んでよい。

`Answered` は冪等である。一度答えたキーで後から呼んでも、チケットは動かず、どんな `Answer` を渡しても、一度目の返事を返す（一度目の返事を、操作の返事と同じように、キーと一緒に保管している）。答えを渡し、返事を聞く前に止まり、また動き出した実行も、答えを渡すのは一度になる。

dandori が Temporal 向けに書く flow は、別のモジュール（このリポジトリの `temporal/`）が動かす。フレームワークのモジュールは、どの SDK にも依存しない。

**Books。** 帳簿の仮押さえ・確定・取消（約束の `hold`、`post`、`release`）は、chobo が帳簿ごとに型を付けて書くクライアントの呼び出しである。`Books` は、約束の呼び出し一つを、そのクライアントへつなぐ。

```go
type Books interface {
	// Call makes one call. tx is the store's transaction (a pgx.Tx for the PostgreSQL store) when
	// the books share the store's database, and nil when they do not (Config.BooksApart).
	Call(ctx context.Context, tx any, c BookCall) (BookAnswer, error)
}

type BookCall struct {
	Key                  EffectKey
	Kind, Ticket         string
	Call                 string // hold, post or release
	Book, Transfer       string
	Args                 []NamedValue // c.Arg("amount") finds one; Str, Num and Truth read it
}
```

`BookAnswer.Outcome` は `done`、`done_before`、`refused` のどれか（chobo の結果）である。帳簿が拒否すると、イベントを `book` で断り、チケットは元のままになる。`Reason` は断りの `text` に、`ReasonJa` は `text_ja` になる（空なら `Reason` をそのまま使う）。帳簿は、申請と試み（呼び出しの引数にある `attempt`。ringi の DESIGN 12.4）を仮押さえのキーにする。帳簿が別のデータベースにあるときは、`Config.BooksApart` を設定する（下の「帳簿が別のデータベースにあるとき」）。このときサーバーは、保存した動きが持たない仮押さえを、その仮押さえと同じ引数（キーも含む）の `release` で取り消す。

**Files。** 添付ファイルは、フォームの中ではファイルの ID である（型が `attachment` のフィールド。明細の行の中も）。ファイルそのものは、だれがいつアップロードしたかと一緒に `Files` が保管する。`Put` はファイルをその記録（`FileInfo`）と一緒に受け取り、`Stat` は記録を、`Open` は中身を返し、`URL` はブラウザに渡すストア自身のアドレスを返し（サーバーが中身を送るときは `""`）、`Delete` は消す。人がファイルをアップロードするのは `Server.Upload` で、ファイルを `Files` に入れ、その人のアップロード（まだどのチケットも持たない物）としてストアに記録する。`Config.Files` があるとき、`Do` は `submit`・`resubmit`・`edit` が指せるファイルを、チケットがもう持っている物（フォームか履歴のイベントが指した物）と、送った本人がアップロードした物に限る。ほかの ID は、どの入り口からでも 400（`form.receipt: no such file`）である。アップロードを指した動きは、それからそのアップロードを持つ（下の「一つの操作」）。アップロードとダウンロード、だれが何をダウンロードできるかは `web` が受け持つ（`web` の DESIGN.md の 6 章）。

どのチケットも持たないアップロードは、`Config.UploadsKept` のあいだ残す（無ければ 7 日。負の長さなら、チケットが持つまで）。そのあと `Sweep` が手放し、ファイルを消す。`Run` は、始めたときと `Config.SweepEvery` ごと（無ければ 1 時間）に片づける。ストアは、アップロードを手放すのも、動きがアップロードを持つのも、それぞれアップロードの行に対する一つのトランザクションで行うので、二つが両方起きることは無い。手放したアップロードを指す動きは保存せず、無いファイルと同じく答え（400、`form.receipt: no such file`）、動きが持ったアップロードは手放さない。手放したアップロードは、手放した物として記録に残すので、あとで動きが持つことも無い。アップロードの記録の無いファイル（ほかの方法で `Files` に入れた物や、ストアがアップロードを記録する前からある物）は手放さない。一人が持てる、どのチケットも持たないアップロードは `Config.UploadsPerPerson` まで（無ければ 50。負の数なら制限しない）で、`Upload` はその次を `*TooManyUploads` で断り、何も残さない。`web` はこれを `Retry-After` の無い 429 で答える。数は、申請がアップロードを持つか、サーバーが手放すまで減らず、送り直せる時を言えないからである。

`Server.UploadOnce` は、その人の一つのキー（操作のキーと同じ文字で書く）につき、ファイルを一度だけ保管する。そのキーで上げたアップロードが残っているあいだは、チケットが持っていても、いなくても、同じキーの次のファイルを読まず、数にも入れず、残っている物の ID を返す。二つが同時に来れば、ストアはその一方だけを記録し、サーバーがもう一方のファイルを消し、どちらにも記録した物の ID を返す。手放したアップロードは、もうキーの物ではない。キーは、呼ぶ側がファイルから作る。ページは、フォームを送ったページとファイルからキーを作る（`web` の DESIGN の 9.3）ので、ファイルのあるフォームを二度送っても、同じ一つのファイルを指し、一つの操作になる。

**OrgSource。** 組織のデータを読む。版（チーム、人、所属）、代理、マスタの行である。フレームワークは、何を読むか（ritsu のファイルが書くデータの場所、マスタとそのキーとフィールド）を伝え、読んだ物を一つのスナップショットとして保管する（下の「組織」）。

```go
type OrgSource interface {
	Read(ctx context.Context, want OrgLayout) (*OrgData, error)
}
```

`orgfiles.New(fsys)` は、ritsu が読むのと同じファイルを読む。版ごとに、効き始める日を名前にしたディレクトリがあり、`teams.csv`・`people.csv`・`members.csv` か、`organization.json` 一つが入っている。版のディレクトリの横に `proxies.csv` があり、マスタはそれぞれ CSV である。人事のシステムから読むソースも、同じ `OrgData` を返せばよい。

**AuditLog。** チケットを動かしたイベントと、保管した断りのそれぞれについて、記録を一つ受け取る（`AuditEntry`：だれが、いつ、どの操作で、どのチケットを、動かしたか断られたか、イベント、どこから入ったか（`Via`））。記録はすることの一覧を通して渡すので、プロセスが止まっても失われない。二度渡す記録は同じ `ID` を持つ。何が起きたかの記録はストアの動きであり、監査ログはそのコピーを送る先である。

**Clock**（`Now() time.Time`）、**NewTicketID** と **NewAttempt**（`func() string`）は試験のためにある。NewAttempt は、申請と出し直しの試みに名前を付ける（下の「一つのイベントの順」「帳簿が別のデータベースにあるとき」）。既定の ID と名前は 26 文字で、ミリ秒の単位まで作った順に並ぶ（ミリ秒の時刻と乱数を文字にした物）。

### 2.4 一緒に付いてくる物

- `postgres`：PostgreSQL（pgx v5）のストア。表と、表を上げる手順を持つ（下の「ストア」）。
- `memstore`：メモリのストアと添付ファイルの置き場（`Files`）。試験と、フレームワークを試すために使う。
- `orgfiles`：組織のファイルを読むソース。
- `servertest`：時刻を設定できる時計、ヘッダーを信じる Authenticator、渡された物を覚えておく通知・flow・エージェント・帳簿・ファイル・監査ログ。プロジェクトのページの試験に使う、スクリプトの無いブラウザとページのフォームもある。
- `screens`：ritsu が生成した画面（A2UI）にサーバーのデータを入れる。ボタンごとの操作も作る。`screens/page`：同じ画面を素の HTML のページにし、ページから送られたフォームを読む。
- `web`：画面とページと添付ファイルを、API の前で HTTP で出す。ブラウザに要る守りも持つ。ディスクにファイルを保管するストアと、人をログインさせるプロキシの後ろに置くサーバー向けの Authenticator もある。
- `oidc`：組織の OpenID Connect のプロバイダーでのログイン。`oidc/oidctest` は試験のためのプロバイダー。
- `devlogin`：手元のコンピューターでサーバーを試すための、本人を確かめない二つのログイン。人に使ってもらうサーバーでは動かないようにする確かめも持つ。
- `serve`：サーバーのコマンドライン。どのサーバーにも要るフラグとその確かめ、フラグが言う保管先と添付ファイルの置き場、ログイン、ページを前に置いた HTTP のサーバー（下の「コマンドラインからサーバーを動かす」。`devlogin` と `servertest` のブラウザとフォームも、そこに書く）。
- `conformance`：適合の試験（下の「適合の試験」）。どのストアにもかけられる。
- `internal/jatext`：フレームワークの日本語の文（ページ、MCP のツール、サーバーが書く断り、ログインのページ）が名前を置く、一つの決まり。ASCII で終わるか始まる名前は、仮名や漢字との間を空白で区切り、全角の約物には空白を置かずに付ける（`Leave request の履歴`、`Leave request：あなたの申請`）。プロジェクトのコードが import する物ではない。
- `mcp/`：別のモジュール。申請をツールとして出す Model Context Protocol のサーバー。
- `temporal/`：別のモジュール。flow とエージェントのステップを Temporal の上で動かす。
- `examples/`：ritsu の五つの例を動かす、別のモジュール（README も見てほしい）。

フレームワークのモジュールが依存するのは、標準ライブラリと pgx v5 である。例のモジュールは、sekisho が書いた gate が import する cedar-go を足す。`mcp/` は Model Context Protocol の Go の SDK を、`temporal/` は Temporal の Go の SDK を足す。

### 2.5 リクエストを受ける前に止まる設定

誤った設定は、リクエストのたびに失敗させるのでなく、リクエストを受ける前に、何が違うかを言ってサーバーを止める。どの部品も、作るときに自分の設定を確かめる。

| どこで | 断る物 |
|---|---|
| `Load` | フレームワークが読まない約束の版のパッケージ（読む版と、その版を書く ritsu で生成し直すか、その版を読むフレームワークを使うことを言う）。入口が `en` と `ja` のほかの言語で書くパッケージ。パッケージの種類と違う版の `openapi.json` や `types/<name>.json`（Go のパッケージと requests のディレクトリを、`ritsu gen` の別々の回が書いた）。`utc_offset_minutes` か `self_approval` の無い `types/<name>.json`、一日以上のオフセット（下の「UTC オフセットと自己承認」） |
| `New`（I/O なし） | 要る部品が無い。`Checker` である部品がエラーを返す（`web.ProxyHeader`：秘密が無い、32 文字より短い、ヘッダーがそのままでは運ばない文字がある、二つのヘッダーの名前が同じ） |
| `postgres.New` | `postgres.ReachWithin`（10 秒）のうちに届かないデータベース、コードより新しい表 |
| `oidc.New`、何かに届く前なら `oidc.Config.Check` | https でない issuer やリダイレクト URL。クライアントの ID か秘密が無い、または端に空白や改行がある。セッションの鍵が無い、または 32 バイトの乱数でない（文字列、同じバイトの繰り返し、同じ鍵が二度）。ディスカバリーの文書や鍵が読めない、または要ることを言わないプロバイダー |
| `web.New`、`mcp.New`、`(*mcp.Tools).Handler` | それぞれの設定（それぞれの DESIGN.md）。`web.New` は、画面が一つのカタログの ID を別のファイルに付ける二つのパッケージも断る（`web` の DESIGN.md の 2.3） |
| `ImportOrganization` | 読めない組織のデータと、確かめ（13.3）を通らない組織のデータ。問題は先頭の 100 件をそれぞれ一行で、ファイルと行を付けて言い、残りの数と、1,000 件で確かめを止めたときはそのことも言い、`ritsu ringi check` でデータを確かめるように言う。例のコマンドは、一行ずつログの記録にする（`server.LogStop`） |
| `serve` の `Stores.Serve`（何かを作ったり何かに届いたりする前） | プロジェクトが足したルート（`Stores.Handle`、下の「コマンドラインからサーバーを動かす」）のうち、パターンが空のもの、`http.ServeMux` のパターンとして読めないもの、フレームワークのパスを取るもの、先に足したルートとぶつかるもの。ハンドラーの無いルート。ハンドラーを作れないとき、作ったハンドラーが nil のときは、何かを出す前に止まる |

パッケージ `serve` で動くサーバーのコマンド（例のサーバーもその一つ。下の「コマンドラインからサーバーを動かす」）は、何かを読んだり何かに届いたりする前に、フラグと環境を確かめる（`serve.Flags.Check`）。止まるのは、`-db` と `-files` を一緒に渡したとき（`-db` では添付ファイルはデータベースに入る。PostgreSQL と別の所に置くサーバーは、Go で作って `Config.Files` を渡す。上の「差し替える物」）、二つのログインを同時に選んだとき、プロバイダーの設定が一部だけのとき（足りない物の名前を言う）、本人を確かめないログイン（`X-Person` ヘッダー、`-dev-login`）を、ほかのコンピューターから届くアドレスで使うとき、環境にプロバイダーの設定（`RINGI_OIDC_CLIENT_SECRET`、`RINGI_SESSION_KEYS`）があるのに使うとき、証明書（`-tls-cert`）と一緒に使うときである。これらは人に使ってもらうサーバーの設定で、そこでフラグを一つ書き忘れると、だれでもだれにでもなれてしまう。この二つのログインは、プロキシを通ったリクエスト（`Forwarded`、`X-Forwarded-For` など、プロキシが足すヘッダーを持つ物）にも人を返さない。

`Serve` はそのあと、サーバーを作ったり何かに届いたりする前に、プロジェクトがサーバーに足すルート（`Stores.Handle`、下の「コマンドラインからサーバーを動かす」）を確かめる。パターンが空のものや `http.ServeMux` のパターンとして読めないもの、ぶつかり合う二つのルートは、足すときに `http.ServeMux` を panic させる。フレームワークのパス（`/`、ページ、画面、添付ファイル、API、ログイン）を取るルートは、ページや操作をプロジェクトのルートの後ろに隠してしまう。

### 2.6 コマンドラインからサーバーを動かす

どのプロジェクトのサーバーにも、プロジェクトが渡す部品のまわりで同じことをするコマンドがある。アドレス、チケットと添付ファイルの置き場、ログインの方法、証明書をフラグで受け取り、何かを読んだり何かに届いたりする前に確かめる。保管先と添付ファイルの置き場を開き、ログインを作り、止めるように言われるまで、API の前にページを置いて出す。パッケージ `serve` は、このコマンドから、プロジェクトの部品を除いた物である。

```go
var f serve.Flags
f.Register(flag.CommandLine) // -addr, -db, -files, -dev-login, the -oidc-… flags, -session-keys, -tls-cert, -tls-key
flag.Parse()
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
st, err := serve.Open(ctx, f, log) // Check first, then the store and the files
if err != nil {
	return err
}
defer st.Close()
return st.Serve(ctx, server.Config{
	Packages: []*server.Package{p}, Gate: gate, Organization: org, Notifier: notifier, Agents: agents, Books: books,
}, func(srv *server.Server) { agents.Server = srv })
```

- `Flags.Check` は、上の「リクエストを受ける前に止まる設定」に書いた設定で、何が違うかを言って止まる。
- `Open` は、`-db` があれば PostgreSQL を開く。スキーマは `ringi` で、添付ファイルと、プロバイダーでのログアウトもそこに入る。`Stores.Pool` は同じデータベースで、プロジェクトがほかにそこへ置く物（例では帳簿）に使う。`-db` が無ければ、チケットはメモリに、添付ファイルは `-files` のディレクトリか、メモリに入る。`-files` は `-db` の無いサーバーのための物で、`Check` は二つを一緒には受けない。
- `Serve` は、フラグが言う保管先、添付ファイルの置き場、ログインでサーバーを作る。この三つのどれかを自分で決めた `Config` は断る。何かを出す前に、サーバーを `made` に渡す。サーバーを呼び返す部品（例ではエージェント）のためである。そのあと、組織を読み込み（確かめを通らないデータなら止まる。上の「リクエストを受ける前に止まる設定」）、ページ（`web`）を前に置いてログインを同じ守りの後ろに付け、時計とすることの一覧を回し、`ctx` が終わるまで HTTP（`-tls-cert` があれば https）で出す。HTTP のサーバーは、リクエストのヘッダーを 5 秒、リクエストを 30 秒のうちに読み、答えを 30 秒のうちに書き、使われていない接続を 2 分残し、ヘッダーは 64 KiB までにする。止めるときは 10 秒まで待ち、まだ答えている物はそこで切る。
- `Stores.Handle` は、`Serve` が作るサーバーに、プロジェクトのルートを足す。たとえば `mcp/` のエージェントのツールを `/mcp` に置く。`handler` は、サーバーが `made` に渡されたあと、何かを出す前に、そのサーバーからルートのハンドラーを作る。ルートはページや API と同じ守りの後ろに置かれ（同じヘッダーが付き、ほかのサイトからの POST は断られる）、人にだけ答える。フラグが言うログインで人が見つからないリクエストには、ハンドラーを呼ばずに、API と同じく 401 を返す。ハンドラーは `serve.Person` で人を読む。その人としてサーバーにさせること（`Server.Do`）は gate に尋ねられる。ルートはフレームワークのパスを取れない。`/`、`/pages`、`/a2ui`、`/files`、API の `/requests`、`/waiting`、`/mine`、`/tickets`、`/organization`、`/masters`、`/find`、`/admin`、`/login`、`/logout`、プロバイダーのコールバックである。そういうルート、空のパターンや読めないパターン、ぶつかり合う二つのルート、ハンドラーの無いルートがあると、`Serve` は何かを作ったり何かに届いたりする前に止まる（上の「リクエストを受ける前に止まる設定」）。
- `Flags.ProviderClient` は、サーバーがプロバイダーに届くときに使うクライアントである。ディスカバリーの文書、鍵、トークンを、これで取りに行く。フラグではなく、プロジェクトのコマンドが Go で設定するフィールドで、プロキシを通して届くプロバイダーや、組織の認証局が署名した証明書を使うプロバイダーのためにある。nil なら、10 秒で時間切れになるクライアントを使う。

エージェントのツールを `/mcp` に置くには、次のようにする。

```go
st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
	tools, err := mcp.New(srv, mcp.Config{Logger: log})
	if err != nil {
		return nil, err
	}
	return tools.Handler(mcp.HTTP{Person: serve.Person}) // the person the sign-in found
})
```

エージェントは、サーバーのほかのクライアントと同じ方法でログインする。手元のコンピューターでは `X-Person` ヘッダーか `-dev-login` のクッキーを送り、プロバイダーでは、ブラウザでログインしたセッションのクッキーを送る。ベアラートークンでツールを出す形（`mcp.HTTP.Verify`）は、プロジェクトが自分で出すサーバーのための物である。`Handle` で足したルートは、先にログインの人を求めるからである。

`devlogin` は、手元のコンピューターでサーバーを試すための、本人を確かめない二つのログインを持つ。組織の人を並べるページ（`SignIn`。選ぶ部品が並べる数より人が多ければ、名前の一部か ID で探す）と、API を試すための `X-Person` ヘッダー（`Header`）である。どちらも、ループバックのアドレスに送られ、プロキシを通っていないリクエストにだけ人を返す（`Local`）。`OnLoopback` は、ほかのアドレスでこれらを使って待ち受けるのを断り、`NotWithProvider` は、環境にプロバイダーの設定があるときに断る。`serve` は、`-dev-login` と、ほかのログインを選ばないときのヘッダーに、これらを使う。自分のフラグを持つ `mcp/` の例のコマンドも、同じ確かめを使っている。

`(*OrgVersion).TeamOf(person, kind)` は、その人の、ある種類のチームを返す。主な所属のチームから上へたどって最初に当たるその種類のチームで、無ければ主な所属のチームである。人の部署を読む gate が使う（下の「gate に尋ねる」）。

`servertest.Browser` と `servertest.Form` は、プロジェクトのページの試験に使う。ブラウザは、スクリプトの無いブラウザがサーバー自身のページから送る物を送り、答えで受け取ったクッキーを覚え、リダイレクトはたどらない。フォームは、ブラウザが持つのと同じページのフォームで、値を入れ（`Set`、`Tick`、`Attach`）、ボタンの一つでページと同じ形で送る（`Press`。url-encoded か multipart）。

これらをフレームワークに置くのは、どのプロジェクトのサーバーにも要るからである。`examples/` にあったときは、使う人は写して直すしかなく、写した物はフレームワークが変わっても変わらなかった。プロジェクトのコードに残るのは、プロジェクトごとに違う所である。gate への尋ね方はどのプロジェクトでも同じで、sekisho がどの gate にも書く入口（`Authorize`）に、action の名前で尋ねる。違うのは、フレームワークの値を gate の型に写す所（gate のストア）で、gate とその型はプロジェクトごとに生成されるので、gate ごとに違う（`examples/gate.go`）。chobo が書いたクライアントを包む帳簿のつなぎも同じである（`examples/books.go`）。例のサーバーのコマンド（`examples/cmd/server`）が持つのは、それらと、パッケージ、組織、通知、flow、エージェント、フラグ `-verdict` だけである。

### 2.7 プロジェクトの最初のファイル

`ritsu ringi init <ディレクトリ> --module <パス> [--mcp] [--temporal]` は、フレームワークで動くプロジェクトを書く。`project/` の規則（申請の種類、組織、gate、暦）、`ritsu gen` がそこから作る物（`go/` と `requests/`。`go generate` が書き直す）、それをフレームワークに渡す Go のコードである。このコマンドはフレームワークではなく ritsu の物である。書くコードは ritsu が生成した物を呼び、生成する物は ritsu とともに変わるからである。Go で書くのは、プロジェクトごとに違う部分（上の「コマンドラインからサーバーを動かす」）だけである。

- `<パッケージ>.go`：`Package` は、プログラムに埋め込んだファイルから生成したパッケージを読む。`Organization` は、同じく埋め込んだ `project/org` を読む。`Parts` は、この二つと gate と Notifier を入れた `server.Config` を返す。`<パッケージ>.go` がほかのファイルの名前になるとき（モジュール `…/gate`）や、Go が一つのシステムでだけビルドする名前になるとき（`…/approvals-windows`）は、`project.go` にする。
- `gate.go`：sekisho が書いた gate に、入口の `Authorize`（8 章）で、action の名前と、チケット、ステップ、申請の種類の三つの ID を渡して尋ねる。gate は、その action が取る型の ID を使う。gate の型は、ストアが問いから埋める（本人の部署は `TeamOf` で求める）。gate に無い action は誤りで、ほかの断りは「許さない」である。
- `notify.go`：通知を一つずつログに書く Notifier。
- `cmd/server/main.go`：`serve.Flags` でフラグを読み、`serve.Open` で保管先を開き、`Parts` を `Stores.Serve` に渡す。
- 試験：`TestVectors`（12 章。`memstore` で）。`TestGate` は gate だけが決めること（だれがチケットを見られるか、だれが取り下げられるか）を確かめる。ベクタは `server.AllowAll` で流すからである。`TestPages` は、`devlogin.SignIn`、`servertest.Browser`、`servertest.Form` を使って、ページで申請し承認する。

二つのオプションは、プロジェクトに無くても動く物を、それが要るフレームワークのモジュールと一緒に足す。

- `--mcp`：`tools.go` が、モジュール `mcp` のエージェントのツールを、サーバーの `/mcp` に置く。`Stores.Handle`（上の「コマンドラインからサーバーを動かす」）と `mcp.HTTP{Person: serve.Person}` を使うので、エージェントはブラウザと同じ仕方でログインし、することは gate に尋ねられる。`TestTools` は、`cmd/server` と同じくプロジェクトを動かし、MCP の SDK のクライアントから、ツールで申請を出して読む。だれもログインしていないリクエストには 401 が返る。
- `--temporal`：二つ目の申請の種類 `expense.ringi` を書く。承認されると flow `pay`（`project/shared/pay.flow`）を `launch` する。`flows.go` の `Flows` は、種類の説明が挙げる flow から（`FlowsOf`）、モジュール `temporal` の Runner を作る。`Workers` は、プロジェクトが書くタスク（`payments.go`。ログに書くだけ）で、flow のワーカーを作る。ワーカーを動かすのは `cmd/worker` である。`cmd/server` は Temporal に接続し（`-temporal`）、Runner を `Flows` と `Agents` に渡して、サーバーの横で動かし（`Run`）、30 秒のうちに `CheckWorkers` が全部のキューのワーカーを見つけなければ、ログに警告を書く。`TestFlows` は、Temporal の開発用サーバーの上で、精算を承認から支払いまで動かす。Temporal の CLI が無ければ、モジュール `temporal` の試験と同じく、理由を言って飛ばす。

プロジェクトの `go.mod` は、それを書いた ritsu が言うフレームワークの版を求める。モジュール `mcp` と `temporal` も同じ版である。ritsu のどの版も、フレームワークの一つの版に合わせて作り、その版を書く。プロジェクトが求めるのはこの三つのモジュールで、例のモジュールは公開しない。三つのどれも、例のモジュールを求めない。プロジェクトで `go mod tidy` を回すと、読み込んだパッケージの試験も読まれるので、例を動かす `mcp` の試験は例のモジュールに置く。`scaffold/` の試験は、PATH の `ritsu`（または `RITSU` が指す物）で、両方の言語とオプションの組（無し、`--mcp`、`--temporal`、両方）ごとにプロジェクトを書き、`replace` でこのリポジトリのモジュール（三つのうち、オプションが要る物）に向け、ネットワークなしで `go mod tidy`、`go generate`（何も変えてはいけない）、`go vet`、プロジェクトの試験を回す。`init` が書くコードを、ここにあるフレームワークに照らして確かめる。ritsu が無ければ、そう言って飛ばす。`TestPublishedModules` は、ritsu なしで三つのモジュールの `go.mod` を読み、どれも三つのほかにこのリポジトリのモジュールを求めず、置き換えもしないことを確かめる。

### 2.8 版を公開する

三つのモジュールは一つの版で公開し、タグを三つ付ける（`vX.Y.Z`、`mcp/vX.Y.Z`、`temporal/vX.Y.Z`）。このリポジトリでは、`mcp` と `temporal` はフレームワークを `v0.0.0` で求め、`../` に置き換えている。フレームワークを求めず `mcp` か `temporal` を求めるプロジェクトには、この二つの go.mod が求める版のフレームワークが選ばれ、`v0.0.0` は取れない。そこで、タグの前にこの require をその版に上げる。replace は残す。go コマンドが読む replace はメインモジュールの物だけで、`mcp` と `temporal` には `go install` で入れるコマンドも無い（`go install` は replace のあるモジュールを断る）ので、replace が効くのはこのリポジトリでの作業だけだからである。モジュールの zip に入るのは、そのディレクトリのファイルと、リポジトリの根の `LICENSE` という名前のファイルだけである。このリポジトリのライセンスは `LICENSE-APACHE` と `LICENSE-MIT` なので、`mcp/` と `temporal/` にその写しを置く（ファイルで置く。リンクは zip に入らない）。`TestLicensesOfTheModules` が、根のファイルと一字一句同じかを確かめる。タグの前には、プロジェクトが取るのと同じ形で確かめる。タグを付けるコミットからファイルのプロキシを作り、ネットワークも replace も使わずに、`mcp` だけを求めるモジュールと `temporal` だけを求めるモジュールを tidy して組み、vet する。`ritsu ringi init` が書くプロジェクト（`--mcp` と、`--mcp --temporal`）は、tidy、generate、vet、試験まで回す。三つのモジュールの zip のそれぞれに、二つのライセンスが入っていることも見る。このリポジトリのままで作ると、require が `v0.0.0` なので `mcp` だけ・`temporal` だけを求めるモジュールは tidy できず、上げるとどの確かめも通る。

## 3. 一つのイベントの順

人の操作（`Do`）は、次の順で進む。

1. **読む。** `operationId` で操作を引く。キーの無い POST と、キーが 1〜255 バイトの表示できる ASCII でない POST は Invalid である（下の「答え」）。本文を操作のスキーマに照らし（下の「本文を確かめる」）、読めない本文は Invalid にする。
2. **時計を先に。** チケットの時計に期限の来た物があれば、先に `next` に渡す。それぞれが一つのイベントで、保存も一回ずつである（下の「時計」）。
3. **キー、試み、チケット、組織。** その人が前に使ったキーなら、一度目の答えを返し、何も変えない。前に別の操作か別の本文で使ったキーなら Invalid である。申請と出し直しは試みである（ringi の DESIGN 7.1）。サーバーは試みに名前を付け（`Config.NewAttempt`）、帳簿に何かを尋ねる前に、その名前を、キー、本文のダイジェスト、申請なら新しいチケットの ID と一緒に保管する（`Store.Begin`）。返事を保管する前に同じキーで送り直された操作は、同じ名前のまま、同じ試みとして進む。チケットを番号と一緒に読む。番号は保存のたびに一つ上がる。その人がチケットを見られなければ 404 を返す（下の「チケットを見られる人」）。組織は、イベントの日（種類のオフセットで読む）に効いている版を使う（下の「イベントに渡す組織の部分」）。その日に効いている版が無ければ、フレームワーク自身がイベントを `data` で断る。
4. **gate。** 本文と人からイベントを作る（`by` はその人である。ほかの人の代わりに出す申請は、`requester` にその人を書く。申請と出し直しは、API の本文には無い試みの名前 `attempt` を持つ）。gate には下の「gate に尋ねる」の値を添えて尋ねる。gate が許さなければ、`person` の断りを返す。
5. **`start` か `next`。** 種類の入口を、チケット、イベント、その二つに要る組織の部分（下の「イベントに渡す組織の部分」）、時刻を渡して呼ぶ。断りは 409 で返し、キーと一緒に保管する。チケットは変わらない。
6. **保存。** 動きを一つのトランザクションで保存する（下の「一つの操作」）。番号が読んだときのままならチケット、`next` が返したすることを含む動き、掛けた時計と外した時計と鳴った時計、保存のあとにすること、チケットが今だれをいつから待っているか、チケットが名前を挙げる人、キーと返事（`Begin` がそのキーに保管した物と置き換える）、帳簿が同じデータベースにあれば帳簿の呼び出し、である。帳簿が拒否すれば何も保存せず、イベントを `book` で断る。番号が変わっていたか、あいだにキーが使われていたら、3 に戻る。`next` は渡された物だけから答えるので、やり直しても問題はない。一つのイベントは 20 回まで試し、新しい ID がすでに使われていた申請には別の ID を付ける。
7. **返事。** 保存が済んだらすぐに返事を送る。知らせと flow は返事を待たせない。
8. **そのあと。** `Run` が起き、保存した物から、することをキーで行う（下の「することの一覧」）。保存の前に外へ出る物は無い。

サーバー自身のイベント（鳴った時計、エージェントの答え、`Deliver`）も、gate のほかは同じ順で進む。時計と答えには人がいない。

### 3.1 帳簿がストアと同じデータベースにあるとき

帳簿の呼び出しは、保存のトランザクションの中で、することの一覧に並んだ順に行う。拒否か失敗があれば、保存の全体を巻き戻す。これが既定で、あとで直す物が残らない。仮押さえが残るのは、その動きが残るときだけである。

### 3.2 帳簿が別のデータベースにあるとき

帳簿が専用のデータベースにあるとき（`Config.BooksApart`）は、一つのトランザクションで両方を囲めない。そこでフレームワークは次のようにする。

- **チケットのロック。** チケットを読む前にロック（`Store.Lock`）を取り、イベントに答えるまで、やり直しのあいだも持ち続ける。一つのチケットへの操作は重ならない。
- **仮押さえと確定は保存の前に。** それぞれ帳簿の側のトランザクションで行う。拒否されたら、イベントを `book` で断り、何も保存しない。
- **保存に失敗したら、何も取り消さずにやり直す。** 同じロックを持ったまま、同じ試みのまま（`attempt` は変えない）、チケット、組織、gate、`next` を読み直してやり直す。時刻は受け取ったときのままである。同じ呼び出しを持つ答えは、帳簿で `done_before` になり、その保存が呼び出しを持つ。20 回やり直しても保存できなければ失敗（500）である。
- **取消は保存のあとに。** `release` は、その動きのすることの一つになり、保存のあとで行う。
- **保存した動きが持たない仮押さえは取り消す。** やり直しが済んだら、帳簿が受けた仮押さえのうち、保存した動きが持たない物を、その仮押さえ自身のキーで取り消す。やり直した答えが変わったとき（gate か入口が断った、別の仮押さえを答えた）と、どの試みも保存できなかったときである。帳簿は、申請と試みを仮押さえのキーにする（上の「差し替える物」の Books）ので、取り消しはその仮押さえにだけ当たり、ほかの試みの仮押さえには当たらない。

どの試みも保存できなかったときは、取り消す前に、操作のキーに新しい試みの名前を付ける（`Store.Renew`）。同じキーで送り直された操作は、新しい試みとして、帳簿に一から確かめられる。取り消した試みのままだと、帳簿は `done_before` を返し、何も押さえない。新しい名前を保管できなければ、仮押さえは取り消さずにログに書いて残し、同じキーで送り直された操作は、その仮押さえを持つ試みとして進む。キーで新しい試みにできないイベントの仮押さえも、同じく残す。`Deliver` で渡すイベントは試みの名前を自分で持ち、時計やエージェントの答えは、もう一度来ても同じ試みなので、帳簿はその仮押さえに `done_before` を返す。帳簿が答えなかった取り消しは、することとして保管し（`Store.KeepToDo`。ID は、`void/` のあとに、仮押さえの `EffectKey` と試みの名前を `/` でつないだ物）、`Run` が動きの `release` と同じように行う（下の「することの一覧」）。それも保管できなければ、ログに書く。帳簿が断った取り消しは、やり直しても変わらないので、ログに書く。保存した動きが持たない確定は、ログに書いてそのまま残す。確定は取り消さず、同じ操作を同じキーで送り直せば、その確定に `done_before` で当たる。

帳簿が呼び出しを受けたあと、保存する前にサーバーが止まったときは、その呼び出しを、同じイベントが送り直されたときに任せる。それは同じ試みなので、帳簿はその呼び出しに `done_before` を返し、その保存が呼び出しを持つ。イベントがもう来ない仮押さえは、期限切れに任せる（chobo の仮押さえには期限がある。帳簿を別にする配置では、帳簿の `Expire` を定期の仕事から呼ぶ）。

帳簿は拒否も、キーごとに覚えている。帳簿がストアと同じデータベースにあれば、拒否は動きのほかの物と一緒に巻き戻る。別なら、拒否は帳簿に残る。どちらでも、値を変えた出し直しも、予算が増えたあとに同じ値で出し直すのも、自分のキーを持つ別の操作である。新しい試みになり、その仮押さえも別のキーを持つので、帳簿は一から確かめる。

## 4. ストア

### 4.1 一つの操作

ストアは `Store` インターフェースである。ストアが正しく作らなければならないのは、一つの操作 `Save` だけである。`Save` は動きを受け取り、次の物を一つのトランザクションで、全部か何もなしかで書く。

- チケット。番号が、動きが読んだときの番号のままのときだけ書き（新しいチケットなら、その ID のチケットがまだ無いときだけ）、番号を一つ上げる。動きそのもの（イベント、時刻、人、キー、`next` が返したすること、組織のスナップショット）を、そのチケットの前の動きのあとに足す。チケットの種類と版は、最初の動きの物のままにする。
- 鳴った時計を、同じ時刻のまま残っていれば外し、そのあとで、動きが外す時計と掛ける時計を書く。
- することを、ID ごとに一度だけ、まだ試していない物として書く。
- チケットが今だれを待っているかを、前の物と置き換えて書く。いつから待っているか（`Move.Since`：待っているステップが始まった時刻か、チケットが申請者に戻った時刻）も、チケットと一緒に書く（`TicketRecord.Since`。だれも待っていなければ無し）。`Waiting` はこの時刻で並べる。チケットを動かさない動きには、チケットが持っていた時刻を渡す（下の「その人を待っているチケット」）。
- チケットが名前を挙げる人と、そのうちだれの申請か（`Move.People`、`Move.Own`）を、前の動きが挙げた人に足して書く。消すことはない（下の「チケットを見られる人」「その人の申請」）。
- 操作のキーと返事を、その人のそのキーにまだ返事が無いときだけ、`Begin` がそのキーに保管した物と置き換えて書く（上の「一つのイベントの順」の 3）。
- 動きのイベントが指すアップロードのうち、チケットがまだ持っていなかった物を、持った物として記録する。それからは手放さない（上の「差し替える物」の Files）。
- 動きの `Books` をトランザクションと一緒に呼んで、帳簿を呼ぶ。

番号が読んだときと違えば `ErrConflict` を、そのキーにもう返事があれば `ErrKeyTaken` を、アップロードのどれかが手放されていれば `*UploadGone` を、帳簿が拒否すれば `Books` が返したエラーを返す。チケットの無い動きは、断った操作のキーと返事、その監査の記録、断られた時計を外すことだけを保管する。

インターフェースのほかのメソッドは、`Save` が書いた物を読み、一覧をページに分けて読み（`Waiting`、`Mine`。下の「その人を待っているチケット」「その人の申請」）、始めた操作を試みの名前と一緒に保管し（`Begin`。新しい試みには `Renew`。上の「一つのイベントの順」「帳簿が別のデータベースにあるとき」）、チケットのロックを取り（上の「帳簿が別のデータベースにあるとき」）、時計とすることをリースし（下の「時計」「することの一覧」）、どの動きも持たない仮押さえの取り消しを、することとして保管し（`KeepToDo`。上の「帳簿が別のデータベースにあるとき」）、アップロードを、人の一つのキーにつき一つまで記録し、キーから探し、手放し（上の「差し替える物」の Files）、期限を過ぎた操作の記録と、始めたまま答えなかった操作の記録を消し（下の「行をいつまで持つか」）、管理の記録を、それが変える物と一緒に全部か何もなしかで保管して、読む（`SaveChange`、`Records`、始まり、取り込み、下書き。13.8）。`Lock` はロックを運ぶコンテキストを返す。フレームワークはそのコンテキストでチケットを読み、動きを保存するので、ストアはロックを一本の接続で持ち、その接続で読み書きしてよい（PostgreSQL のストアはそうしている）。適合の試験は、ストアをこの全部に照らして確かめる。メモリのストアは、渡された物を、フィールドごとに PostgreSQL と同じに読み返す。フレームワークの試験 `TestStoresKeepEveryField` が、両方からフィールドを一つずつ読み返して確かめる。

ストアは時刻をマイクロ秒まで持つ。それより細かい所は切り捨てる（過去の側に）。持つ時刻も、比べるために渡される時刻（今、それより前、リースの終わり）も同じである。PostgreSQL はそう持ち（ドライバーが時刻をマイクロ秒に切ってから送る）、メモリのストアも同じに切る。一マイクロ秒の中の二つの時刻は同じ時刻なので、時刻で並べる一覧は、そのようなチケットを、同じ時刻のチケットと同じく ID で並べる（下の「その人を待っているチケット」）。メモリのストアで書いた試験も、PostgreSQL と同じ順になる。約束はこれをどのストアにも求め（`Store`）、`WaitingOrder` が確かめる（下の「適合の試験」）。

ストアが返す時刻は、どれも UTC である。渡された時刻のゾーンにも、動いているプロセスのゾーンにもよらない。ストアの答えは、どのマシンでも同じになる。PostgreSQL のドライバーは時刻をプロセスのゾーンで読むので、ストアは読んだ時刻を UTC に直す。メモリのストアは、渡された時刻を UTC にして持つ。ストアのファイルも、ファイルの時刻を UTC で返す。精度は問わない（フレームワークは比べない。`Files`）。`Keeping` が、どちらのゾーンも確かめる（下の「適合の試験」）。

ストアは、人を待っているチケットを、読まずに数えてもよい。そうするストアは `server.WaitingCounter` でもあり、`CountWaiting(ctx, person)` で、その人一人について `Waiting` が読むチケットの数を、種類ごとに返す（種類は、ストアがチケットごとに持つ `TicketRecord.Kind`）。ページのホームは、これで数える（下の「その人を待っているチケット」）。`WaitingCounter` でないストアでは、サーバーがチケットを読んで数える。PostgreSQL とメモリのストアは `WaitingCounter` である。適合の試験の `WaitingCounts` が、この数を `Waiting` に照らして確かめる（下の「適合の試験」）。

人の申請も、同じように数えてよい。そうするストアは `server.MineCounter` でもあり、`CountMine(ctx, person)` で、その人一人について `Mine` が読むチケットの数を、種類ごとに、状態ごとに返す（`MineCount.Status`）。申請者に戻っているチケットには、差し戻された物（`sent_back_by` が、差し戻した人を持つ）と、申請者の側が取り下げた物（`withdraw`）がある。そのうち差し戻された数も返す（`MineCount.SentBack`）。ページのホームは、これで数える（下の「その人の申請」）。`MineCounter` でないストアでは、サーバーがチケットを読んで数える。PostgreSQL とメモリのストアは `MineCounter` である。適合の試験の `MineOrder` が、この数を確かめる（下の「適合の試験」）。

### 4.2 表（PostgreSQL）

表は専用のスキーマに置く（`postgres.New` に別の名前を渡さなければ `requests`）。

| 表 | 持つ物 | キー |
|---|---|---|
| `tickets` | `next` が返したチケットの JSON、種類と版、状態、番号、待っている人をいつから待っているか | `id` |
| `moves` | 動きごとの番号、時刻、人、キー、イベント、すること、組織のスナップショット、チケットの種類と版、入り口（13.8） | `ticket`、`number` |
| `waiting` | チケットごとに、だれをいつから待っているか（`list_waiting` と、ホームの数） | `ticket`、`person` |
| `timers` | 掛けた時計：どの時計か、どのステップか、いつ鳴るか、次にいつ試すか、リース | `ticket`、`timer`、`stage` |
| `todo` | することの一覧：何をするか、試した回数、次にいつ試すか、最後のエラー、リース、済んだ時刻 | `id` |
| `operations` | 操作のキーごとの返事と答えた時刻、そのキーを使った操作のダイジェスト | `person`、`key` |
| `begun` | 始めた操作（申請と出し直し）のうち、まだ返事を保管していない物：操作のダイジェスト、新しいチケットの ID、試みの名前、始めた時刻 | `person`、`key` |
| `organizations` | 組織のデータのスナップショット | `id` |
| `people` | チケットごとの人。動きが名前を挙げた人の全部（申請者の側、差し戻した人、承認者、答えた人）を、チケットが名前を挙げなくなっても持つ（下の「チケットを見られる人」）。そのうち、だれの申請か（下の「その人の申請」） | `ticket`、`person` |
| `files` | 添付ファイル：中身、名前、種類、大きさ、上げた人と時刻 | `id` |
| `uploads` | アップロードごとの、上げた人と時刻、キー（あれば）、チケットが持ったか、手放したか、ファイルを消したか（上の「差し替える物」の Files） | `id` |
| `revoked` | 期限より前に終えたログインのセッション（`oidc` の `Revocations`）。もともと終わるはずだった時刻まで | `session` |
| `records`、`starts`、`drafts` | 管理の記録、管理する人が決めた版の始まり、組織のデータの下書き（13.8） | `records` は `id`、`starts` は `kind` と `version`、`drafts` は `person` と `id` |
| `schema_version` | これらの表の版 | |

チケットが見せる履歴は、動きから読む（動きのすることのうちの `history` の行に、動きの時刻を付ける）。`moves` は、一度書いたら変えない。

### 4.3 表を上げる

`schema_version` は数を一つ持つ。`postgres.New` は、その数から、コードが知る数までの手順を、全部一つのトランザクションで、トランザクションの間だけ効くアドバイザリーロックの下で走らせる。二つのプロセスが同時に起動しても、上げるのは一度である。表の数より小さい数しか知らないコードのプロセスは、起動せずにエラーを返す。古いバイナリが新しい表に書くことはない。手順は足すだけ（表、列、インデックス）にする。行を書き換える手順は、それだけで一つの手順にし、この文書に書き添える。手順は `postgres/schema.go` にある。手順 7 が行を書き換える。待っているチケットのそれぞれに、待ち始めた時刻を入れる。ステップが始まった時刻（JSON の `stages[].started`）か、無ければ最後の動きの時刻である。そして、人を待っているチケットを、その時刻を含むインデックス（`waiting_list`）で読むようにし、人だけで引くインデックスを消す。手順 8 は管理の表を足す（13.8）。手順 9 も行を書き換える。それより前に保管した動きのそれぞれに、そのチケットの種類と版を入れる。手順 10 は、アップロードのキー（上の「差し替える物」の Files）と、手放していないアップロードを、人の一つのキーにつき一つまでにするインデックスを足す。

### 4.4 行をいつまで持つか

`moves` は記録なので残す。チケット、チケットの人、組織のスナップショット、済んだことを済んだ時刻と一緒に持つ `todo` も残す。記録でない物は消す。

- `revoked`：終えたセッションの行は、そのセッションがもともと終わるはずだった時刻を過ぎたら消す。
- `operations`：キーと返事は `Config.OperationsKept` のあいだ持ち（無ければ 30 日。負の長さなら、ずっと）、そのあと `Sweep` が消す。消したあとに同じキーが来たら、別の操作である（下の「答え」）。返事はチケットを含むので、この表は操作のたびに、チケット一つ分ほど増える。一か月は、クライアントが失敗した呼び出しを繰り返す間よりも、人がブラウザの履歴からページをもう一度送る間よりも、ずっと長い。
- `begun`：始めたまま返事を保管しなかった操作（失敗して、送り直されなかった物）は、同じ時期の操作と一緒に `Sweep` が消す。
- `drafts`：組織のデータの下書きは `Config.DraftsKept`（無ければ 1 日）のあいだ持ち、そのあと `Sweep` が千件ずつ消す（13.9）。管理の `records` と `starts` は記録の一部なので残す。

`Sweep` は、ストアに千件ずつ消させる（`ForgetOperations`）。返事も始めた操作も、それぞれ古い物から千件までで、ストアは両方で何件消したかを返す。同じ時刻の物は、人、キーの順に消す。順は Go が文字列を比べる順（バイトの順）で、PostgreSQL はデータベースの照合順序によらず `collate "C"` で並べるので、どちらのストアも同じ物を消す。千件に満たなければ、その時刻より前の物はどちらも残っていないので、`Sweep` はそうなるまで繰り返す。何件あっても一回の掃除で全部消え、一つの文が消すのは一つの表の千行までである。`Keeping` が、どちらのストアでもこの上限と順を確かめる（下の「適合の試験」）。
- `uploads` と `files`：どのチケットも持たないアップロードは、`Config.UploadsKept` のあとで手放し、ファイルを消す（上の「差し替える物」の Files）。行は印を付けて残すので、あとで動きが持つことは無い。アップロード一つにつき短い行一つである。

## 5. 時計

`set_timer` と `cancel_timer` は、それを返した動きの保存で `timers` 表を書き換える（時計は、チケットと時計の名前とステップで決まる。同じ時計にもう一度 `set_timer` すれば、鳴る時刻が動く）。鳴った時計は、鳴ったことによる動きの保存で外す。

`RunTimers` は、期限の来た時計（次に試す時刻が今以前の物）を、古い順に 100 ずつ、30 秒のあいだリースする。ほかのプロセスが持っている時計は飛ばす。それぞれについて、時計のイベント（時計の名前：`"remind"`、`"late"`、`"pause_ends"`、`"resubmit_late"` など）を今の時刻でチケットに渡す。保存は、時計が同じ時刻のまま残っていて、チケットの番号が読んだときのままのときだけ、時計を外す。二つのプロセスが同じ時計を取っても、チケットが動くのは一度である。二つ目は、チケットが動いて時計が外れているのを見て、止まる。リースを持ったまま止まったプロセスの時計は、リースが切れたあとで、別のプロセスが取る。

人のイベントの前には、そのチケットの期限の来た時計を先に渡す（上の「一つのイベントの順」の 2）。期限のあとに送った承認は、期限が変えたあとのチケットに当たる。

`next` が `data` か `unread`（データがその日まで届いていないか、読めない）で断った時計は、一時間あとに回して、もう一度試す。データが直れば、チケットは同じイベントで先へ進む。ほかの理由で断られた時計は外し、断りを、その時計の動きとして保管する。

## 6. することの一覧

`notify`、`launch`、`ask_agent`、`ask_note`（帳簿が別のときは `release` も）は、その動きの保存で、キー（チケット、回、通し番号）を付けて `todo` に書く。監査ログの記録もここに書き、保存しなかった試みの仮押さえの取り消しのうち、帳簿が答えなかった物もここに書く（上の「帳簿が別のデータベースにあるとき」）。`RunToDo` は期限の来た物を、古い順に 100 ずつ、60 秒のあいだリースし、それぞれを行う。

- `notify` は `Notifier.Notify` へ渡す。相手の名前とアドレスは、今日の版の組織から読む。`launch` は `Flows.Launch` へ、`ask_agent` と `ask_note` は `Agents.Ask` へ、`release` はトランザクション無しで `Books.Call` へ、監査の記録は `AuditLog.Record` へ渡す。
- 済んだ物には済んだ印を付け、二度は行わない。
- 失敗した物は、2 秒あと、次は 4 秒、8 秒と、間を最大 10 分まで延ばしながら、終わり無く試す。済むまでが仕事である。試した回数と最後のエラーを、管理する人が見られるように残す。
- 止まったプロセスのリースは切れるのを待ち、次にリースしたプロセスが行う。リースより長くかかった呼び出しは、別のプロセスがもう一度行うことがある。キーがあるので、二度行っても一度と同じになる。

古い順に取るが、失敗した物がほかを止めることはない。送れない知らせがあっても、flow は始まる。約束の中に、一つが別の一つを待つ物は無い。待つ物（帳簿の呼び出し）は保存の中で行う。

## 7. 断り、HTTP の答え、本文

### 7.1 答え

| 起きたこと | Outcome | HTTP | 本文 |
|---|---|---|---|
| イベントを受け取った | OK | 200 | `{"ticket": …}` |
| `start` か `next` が断った（約束の 12 種類） | Refused | 409 | `{"refused": …, "text": …, "text_ja": …, "at": …}` |
| gate が許さなかった | Refused | 409 | `{"refused": "person", …}` |
| 帳簿が拒否した | Refused | 409 | `{"refused": "book", "text": <帳簿の理由>, "text_ja": <その日本語>, "at": null}` |
| その日に効いている組織の版が無い | Refused | 409 | `{"refused": "data", …}` |
| 本文が読めない、POST にキーが無い、キーが 1〜255 バイトの表示できる ASCII でない、キーが別の操作に使われていた | Invalid | 400 | `{"error": …}` |
| `start` か `next` が、受け取らない本文の値で止まった（下の「本文を確かめる」） | Invalid | 400 | `{"error": <入口の文そのまま>}` |
| フォームが計算する物を尋ねる本文が、その形でないか、`Worked` が止まった（下の「フォームが計算する物」） | Invalid | 400 | `{"error": …}` |
| サーバーがいまは受け付けないコメント（下の「コメントを受け付ける間隔と数」）、その日の数を越えた取り込み（13.3） | Limited | 429 | `{"error": …}`。待てば受け付けるときは `Retry-After` |
| チケットが無い、その人が見られない、または種類ごとの操作（`resubmit_`、`edit_`、`ticket_worked_`）にほかの種類のチケットを渡した | NotFound | 404 | `{"error": …}` |
| 今効いている版の無い種類の申請か、フォームが計算する物を、gate が出すのを許す人が尋ねた（下の「その人が出せる申請の種類」） | NotFound | 404 | `{"error": …, "from": <次の版が始まる日>}`。始まる版が無ければ `from` は無い |
| パスが無い | | 404 | `{"error": …}` |
| パスがそのメソッドを受けない | | 405 | `{"error": …}` |
| 本文が `Config.MaxBody` より大きい。組織のデータのアップロードが、サーバーの受け取る大きさより大きい（13.3） | TooLarge か、無し | 413 | `{"error": …}` |
| 本文が `application/json` で送られていない | | 415 | `{"error": …}` |
| 人がいない | | 401 | `{"error": …}` |
| サーバーの失敗 | | 500 | `{"error": …}`。失敗はログに書く |

gate の断りが言うのは、その人がそれをしてはいけないことだけである。チケットへの操作は、本文より先にチケットを読む。その人がチケットをまったく見られないときは（下の「チケットを見られる人」）、本文が何であっても、gate に尋ねる前に、無いチケットと同じく 404 を返す。その人への答えは、チケットについて何も伝えない。断りも、ほかの返事と同じくキーと一緒に保管する。キーを持っているあいだ（`Config.OperationsKept`、上の「行をいつまで持つか」）は、同じキーには同じ断りを返す。ストアがキーを消したあとの同じキーは別の操作で、そのときのチケットに対して行う。送ったときの版のまま送り直した承認は、チケットがもう進んでいるので断る。コメントは、もう一つのコメントになる。申請は、もう一つの申請になる。

断りの本文は約束のとおりである（ringi の DESIGN 12.4）。断りの種類（`refused`）、英語の理由（`text`）、日本語の理由（`text_ja`）、フォームのどこの断りか（`at`）を持つ。`at` は、フィールド（`purpose`）、明細の行のフィールド（`items[1].price`。行は 0 から数える）、明細の行でフォームが計算するセル（`lines[0].subtotal`。範囲の外になった最初の行）で、フィールドに当たらない断りでは null である。空のコメントと長すぎるコメントは、コメントの操作でも、コメントを付けたほかの操作でも、`comment` で、場所を持たずに断られる。説明が理由を求めるステップ（`types/<名前>.json` の `comment_required`）での、理由の無い（コメントが無いか、空か、空白だけの）却下と差し戻しも同じである。断るのは生成したコードで、フレームワークは、ほかのコメントの断りと同じに伝える。gate、帳簿、フレームワーク自身の断りは、両方の文を持ち、場所を持たない。ページ、画面、エージェントのツールは、答える相手の言語で断りを伝える。ページと画面は、`at` が指すフィールドかセルのところに、コメントの断りはコメントのところに出す。どこに出すかは、断りの種類と場所で決め、文でも、断られた操作でも決めない。

断りのほかのエラーの答えは、どれも文を一つの形 `{"error": …}` で持つ（約束のスキーマ `error`。効いている版の無い種類の 404 は `from` を足す。約束の `unoffered`）。約束は、操作が返すエラーの答えを全部書き、`Package.CheckAnswer` がその全部を約束に照らす。約束の操作のどれでもない答え（パスが受けないメソッド、パスでないパス、本文を付けた GET、13.6 の管理の API）は、フレームワークの物で、同じ形である。

操作のキー（`Idempotency-Key`）は、1〜255 バイトの表示できる ASCII（`!` から `~`。空白は無し）である。申請と出し直しのキーは、操作を始めたとき、入口に尋ねる前に、その本文と結び付く（上の「一つのイベントの順」の 3）。同じキーで別の本文を送れば 400 である。入口の 400 は返事を保管しないが、そのあとでも同じである（下の「本文を確かめる」）。フレームワーク自身の確かめが断る本文（下の「本文を確かめる」。ファイルは上の「差し替える物」の Files）は、それより前に答えるので、キーを結び付けない。直した呼び出しは別の操作で、自分のキーを持つ。

### 7.2 本文を確かめる

本文は、`openapi.json` の操作のスキーマに照らして確かめる。チケットへの操作の本文は、そのチケットのパッケージのスキーマに照らす。確かめるのは、JSON の値が一つであること、JSON の型、必須のキー、スキーマに無いキーが無いこと（`additionalProperties: false`）、日付、整数、申請の種類の値の外にある数の範囲（`minimum` と `maximum`。スキーマが書いていれば、版の範囲）、`oneOf` と `$ref` が指すスキーマ、である。値は、生成した入口と同じに読む。どう書いた整数でも（`20`、`20.0`、`2e1`）、64 ビットに収まれば整数であり、日付は、年を 4 桁以上で `YYYY-MM-DD` と書いた、0001-01-01 から 9999-12-31 までの暦にある日である。

合わない本文は 400 である。入口も読む値は、生成したコードが言う文で言う（ringi の DESIGN 12.2 の「入口のエラー」）。フレームワークが先に見つけても入口に任せても、同じ物を同じ文で言うためである。文は、場所を、本文が入る入口の引数で言い（申請の本文は `start` の `submit` に、イベントの本文は `next` の `event` のイベントの値に入る）、そこにある物と要る物を言う。言語は、パッケージの入口が書く言語（`ritsu gen --lang`。パッケージは `ticketkinds.Lang` で言う。上の「生成したパッケージを渡す」）である。

```text
The argument `submit.form.days` of `start` is "three", and must be an integer
The argument `submit.form` of `start` is missing, and must be an object
The argument `submit.form.first_day` of `start` is 2026-13-40, outside its range >=0001-01-01 <=9999-12-31
The argument `event.approve.revision.edits` of `next` is outside int64
関数 `start` の引数 `submit.form.days` が "three" です。整数が要ります
```

入口が見ない物は、フレームワーク自身の文で、本文の中の場所を付けて言う。JSON の値が一つでない本文やオブジェクトでない本文と、フィールドに無いキー（`form: "color" is not one of its fields`）である。スキーマの語（`enum`）は型ではない。入口は選択肢と `clear` の名前を文字列として読み、申請の種類が、持たない語を断る（下）。受け付けない値が本文に二つ以上あるときは、フレームワークはキーの順で最初に見つけた物を言う。入口が最初に見つける物とは違うことがある。ページは、どちらの文も、その文が言うフィールドのところに出す。

人が書いた物（`submit`、`resubmit`、`edit` のフォームと明細の行のフィールド、コメント）は、`start` と `next` に任せる。選択肢に無い値、範囲の外の数、長すぎる文字、多すぎる行、空の必須のフィールド、空か長すぎるコメント、フォームに無い名前を `clear` に書いたもの、である。スキーマは、これらのフィールドも、コメントの操作の `comment` も必須にせず、どれも null を取る。無い、null、空のフィールドやコメントは、そのまま入口に渡す。入口は、ベクタのとおり、フィールドは場所を `at` に入れて `form` で、コメントは場所を持たずに `comment` で断り（上の「答え」）、ページと画面はその断りをそのフィールドかコメントのところに出す。スキーマにはほかの制限（`enum`、`minimum`、`maxLength`、`maxItems` など）も書いてあり、クライアントと画面が使う。サーバーがそれを 400 で返さないのは、約束がそれを断りで返すからで、画面は一つの形の断りを出せばよい。版はこれより厳しく扱う。`revision` の無い本文と、キーの欠けた版や範囲の外の版を持つ本文は 400 である。

入口が受け取る範囲は、受け付ける範囲より広い。スキーマが書いていない、その外の値（スキーマが範囲を書いていないときの、版の回の 0）は、何かを断る前に、生成したコードを種類 `input` のエラーで止める。そのエラーが言う引数が、人の送った物（`start` の `submit`、`next` の `event`）なら、上と同じく入口の文そのままで 400 を返し、キーに返事を保管しない。申請と出し直しのキーは、その本文と結び付いたままである（上の「答え」）。サーバーが渡した引数（チケット、組織の部分、時刻）についての種類 `input` のエラーと、入口のほかのエラーは、サーバーの失敗である（500。ログに書く）。生成したパッケージはそれぞれ自分の型を持つので、フレームワークはエラーを、そのフィールド（`Kind`）と文（バッククォートの中の引数）で読む。

フレームワークは、ritsu が書くキーワード（`type`、`enum`、`required`、`properties`、`additionalProperties`、`items`、`$ref`、`oneOf`、`format`、`minItems`、`maxItems`、`minLength`、`maxLength`、`minimum`、`maximum`、`title`、`description`）だけを知る小さなチェッカーで確かめる。`Load` は、ほかのキーワードを使う `openapi.json` を受け付けない。新しいキーワードを黙って見逃さないためである。`$ref` は、パッケージを読むときに一度ずつたどっておく。本文を確かめても何も書き換えないので、多くのリクエストが同時に確かめてよい。

### 7.3 本文の大きさ

ハンドラーは本文を `http.MaxBytesReader` と `Config.MaxBody`（渡さなければ 1 MiB）で読み、それより大きい本文には、残りを読まずに 413 を返す。例が受ける本文でいちばん大きいのは、20,000 字の本文を持つ契約である。日本語なら UTF-8 でおよそ 60 KB、クライアントがどの文字も `\uXXXX` で書けば、およそ 120 KB になる。添付ファイルはファイルではなく ID なので、フォームが運ぶことはない。`web` は、画面のボタンの操作の本文も同じ大きさまでに限り、ファイルは別のルートで、それぞれ自分の上限（`web.Config.MaxFile`、渡さなければ 10 MiB）まで受け取る。

### 7.4 コメントを受け付ける間隔と数

コメントは、人がチケットを動かさずに何度でもできる、ただ一つの操作である。コメントのたびにチケットの人たちへ知らせが出て、チケットを読むたびの答えが長くなる。そこでサーバーは、一人の一つのチケットへのコメントを `Config.CommentEvery`（渡さなければ 10 秒）に一つ、一つのチケットへのコメントを全部で `Config.CommentsPerTicket`（渡さなければ 100）まで、そのうち一人のコメントを `Config.CommentsPerPerson`（渡さなければ 25）まで受け付ける。一人で、ほかの人の分までチケットのコメントを埋めないためである。負の値は制限しない。どれかを越えるコメントには 429（Limited）を返し、保管しない。同じキーは、あとでそのコメントに使える。一つ目のときは、その人の次のコメントを受け付けるまでの秒数を `Retry-After` に書く。ほかのときは、待っても変わらないので書かない。

サーバーは、どちらもチケットの動きから読む。数えるのは受け付けたコメントで、その人の最後のコメントは、その人のうちいちばん新しい物である。そのため、一つのストアを使う複数のサーバーも同じに数える。同時に送られた二つのコメントは、チケットのほかの二つの動きと同じく、一つずつ受け付ける（3 章）。二つ目は、一つ目が動かしたチケットでやり直し、一つ目と合わせて数える。

制限は、その人がチケットを見られると分かったあと（見られない人には 404）、gate のあと、入口の前に確かめる。ページと A2UI の操作も同じく答え（429。入力した物は残す）、エージェントのツールは結果 `limited` で答える。

約束のほかの操作は 429 で答えない。アップロード（`web` の `POST /files`）は別に制限し、`Config.UploadsPerPerson` を越えれば `Retry-After` の無い 429 で答える（上の「差し替える物」の Files）。管理の操作では、組織のデータの取り込みも、日ごとに制限する（`Config.ImportsPerDay`。13.3）。

### 7.5 フォームが計算する物

どの種類にも、フォームが計算する物を読む操作が二つあり、どちらも何も変えない。

- `worked_<名前>`（`POST /requests/<名前>/worked`）は、新しい申請が今取る版で答え、ログインしただれにでも答える。答えは種類の決まりとフォームだけで決まり、画面も同じ決まりを、開いただれにでも渡しているからである。効いている版が無ければ、`submit_<名前>` と同じに答える（下の「その人が出せる申請の種類」）。
- `ticket_worked_<名前>`（`POST /requests/<名前>/tickets/{id}/worked`。出し直しか直しのある種類）は、チケットを出した版で答え、そのチケットを見られる人にだけ答える。ほかの人と、ほかの種類のチケットには 404 で答える。チケットからは版だけを読み、フォームは本文の物を使い、チケットの中身は答えに出さない。

どちらも、途中まで埋めたフォームと承認者の直し（無ければ null）を `{"form": …, "edited": …}` で受け、版の `<名前>.worked.in` に照らして確かめる。答えは種類の `Worked` の答えそのまま（`<名前>.worked.out`）で、条件つきのフィールドを見せるか、要るか、計算するフィールドの値を言う。まだ決まらない所は null である。範囲の外の値も、途中までのフォームも、エラーでなく答えである。形の合わない本文はフレームワークの文で、入口が止めた入力は入口の文で、400 で答える（`unworked`）。入口の panic はサーバーの失敗（500）である。どちらもキー、版の番号、コメントを取らず、409 も 429 も返さない。同じ名前のエージェントのツールが、これを呼ぶ。

ページと A2UI の画面は、同じ入口に `Server.Worked(kind, version, in)` で尋ねる。手で書いた種類で入口が無ければ false を返し、画面はフォームを埋める前の形で見せる。

## 8. gate に尋ねる

どのイベントも、gate の動作のどれか一つに当たる。`see` だけはイベントに当たらない。チケットの申請者の側にも承認者の側にもいない人について、その人がチケットを読むか何かをする前に尋ねる動作である（下の「チケットを見られる人」）。管理の操作は、自分の四つの動作を尋ねる（13.4）。`change_request_type`、`read_audit`、`import_organization`、`read_organization_audit` である。

| イベント | 動作 | 対象 |
|---|---|---|
| `submit`、`resubmit` | `submit` | チケット |
| `withdraw`、`discard` | `withdraw` | チケット |
| `approve`、`edit` | `approve` | ステップ |
| `reject` | `reject` | ステップ |
| `send_back` | `send_back` | ステップ |
| `pause`、`resume` | `pause` | ステップ |
| `retract` | `retract` | ステップ |
| `add_approver` | `add_approver` | ステップ |
| `hand_over` | `hand_over` | ステップ |
| `comment` | `comment` | ステップ |
| （無し） | `see` | チケット |

問いには、ritsu が gate のために決める値を添える。

- `Requester`：チケットの申請者（`submit` では、申請を出してもらう人）。
- `Requesters`：申請者の側。申請者、代わりに出した人、その日に申請者の代わりに出せる代理（代理のうち、その種類を挙げるか、何も挙げない `submit` の行）。
- `Approvers`：チケットがステップにあるあいだは、そのステップの承認者と、その日に効いているその代理。申請者に戻っているあいだは、差し戻した人。ほかのときは、だれもいない。ステップは、チケットの版が指す物（`revision.stage`）で、同じ名前の最初の物ではない。あとのステップを待ったステップは一覧に二度あり、一度目は待ったときのままである（ringi の DESIGN 5.12）。チケットが待っている人と、待ち始めた時刻も、同じステップから決まる（10.2）。
- `Owner`：その種類を持つチーム。`SelfApproval`：その経路が、申請者が自分の申請を承認するのを許すか（下の「UTC オフセットと自己承認」）。
- `Org`：その日に効いている組織の版。その人の部署などを読む gate のためにある。
- `Stage`、`Ticket`、`Kind`、`Version`、`Now`、そしてログインで分かった役割を持つ `Person`。

sekisho が書いた gate を包むときは、これらの値から gate のストアを組み立てる。ステップの `approvers` と `requesters` はグループで、その人の ID が一覧にあれば、その人はグループに入っている。例に一つある（`examples/gate.go`）。人の部署は、主な所属のチームから上へたどって最初に当たる、持ち主のチームと同じ種類のチームで、`OrgVersion.TeamOf` で求める（上の「コマンドラインからサーバーを動かす」）。その版で主な所属の無い人には、部署が無い。持ち主の無い種類には、どのチームも持たない ID を持ち主として渡す。部署を読まない規則のほかは、その種類について何も許さないようにするためである。gate には、gate が宣言する役割だけを添えて尋ね、ほかの役割は、それぞれ一度ログに書く（13.4）。

管理する人の `add_approver` と `hand_over` は、送った人をイベントの `by` に書く。ベクタのこの二つのイベントには人が書いてない（`by` が null）。人を書いても、`next` の答えは、履歴の行の `by` のほかは変わらない。

### 8.1 その人が今できること

画面は、その人が今チケットに対してできる操作のボタンだけを見せる。

```go
// Can は、その人がそのチケットに今渡せるイベントを、渡した順で返す。
func (s *Server) Can(ctx context.Context, p server.Person, ticket string, events []string) ([]string, error)
```

gate だけでは、これは決まらない。gate が言うのは、だれがしてよいかで、チケットがそれを受けるかではない。終わったチケットの申請者も、申請者のままである。そこで `Can` は、操作と同じように両方に尋ねる。次の三つがそろうイベントを返す。

1. 種類の操作のどれかが、そのイベントをチケットに渡す。新しい申請の `submit` や、承認者が何も記入しない種類の `edit` は、ここで外れる。
2. gate が、そのイベントの動作を許す。問いは、操作が尋ねる物と同じである。
3. 種類の `next` が、チケットの状態やその人がだれかを理由に断らない（`state`、`person`、`event`、`limit`）。

見るのは、操作が見るのと同じチケットである。時刻の来た時計を先に渡し（3 章）、それはメモリの上だけで行って、試し（2.2）と同じく何も残さない。期限を過ぎたチケットは、時計がまだ鳴っていなくても、承認を受けない。無いチケットと、その人が見られないチケットは `ErrNotFound` になる。組織の版がどれも効いていない日には、何も受けない。

`next` には、イベントがいちばん少なく言うことを、チケットの今の版で、コメントを付けて渡す。もう一度の申請と編集は何も変えずに、差し戻しは申請者へ、である。人を挙げるイベントは、ステップの部分ごとに、組織の人のうち申請者の側でもその部分の承認者でもない一人と、申請者の側を挙げてみて、どれかを受けるなら受けると見る。例の `TestCanIsWhatIsTaken` が、人やステップを挙げる操作を全部かけて、これを確かめる。

ほかの断りでは、ボタンを残す。その人が書いた物についての断り（`form`、`comment`）か、画面に出ない物についての断り（`data`、`clock`、`unread`、`book`）で、理由は操作の答えが言う。帳簿にも尋ねない。予算が足りないことは、操作が帳簿に尋ねたときに言う。コメントは、サーバー自身の限り（7.4）も見る。あとで受けるコメントは「できる」とし、待つ長さは答えが言う。もう受けないコメントは「できない」とする。

`Can` に尋ねるのは画面を作るときで、ボタンを押すまでにチケットが動くことがある。操作は、行うときに gate と `next` にもう一度尋ねる。

画面には、gate が許すイベントの数だけ `next` の呼び出しがかかる。渡すのは、それぞれのイベントに要る組織の部分である（9.2）。サーバーは別々のゴルーチンで同時に呼ぶので、承認者のカードは、操作一つとほぼ同じ時間でできる。ノートパソコンで 1 ミリ秒ほど（0.6〜1.6 ミリ秒）で、例の 24 人の組織でも、1,200 人、5,000 人、20,000 人の組織でも変わらない。種類ごとに `start` へ尋ねるホーム（10.1）も、同じくらいである（0.7〜0.8 ミリ秒）。

ボタンを今見せていないので操作を行わない画面にも、前に受け付けた操作がもう一度届くことがある。ボタンを二度押したときや、前のページに戻って押したときである。

```go
// Replied は、その人のチケットへの操作に前に何と答えたかと、答えたかどうかを返す。
func (s *Server) Replied(ctx context.Context, c server.Call) (server.Reply, bool, error)
```

`Replied` は、その人のキーと一緒に保管した返事（3 章）を返す。キーを使ったのが同じ操作、同じチケット、同じ本文で、その人がそのチケットを見られるときに限る。何も行わない（`web` の DESIGN の 9.2）。

もう一度の申請と承認者の編集は、フォームから変えた物を送る（ringi の DESIGN 7.3）。だからその送信は、ページが見せたフォームと比べて読む。チケットは、そのフォームをもう持っていないことがある。どのフォームだったかは、ページが見せた版で分かる。

```go
// FormAt は、チケットがその版だったときのフォームを返す。何度かその版だったなら、最後の物である。
func (s *Server) FormAt(ctx context.Context, p server.Person, ticket string, revision json.RawMessage) (form json.RawMessage, ok bool, err error)
```

`FormAt` は、`Replay` と同じように、チケットの動きを種類の入口へもう一度渡す。チケットがその版を過ぎた所で止める。過ぎた所とは、あとの回に入ったか、同じ回で編集の数が増えた所で、チケットはそこから戻らない。チケットがその版だったことが無いとき、チケットが無いか、その人が見られないときは、`ok` が false になる。フォームを、動きのほかに保管することはしない。今のチケットを見せているページには、これは要らない。費用がかかるのは、前に見せたページからの送信だけである。

## 9. 組織

### 9.1 スナップショット

`ImportOrganization` はソースを読んで確かめ（13.3）、一つのスナップショットとしてストアに保管する。スナップショットの名前はデータのダイジェストで、同じデータをもう一度読めば同じスナップショットになる。取り込むのは、ソースが前に取り込んだときと違うデータを返すときだけである。起動し直したサーバーが、そのあいだに管理する人が取り込んだ物を戻してしまわないためである（13.3）。スナップショットは変えない。データが変われば、新しいスナップショットになる。動きは、組織の部分を作ったスナップショットを記録するので、どの動きも、そのときと同じように `next` へもう一度渡せる（`Replay`）。プロセスは最新のスナップショットをメモリに持ち、30 秒ごとに新しい物が無いかを見る。一つのサーバーが受け持つ組織は一つで、二つの組織を読む種類は `New` がエラーにする。

組織のファイル（版ごとのファイル、代理、マスタのファイル）は、ritsu と同じに読む。読むのは `server.ReadOrgFiles` で、パッケージ `orgfiles` もアップロード（13.3）も、これで読む。マスタのファイルは、フィールドの列を、フィールドの名前でも、`.ringi` のファイルがフィールドを書いた名前でも書ける（たとえば、日本語で書いた種類のフィールド `name` なら `名前`）。行は、どちらで書いてもフィールドの名前で持つ。そのため、どちらで書いた同じ行も同じデータで、同じスナップショットになり、もう一方の名前で書いたアップロードも、ソースの物と何も違わない。一つのフィールドの列を両方の名前で書いたファイルは、どちらでも読まない。その問題は、二つの列を挙げる。種類の説明が optional とするフィールド（`types/<名前>.json` のマスタのフィールドの `"optional": true`。`columns.md` は「Needed: no」と書く）は、列が無くてもよい。そのときは、行はそのフィールドを、空のセルと同じに空の値として読む。マスタのキーは optional にならない。その列を書かない版のデータは、ディレクトリからでもアップロードからでも、列を空で書いたデータと同じスナップショットになる。

人は言語を持てる（`language`。`people.csv` の、無くてもよい列か、`organization.json` のフィールドで、null は「無し」）。`en`、`ja`、無しのどれかで、小文字で書く。ほかの値は、ファイルと行を挙げた問題になり、英日で言う（「人 `p2` の `language` が `fr` です。`en` か `ja` にするか、空にしてください」）。言語の無いデータは、列があっても無くても、列を読む前と同じスナップショットになる。人の言語は、本人が選んだ言語とブラウザの言語の次に使う。ページ、画面、管理のページ、エージェントのツールは、一つの関数で言語を選ぶ（`server.LanguageChoice`。選んだ言語、`Accept-Language`、ヘッダーが `q=0` で断っていなければ人の言語、種類の言語、最後に英語）。知らせは、人ごとにその人の言語で送る（`Recipient.Language`。2.3）。イベントに渡す組織の部分（9.2）には入れないので、どの規則も読まない。

`People` と `Teams` は、今日効いている版の人とチームを、最新のスナップショットから、名前（人はメールアドレスも）と一緒に返す。ログインの仕組みはその中からメールアドレスで人を探し（`oidc.PeopleOf`）、ページは、フォームが人やチームを指す所でそれを選べるようにする。

`get_organization` は、今日効いている版を、その版にいる人にだけ返し（人ごとに `language` を付ける。無ければ null）、ほかの人には 404 を返す。全員のメールアドレスを含むためである。組織にいない人（ログインの仕組みが誤って通した人や、辞めた人）には要らない。404 はわけを言う。"no version of the organization is in effect on 2026-03-20" か "the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-16" である（`server.OrgUnread`）。

### 9.2 イベントに渡す組織の部分

`start` と `next` は、組織を、渡された部分からだけ読む。サーバーは、チケットとイベントごとに、約束のとおりに部分を切る（ringi の DESIGN 7.5）。部分に入るのは次の物である。

- `day`：イベントの日。種類の UTC オフセットで読む。
- `version`：その日に効いている版の日。
- `teams`、`people`、`members`：その版の、チケットとイベントに要るチームと人と、その人の所属。どれもデータの順に並べる。人は ID と名前である（`{"id", "name", "name_ja"}`。データに無い名前は空の文字列）。入口は断りの文で人をこの名前で言う：`e1201 (Sales staff 1)`、`e1201（営業担当 1）`。名前の無い人は `e1201` だけである。取り込みは名前の無い人を断る（13.3）が、ほかの方法で書いたストアには、いることがある。
- `positions`：種類の役職。低い順に並べる。
- `proxies`：その日に効いている代理のうち、その種類を挙げるか、何も挙げない物で、本人が部分にいる物。
- `rows`：種類が読むマスタの行のうち、フォームが選んだ物。その日に選べる行（`valid_from`、`valid_to`）から選び、マスタは種類が付けた名前で呼ぶ。

チケットとイベントに要る物は、三つから始まる。一つは、チケットとイベントが挙げる人である。申請者、代わりに作った人、承認者、答えた人とその席、イベントをする人、イベントが足す承認者と、引き継ぐ前と後の承認者が、それに当たる。二つ目は、フォームが挙げる人とチームと、フォームが選んだマスタの行と、その行が挙げる人である。どのフィールドがそれかは、種類の説明（`form`、`masters`）に書いてある。三つ目は、どのチケットでも種類が要る人とチームで、説明の `organization.needs` に並ぶ。`people` と `teams` のほかに、所属する人を全員入れるチームの `members_of` がある。ステップの部分が尋ねるチームも、全員を入れる。サーバーは、ここへ、足す物が無くなるまで、部分の人が所属するチーム、その上のチームとそれぞれの長、部分の人の効いている代理をする人を足す。

部分に要る物より多くの組織を渡しても、入口の答えは同じである（ritsu の試験が、版の全部を渡して確かめる）。切っても答えは変わらず、変わるのは一つのイベントの費用である。組織がどれだけ大きくても、部分には一つのチケットの人とチームだけが入る。サーバーがベクタのとおりに切ることは、`conformance.Vectors`（12 章）が全部のベクタで確かめる。

`Load` は、`organization.needs` の無い説明をエラーにする。ritsu は、どの種類にもこれを書く。人、チーム、所属、マスタの行がデータのどこにあるかは、部分を切るために初めて要るときに索引を作り、スナップショットと一緒に持つ。

### 9.3 UTC オフセットと自己承認

種類は日を UTC オフセット（使う最初のカレンダーのオフセット）で読み、経路が申請者に自分の申請の承認を許すことがある。後者は、各ステップの `self_approval` として gate に伝える。ritsu は、どちらも `types/<名前>.json` に書く。`utc_offset_minutes` は UTC から東へ何分かの整数（`+09:00` なら 540）で、`self_approval` は `true` か `false` である。ritsu は、どの種類にも二つを書く。フレームワークは、この二つを説明からだけ読み、設定で渡す物は無い。`Load` は、どちらかが null の説明と、オフセットが一日以上の説明をエラーにする。例の説明は、どの種類も `utc_offset_minutes` が 540、`self_approval` が `false` である。

種類のほかのカレンダーは、オフセットが違ってもよい。時計は、数えるカレンダーのオフセットで読んだイベントの日から営業日を数え、そのオフセットの時刻に鳴る（ringi の DESIGN）。時刻を決めるのは機械で、サーバーはそれを保ち、その時刻に時計を鳴らす。サーバー自身はカレンダーを読まない。

### 9.4 人、チーム、マスタの行を探す

探す操作（ringi の DESIGN 12.4、13.2）は、フォームが選ぶ値を言葉で探す。`find_people`（`GET /find/people`）は今日効いている組織の版の人を、`find_teams`（`GET /find/teams`）はチームを探し、`find_master_<ファイル>_<マスタ>`（`GET /find/masters/<ファイル>/<マスタ>`。種類が選ぶマスタごとに一つ）は、ある日に選べるマスタの行を探す。答えは `{"found": [{"value", "name", "label"}], "more": …}` で、見つかった値を近い順に `limit` 件まで並べ、ほかにも見つかるかを言う。人とチームは、`get_organization` と同じく組織の人だけが読む。ほかの人と、効いている版が無いあいだは、`find_people` と `find_teams` は引数を読む前に、同じ文で `get_organization` の 404 を返す（見られないチケットの操作と同じく、400 より 404 が先）。マスタの行は、`get_master_<ファイル>_<マスタ>` と同じく、ログインしている人ならだれでも読める。スクリプトの無いページは、人とチームを読めない人（チケットを出したあとで組織からいなくなり、そのチケットを開く人）には、人やチームを並べず、ID を打つ入力にして、チケットの値はそのまま見せる。

```
GET /find/people?q=e1203&lang=ja

{"found":[{"value":"e1203","name":"営業担当 3","label":"営業担当 3（e1203）"}],"more":false}
```

- `q`：言葉。UTF-8 で 200 文字（コードポイント。`server.FindLongest`）までで、Unicode の White_Space の文字で区切る。どの言葉も、たたんだ形で、ID かキーか、どちらかの言語の名前のたたんだ形に含まれる値が見つかる（`server.Fold`：NFKC にし、White_Space の文字を除き、ひらがなをカタカナにし、一字ずつ単純な小文字にし、語末の「ς」を「σ」にする）。言葉が無ければ、全部の値が見つかる。
- 近い順：言葉が打ったままの値、次にたたんで値と同じ物、名前と同じ物、ID かキーか名前が最初の言葉で始まる物、残り。同じ近さの値は、ID かキーのコードポイントの順である。
- `limit`：1 から 100（`server.FindMost`）。無ければ 20（`server.FindShown`）。
- `lang`：`en` か `ja` で、`name` と `label` の言語である。無ければ、呼び出した側の言語（`Call.Languages`。HTTP の API なら `Accept-Language`）のうち最初のどちらか、それも無ければ `en` である。`label` は、名前に ID かキーを添えた物（「Sales staff 3 (e1203)」、「営業担当 3（e1203）」）で、名前が空か、名前が ID かキーと同じなら、ID かキーだけである。
- `on`：マスタの行だけの引数で、行を選べる日（`valid_from`、`valid_to`）。`YYYY-MM-DD` で、無ければ今日である。
- 引数は `q`、`limit`、`lang`、`on` の順に読み、受け付けない最初の物を、一覧の 400 と同じ形の 400 で言う（`limit: a whole number from 1 to 100 is expected, not "0"`、`lang: en or ja is expected, not "fr"`）。空で渡した引数は、渡さなかったのと同じである。

どの入口も、一つの関数で探す。API とエージェントのツール（`Server.Do`）、画面の `/find`（`Server.FindValues`。200 と 400 の本文はバイトまで API と同じ。`web` の DESIGN 5.1）、スクリプトの無いページ（`Server.FindChoices` と `server.Find`）、例のログイン（`server.FindQuery.Answer`）である。自前のページも、同じ関数で同じに探せる（`PeopleChoices`、`TeamChoices`、`Find`、`ReadFind`、`FindQuery.Answer`）。探す操作のベクタ（`organization/find.jsonl`）は、どの行も同じに答える（`TestFindVectors`）。

たたむのに `golang.org/x/text/cases` は使わない。その `Lower` は「İ」を二文字にし、語末のシグマを前後の文字で決め、`Fold` は「ß」を「ss」にするが、探す操作はそうしない。たたんだ形は持っておかず、探すたびに値をたたむ（`BenchmarkFind`：5 万人から 12 ミリ秒。一度の実行で）。

## 10. 一覧と、だれが何を見られるか

### 10.1 その人が出せる申請の種類

`list_requests` は、その人が今日、自分の申請として出せる種類を並べる。フレームワークは、種類自身の `start` に空のフォームで尋ね、`data`、`submit`、`person` のどれかで断られなければ並べる。その前に gate にも尋ねる（`submit`。その人の新しいチケットについて）。だれが出せるかを決めるのは、生成したコードの一か所だけになる。

これは、生成した `start` が確かめる順（ritsu の ringi の DESIGN.md の、遷移の節にある「確かめる順」）に頼っている。組織の部分（`data`）、次に `submitted by`（`submit`）、次にコメントの長さ、その後でやっとフォームの値、という順である。だから、空のフォームが `form` で断られるのは、その人が出せるときだけになる。ritsu がこの順を変えれば、一覧は間違う。例の試験 `TestListRequests` が、例の 24 人と 10 の種類のすべてで、これを確かめている。一覧に出る種類（230）では空のフォームが `form` で断られ、一覧に出ない種類（10。取締役会の 5 人は休暇を申請できない）では `submit` か `data` で断られる。

並べる種類は、それぞれ名前、版、題、説明を、種類の説明（`types/<種類>.json`）が書くままに返す。だれが尋ねても同じ答えで、尋ねた人の言語では変わらない。約束の `request_kind` に `language` と `labels` があれば、種類を書いた言語（`language`）と、ほかの言語での題（`labels`。`lang` と `text` の組）も返し、クライアントが自分の言語の題を選ぶ。無い約束には返さない。`request_kind` はほかのキーを許さないからである。例の試験 `TestAnswersAsTheContract` が、どの人への答えも約束に合うこと（`Package.CheckAnswer`）と、説明が種類のファイルのとおりであることを確かめ、根の `TestListRequestsAsTheContract` が `request_kind` の二つの形を確かめる。

今効いている版の無い種類（どの版もまだ始まらないか、管理する人が止めた。13.2）は並べず、その `submit_<名前>` と `worked_<名前>` には 404 で答える。先に、一覧と同じ尋ね方で、申請を出せるようになるときに、その人が出せるかを尋ねる。いちばん近い後の版で、その始まりの時に、その日の組織で尋ね、始まる版が無ければ、いちばん上の版で、今か、使える最初の日の遅いほうで尋ねる。出せない人には、無いパスと同じ答え（`{"error": "no such path"}`）を返し、種類の版について何も言わない。出せる人には `unoffered` で答える。`{"error": …, "from": "YYYY-MM-DD"}` で、`from` はいちばん近い後の版が始まる、その版のオフセットでの日である。始まる版が無ければ `from` は無い。エージェントのツールも `from` を返し、「この申請は2027-04-01から出せます。」（"This request may be made from 2027-04-01."）と言う。画面は `Server.Offered` に日を尋ねる。`unoffered` と同じに答え、`unoffered` が日を言わない所では false を返す。出せる人の A2UI の申請書には、その種類の `offered` の画面を、出せる日とホームへ戻る先を埋めて返す。その種類の最新のパッケージが画面を持つときで、画面が無いパッケージ（約束の同じ版で、画面が加わる前に生成された物）には、前と同じく `unoffered` の JSON を返す。切り替えの前に読んだフォームへのアクションにも、同じように答える（`web` の DESIGN 2.1）。

### 10.2 その人を待っているチケット

保存のたびに、チケットがだれを待っているかを書く。ステップにあるあいだは、まだ済んでいない部分の承認者のうち、答えていない人である。申請者に戻っているあいだは、申請者と、代わりに出した人である。`list_waiting` は、その人を待っているチケットと、その人がその日に代理として答えられる人を待っているチケット（代理が挙げる種類のもの）を読む。

`list_waiting`（`GET /waiting`）は、これをページに分けて（ringi の DESIGN 12.4）、新しい順に返す。チケットが待ち始めた時刻（`Move.Since`：待っているステップが始まった時刻か、チケットが申請者に戻った時刻。コメントのように、チケットを動かさない動きは時刻を変えない）の新しい順で、同じ時刻のチケットは ID の大きい順である。`limit` は 1 から `server.MaxPage`（100）までで、無ければ `server.DefaultPage`（50）である。ほかの `limit` は 400 になる。`before` は前のページの最後のチケットの ID で、ページはその次から始まる。一覧に無い ID なら、空のページになる。答えは `{"tickets": […], "more": …}` で、`more` はページのあとに続きのチケットがあるかである。サーバーはそれを知るために `limit` より一件多く読むので、`limit` の数まで埋まったページでも、最後のページなら `false` になる。`Server.Waiting` は、`WaitingQuery`（`Kind` で一つの種類に絞り、`Before`、`Limit`。`Limit` が 0 なら全部）でチケットだけを返す。ページと画面（`web`）が使い、API と同じく、見せる数より一件多く求める。ページの一覧は 50 件ずつ見せ、続きがあるときだけ次のページへのリンクを置く。A2UI の一覧は `/more` を入れ、次のページを読むボタンを見せる（`web` の DESIGN）。

ストアは、一ページを読む（`Store.Waiting`）。その人か、その人がいずれかの日に代理する人を待っているチケットを、一覧の順で読む。サーバーは、そのうちその人がその日に答えられる物を残し、残した数がページに足りなければ、ストアのページの続きを読む。PostgreSQL は、人ごとのページを、インデックス `waiting_list`（`person`、時刻、チケット）で読む。時刻はマイクロ秒まで持つ（上の「ストア」）。一マイクロ秒の中で待ち始めた二つのチケットは、同じ時刻に待ち始めた。適合の試験の `WaitingOrder` が、ストアでこの順を確かめる（下の「適合の試験」）。

ページのホームは、その人を待っているチケットの数を、種類ごとに見せる（`Server.CountWaiting`）。`Server.Waiting` が絞らずに返すチケットを数えた物だが、全部を読みはしない。ストアが `WaitingCounter`（上の「一つの操作」）なら、その人を待っているチケットはストアが数える。サーバーが読むのは、その人が代理する人を待っているチケットだけで、そのうち、その人がその日に答えられる物を、`Server.Waiting` と同じように選んで数える。その人も待っているチケットは、その人の物として一度だけ数える。ほかのストアでは、サーバーが全部を読んで数える。PostgreSQL は一つのクエリで数える。表 `waiting` のその人の行を、チケットの種類と一緒に読み、種類ごとに一行を返す。チケットの JSON は返さない。その人の行が全体のうちで少なければインデックス `waiting_list` で読み、多くを占めれば表を全部読む。

ストアが数えるのと、代理する人を待っているチケットを読むのは、別々の二回の読み出しで、一つのスナップショットではない。その間に動いたチケットは、そのページでは二度数えられるか、数えられないことがある。次に見せるホームでは、そのときの形で数える。その人自身を待っているチケットは、ストアが持つ種類で数えるので、版は問わない。代理する人を待っているチケットは、`Server.Waiting` と同じく、サーバーが持つ版のものでなければならない。

### 10.3 チケットを見られる人

チケットを見られるのは、次の人である。

- **申請者の側**：申請者と、代わりに出した人。その日に、どちらかの代わりに出せる代理（代理の `submit` の行）も含む。
- **承認者の側**：差し戻した人、どれかのステップの承認者である人と承認者だった人、どれかに答えた人と、その人が代わりに答えた承認者。その日に、このうちのだれかの代わりに承認できる代理（`approve` の行）も含む。代理を足すのは、その代理の側だけである。申請者の承認の代理は、申請者自身の申請を見られない。承認者の申請の代理は、その承認者を待っているチケットを見られない。
- **gate の `see` が許す人**：gate には、上の「gate に尋ねる」の値を添え、チケットを対象（`Ticket`）に、種類を持つチームを持ち主（`Owner`）にして尋ねる。尋ねるのは、上の二つの側にいない人についてだけである。

どの入口も、何より先にこれを確かめる。`get_ticket` と、API、エージェントの道具（`check_operation` も）、ページ、A2UI の画面から来るチケットへの操作の全部である。見られない人には、gate にその操作を尋ねる前に 404 を返す。gate がその操作を許すときも同じである。操作を許すことは、チケットを見せることを含まない。操作の答えはチケットの全部を載せるからである。`server.AllowAll` は `see` も許すので、これを使えば、だれでもどのチケットも見られる。一覧（`list_waiting`、`list_mine`）は、ほかに何を見られても、その人の物だけを返す。

例の gate（`company.gate`）は、種類を持つ部門の人に、その種類のチケットを見せる。その部門の管理者も含む。gate がコメント、承認者を足すこと、承認者の分をほかの人に渡すことを許す人のうち、二つの側にいない人はその部門の人なので、自分が手を入れるチケットを見られる。ほかの部門の人で、チケットに名前の無い人には、どの入口も 404 を返す（`TestOwningDepartmentSees`）。

後ろのステップから前のステップへ差し戻したチケットは、後ろのステップの承認者の名前を挙げなくなる。ストアは、チケットの動きが名前を挙げた人を全部持つ（`Move.People`、表 `people`）ので、その人たちは差し戻しのあとも、チケットと添付ファイルを見られる。

### 10.4 その人の申請

`list_mine`（`GET /mine`）は、その人が申請者であるか、ほかの人の代わりに出したチケットを、`list_waiting` と同じ形で、新しい順に返す。動きが初めてその人を申請者か代わりに出した人として挙げた時刻の新しい順で、同じ時刻のチケットは ID の大きい順である。ページの分け方は `list_waiting` と同じで（`limit`、`before`、答えの `more`）、`kind` で一つの種類に絞れる（サーバーが受け持たない種類なら 400）。自分の申請は、いつでも見られる。`Server.Mine` は、`MineQuery`（`Kind`、`Status`、`Before`、`Limit`）で同じ物を返す。ページと画面（`web`）が使う。ストアは一ページを読み（`Store.Mine`）、PostgreSQL はインデックス `people_mine`（`person`、時刻、チケット。その人の申請の行だけ）で読む。

ページのホームは、その人の申請の数を、種類ごとに、状態ごとに見せる（`Server.CountMine`）。`Server.Mine` が絞らずに返すチケットを数えた物だが、チケットは読まない。ストアが `MineCounter`（上の「一つの操作」）なら、ストアが数える。ほかのストアでは、サーバーが全部を読んで数える。PostgreSQL は一つのクエリで数える。表 `people` のその人の申請の行をインデックス `people_mine` で読み、チケットと一緒に見て、種類と状態ごとに一行を返す。チケットの JSON は返さない。ホームは、種類ごとに五件を並べる。その人に戻っている物（`MineQuery` の `Status` を `with_requester` にして読む）を、いつ出した物でも先に並べ、そのあとに新しい物を並べる。

## 11. 版

- **約束。** `ticketkinds.Contract` と `x-ritsu-contract` は一つの数である。フレームワークは読める数を並べ（`server.Contracts`。3）、`Load` はほかの数を受け付けない。ritsu は、古い数のために書いたサーバーが新しい物を読み違えるときに数を上げる。約束の版 3 は、チケットのどのステップにも、リマインダーが鳴ったか（`reminded`。ringi の DESIGN 12.2）を持たせ、ストアはそれを、入口が答えたとおりにチケットの JSON に保つ（`conformance.Reminded`。12）。版 2 のサーバーが保ったチケットには `reminded` が無い。フレームワークは、それを読み替えず、無いまま入口にも渡さない。`Import` は断り、読み出しとイベントはサーバーの失敗（500）になる。どれも、どのチケットのどのステップか、約束の版 3 はどのステップにもそのキーを持たせること、前の版のサーバーがそのチケットを保ったことを言い、失敗はそれをログに書く。
- **種類。** チケットは、出したときの版を終わりまで持つ。古い版のチケットが開いているあいだは、新しいパッケージと一緒に古いパッケージも渡す。
- **ストア。** `schema_version`（上の「表を上げる」）。

## 12. 適合の試験

十の試験が、サーバーを約束に照らして確かめる。どれも、どのストアにもかけられる。

- **ベクタ**（`conformance.Vectors`）：ベクタを一本ずつ、空にしたストアのサーバーに渡す。ベクタのチケットをそのままストアに入れ（`Import`）、時計をベクタの時刻にし、組織は例のデータにする。イベントは API から入れる。人の操作は HTTP のハンドラーから、時計、エージェントの答え、ベクタに人が書いてない操作、API に操作の無いイベント、API が 400 を返す本文は `Deliver` から入れる。API の本文には `attempt` が無いので、サーバーには、ベクタが試みに付けた名前を渡す（`Config.NewAttempt`）。返事と、ストアが保管したすることが、ベクタのとおりでなければならない。同じ種類と同じ場所（`at`）の断り（文は比べない。ringi の DESIGN 12.4）か、同じチケットと同じすることか（null とキーが無いことは同じと見て、キーの順は問わない）である。種類の入口に渡した組織の部分も、ベクタの部分と同じでなければならない（9.2）。HTTP のハンドラーの答えは、どれも、約束がその操作のその状態の答えに決めた形でなければならない（`Package.CheckAnswer`。7.2 の確かめ方で見る）。チケットへの人のイベントを渡す前に、`Can`（8.1）が、ベクタと同じことを言わなければならない。ベクタがそのイベントを受けるか、書いた物で断る（`form`、`book`、`comment` のイベントのコメント）なら、できる。チケットの状態やその人がだれかで断る（`state`、`limit`、`person`、`event`。イベントが挙げる人やステップについての断りは除く）なら、できない。動いたあとは、ストアの一覧（`Store.Waiting`）が、チケットが名前を挙げる人のうち、チケットが待っている人（10.2）にだけ、そのチケットを載せなければならない。
- **フォームが計算する物**（`conformance.Worked`）：`vectors/worked/<名前>.jsonl` の各行を、版がベクタの日（2026-11-16、種類のオフセットで 10:00）に新しい申請が取る物なら、HTTP のハンドラーを通して `worked_<名前>` に渡し、ほかは、ページと同じく `Server.Worked` に渡す。どの答えも、行の `answer` と JSON として同じで、約束がその答えに与える形であること（`Package.CheckAnswer`）。種類が `Worked` を持つのに、そのベクタの無いパッケージは落ちる。
- **同時の操作**（`conformance.Concurrent`、`-race` を付けて回す）：一つのストアに二つのサーバー（二つのプロセス）を置き、同じチケットへの多くの操作を同時に送る。チケットが待っている人の承認（いくつかは一つのキーで、二つのサーバーへ二度送る）、コメント、取り下げ、時刻を進めて鳴らす時計、エージェントの答え（二つのサーバーのどちらにも渡す）である。そのあとで次を確かめる。(a) チケットごとに、動きを順に種類の入口へもう一度渡すと、ストアが持つチケットになる。(b) キーが同じすることは二つ無い。(c) 断った操作は何も変えていない。(d) 返事を返した操作は、動きとチケットとすることが保存されている。(e) 保存しなかった動きの知らせは出ていない。二つのサーバーは、二つのプロセスが一つのメールの仕組みを共有するように、一つの通知を共有する。
- **試し**（`conformance.Trials`。ストアは `TrialStore` であること）：イベントが人の操作であるベクタを一本ずつ、専用のサーバーで試し（`Try`）、それから行う（`Do`）。試しは、そのあと行った操作と同じように答え、何も残してはならない。チケット、その動き、時計とすること、その人のキー、帳簿の呼び出し、知らせ、監査ログが、試しの前と同じでなければならない。
- **待っているチケットの数**（`conformance.WaitingCounts`）：二つの種類のベクタのチケットを、まだ済んでいない部分の承認者を書き換えて、空にしたストアに入れる（`Import`）。ある人を待つ物、その人が代理する人を待つ物（一人はある日からある日まで、どの種類も。もう一人はその日から、一つの種類だけ）、その人と代理する人の両方を待つ物、ほかの人を待つ物である。そのあと二つを動かし、一つはほかの人を待つように、一つはだれも待たないようにする。代理の日の前、最初の日の朝早く、日のうち、最後の日の夜遅く（どちらも種類の UTC オフセットで）、日のあとで、次を確かめる。(a) ストアが `WaitingCounter` なら、人ごとのその数が、その人一人についてストアの `Waiting` が読む物を種類ごとに数えた物と同じである。(b) `Server.CountWaiting` が、`Server.Waiting` が返す物を種類ごとに数えた物と同じである。(c) 数が、試験に書いた数のとおりである。その人と代理する人の両方を待つチケットは一度だけ数える。日の外の代理、代理が挙げない種類、代わりに出せるだけの代理（`submit` の行）では数えない。動いたチケットは、今待っている人について数える。
- **待っているチケットの順**（`conformance.WaitingOrder`）：ベクタのチケットを、待ち始めた時刻と ID の順が食い違うようにして、空にしたストアに入れる（`Import`）。ステップにいる四件（まだ済んでいない部分の承認者を書き換えて一人を待つようにし、ステップが始まった時刻も書き換える。二件は同じ時刻）、申請者に戻った四件（四つの時刻に入れる。申請者に戻ったチケットは戻った時から待つので、ここでは入れた時から待つ。うち二件は 700 ナノ秒しか離れず、マイクロ秒までなら同じ時刻で、あとの方の ID が小さい）、だれも待たない一件である。そのあとで次を確かめる。(a) チケットごとの `TicketRecord.Since` が待ち始めた時刻をマイクロ秒に切った物で、だれも待たないチケットには無い。(b) `Store.Waiting` が、その時刻の新しい順、同じ時刻なら ID の大きい順に読む（上の「その人を待っているチケット」）。二人のそれぞれについても、二人一緒でも同じで、ページ（`Limit` と `Before`）と `Kind` で絞ったときも同じ順である。(c) `Server.Waiting` も、その順で返し、ページに分けても同じである。(d) 申請者に最初に戻ったチケットに、ベクタのコメントを、どの時刻よりもあとに API から渡す。コメントは受け付けられ、そのチケットの時刻も、(b) と (c) での場所も変わらない。(e) API の `list_waiting` が、その順でページに分けて返す。`limit` を 1 からチケットの数より一つ多い数まで変え、どのページも、続きがあるか（`more`）を言う。最後のページは、`limit` の数まで埋まっていても続きが無いと言う。同じ時刻のチケットは、ID の小さい方から入れる。ストアが入れた順に読むと、順が合わない。
- **自分の申請の順**（`conformance.MineOrder`）：ベクタの二つの種類のチケット 6 件を、一人の申請として、五つの時刻に入れる（二件は同じ時刻、二件は 400 ナノ秒しか離れず、マイクロ秒までなら同じ時刻で、あとの方の ID が小さい）。同じ時刻のチケットは ID の小さい方から入れる。ほかの人の申請も一件入れる。一人の申請のうち三件は、申請者に戻っている（二件は、自分の申請の無い人が差し戻した物。一件は、申請者が取り下げた物）。残りは済んでいる。そのままと、差し戻されたうちの一件を取り下げで終わらせた（状態を `withdrawn` にした）あとで、それぞれ次を確かめる。(a) `Store.Mine` が、出した時刻の新しい順、同じ時刻なら ID の大きい順に読み（上の「その人の申請」）、ページと、`Kind` や `Status` で絞ったときも同じである。ほかの人の申請は読まず、差し戻した人については何も読まない。(b) `Server.Mine` も、その順で返し、ページに分けても同じである。(c) API の `list_mine` が、上の (e) の `list_waiting` と同じように返す。(d) `Server.CountMine` が、試験に書いた数のとおりである。種類ごと、状態ごとの数と、差し戻された数である。`MineCounter` のストアの数も同じである。
- **リマインダーが鳴ったか**（`conformance.Reminded`）：種類のベクタが止めるステップのリマインダーを鳴らす時計のベクタを、サーバーに渡す（チケットの `reminded` は偽、答えるチケットでは真）。ストアは答えたチケットを、ステップの `reminded` を真のまま保つ。そのステップが待つ人が API からチケットを止め、1 分あとに再開する。ストアは API が答えたチケットをそれぞれ保ち、ステップの `reminded` は、入口が答えたとおり真のままである（鳴ったリマインダーは、再開しても鳴り直さない）。チケットを保つときか読むときにキーを落とすストアは、この試験に落ちる。
- **管理が残す物**（`conformance.Administration`。ベクタは要らない。13 章）：設定のパッケージと組織のサーバーに、管理の操作を役割で許す gate を付け、最初のパッケージの最初の種類で確かめる。(a) 版を、使わない、説明の始まりに戻す、同じ始まりにもう一度（記録は無い）、今すぐ、の順に、それぞれ読んだ番号で切り替える。前に読んだ番号は `stale`（409）になる。ストアは、番号を一つ上げた始まりと、それを決めた記録を持ち、新しい申請が取る版も、そのたびにそれに従う。種類のほかの版の始まり（`Change.Kept`）がストアの持つ物と違う変更は `ErrConflict` になり、何も書かない。(b) 切り替えの記録を、行が何か（`what`）で読める。1 から行の数より一つ多い数までのどの `limit` でも、新しい順のページで、同じ行が二度出ず、抜ける行も無い。(c) ソースのデータをそのまま確かめると、最新と同じだと言う。人を一人足したデータは、その差を持つ下書きになり、そのダイジェストで一度だけ取り込める（下書きのどの差でもないダイジェストは 400 で断る）。一つの最新に対して確かめた二つの下書きを同時に取り込むと、一つを受け付け、もう一つは `stale` になる。一人の 6 つ目の下書きは、その人のいちばん古い物を消し、片づけは 1 日を過ぎた下書きを消す。スナップショットを足す取り込みは、24 時間に `Config.ImportsPerDay`（ここでは 4）まで（越えれば 429）で、ストアもその数を守る（数えた数がそれぞれ少なかった二つのプロセスがするような、`ImportsMax` を越える変更は `ErrTooManyImports` になり、何も書かない）。どのデータの取り込みの記録にも数がある（`ChangesMax`。スナップショットを足しても足さなくても、越えれば `ErrTooManyImports`）。前のスナップショットの取り込みは、何も足さない。起動のとき、ソースから最後に取り込んでから変わっていないデータは取り込み直さず、新しいデータで同時に起動した二つのプロセスでは、一つが取り込む。ストアは、起動が読んだソースの最後の取り込みの記録の ID を、ロックの下で比べる（`Change.LastImport`。それより新しい物があったり、無いと読んだ所にあったりすれば `ErrConflict`）。(d) gate が許さない人は、何も変えず、何も読まず、アップロードも読まれない。(e) `Record` は始まりが指す記録を読み、ストアが持たない ID には `ErrNotFound` を返す。ストアが持つ ID の記録を含む変更はストアのエラーで、何も書かない（同じ ID の二つ目の記録も、始まりも、キーも書かない）。(f) ページの `before` は、`Line.ID` が書くとおりの ID で動きを指し、ほかの書き方（番号の前の 0、符号、小数、int64 を越える数、番号なし）では指さない。
- **ストアが持つ物**（`conformance.Keeping`。ベクタは要らない）：(a) 新しいチケットの動きを、持つ時刻を全部 UTC 以外のゾーン（ちょうどの時から 30 分ずれたゾーン）で、マイクロ秒より細かく渡して保存する。操作を始め、組織のスナップショットを保存し、アップロードを記録するのも、そのような時刻で渡す。読んで返る時刻は、どれも渡した時刻をマイクロ秒に切った物で、UTC である。設定がサーバーにファイルを与えるときは、ファイルの時刻も UTC で返る。(b) ある時刻より前に、それぞれ違う時刻で、返事を保管した操作を五つと、始めた操作を三つ置き、その時刻のあとにも一つずつ置く。上限を 2 にした `ForgetOperations` は、それぞれ古い物から二つずつ消して 4 を返す。続けて呼ぶと 3、1、0 を返し、その時刻のあとの物は残す。(c) (b) のどれよりも前に、返事を保管した操作を七つと、始めた操作を七つ置く。それぞれ六つは同じ時刻で、一つはその一分前である。消える順の逆に入れ、人とキーには、言語の照合順序と Go とで順が違う物を使う（`Zoe` と `ann`、`Key-2` と `key-1`、`fay` と `éva`）。上限を 2 にした `ForgetOperations` は、一分前の物を先に、そのあと同じ時刻の物を人、キーの順（Go が文字列を比べる順）に消し、4、4、4、2、0 を返す。(d) 一つのキーにつき一度だけ記録するアップロードを確かめる。一人の同じキーの二つ目は記録せず、一つ目の ID を返す。その人の上限を越えていても、一つ目を動きが持ったあとでも同じである。同じキーでも、ほかの人の物は別のアップロードで、キーの無い物は一つずつ記録する。同じキーで同時に上げた二十のうち、記録するのは一つで、上限が 1 でも無くても、二十の全部にその ID を返す。手放したアップロードは、キーから見つからず、同じキーの次の物を記録する。

例では、メモリのストアでも PostgreSQL でも、ベクタは全部合う。2,990 本のうち 2,990 本で、HTTP のハンドラーから 2,166 本、`Deliver` から、時計 626 本、人が書いてない操作 102 本（管理する人の `add_approver` と `hand_over`）、エージェントと flow の答え 82 本、API が 400 を返す本文 10 本（スキーマが必須にしている版を持たないイベント）、API に操作の無いイベント 4 本（直す操作の無い種類への `edit`）である。HTTP のハンドラーの 2,166 本の答えは、どれも約束の形に合う。`Can` は、1,542 のイベントについてベクタと同じことを言う（1,360 はできる、182 はできない）。人の操作 2,176 本（2,166 本と、400 を返す本文 10 本）は試しもかけ、どちらのストアでも、試しはどれも操作と同じように答える。ベクタが作らない読む操作の答えは、例の `TestAnswersAsTheContract` が約束に照らす。例の 24 人それぞれが出せる種類、その人を待っているチケットとその人の申請、チケットと、それを見られない人への答え、組織、どのマスタもである。

`WaitingCounts` は、`account` と `account_ja` のチケット 19 件を 6 人について入れ、五つの時刻で数を確かめる。メモリのストア、PostgreSQL、数えないストア（`Store` のメソッドだけを持つメモリのストア。サーバーがチケットを読んで数える）の三つでかけ、どれでも合う。`WaitingOrder` は、`account` と `account_ja` のチケット 9 件を 2 人について入れ、そのうち一件に `account_ja` のベクタのコメントを HTTP のハンドラーから渡す。順も時刻も、`list_waiting` のページも、メモリのストアと PostgreSQL のどちらでも合う。`MineOrder` は、`account` と `account_ja` のチケット 7 件（うち 6 件は一人の申請）を入れる。順も `list_mine` のページも数も、どちらでも合い、数えないストアでも合う。`Reminded` もどちらでも合う。リマインダーが鳴ったステップは、チケットを止めて再開しても `reminded` を持ち続ける。`Administration` も、例の最初の種類（`account`）で、どちらでも合う。`Keeping` は、メモリのストア（メモリのファイルと）でも PostgreSQL（そのファイルと）でも合う。

例では、ベクタを `web` が出す画面の上でも作る（`examples/web_vectors_test.go`。Node が要る）。人が画面で作る 2,048 行を、どれも HTTP で読んだ画面の上で作り、レンダラーが押すようにボタンを Node で押し、ボタンが送るメッセージをサーバーへ送り返す（差し戻されたチケットの破棄は、カードとフォームの両方から押すので、押した数は 2,136）。どちらのストアでも、サーバーに届いた 1,804 回は、ベクタのとおりに答える（受け付けが 1,664 回、断りが 140 回）。残りの 332 回はベクタが断る行で、画面が先に止めるか（ボタンが無くて 142 回、ボタン自身の確かめで 176 回、本人が開けない画面で 12 回）、行のとおりには送らない（2 回。下に書く）。画面にその行のボタンが無いのは、ベクタが 8.1 の理由で断るときで、そのときに限る（その人がだれかで 100 回、限りで 30 回、チケットの状態で 12 回）。前の版で作った行は、チケットが動く前に読んだ画面で押し、その画面にはボタンがある。画面が送らない 130 行（版の無いイベント、種類の画面に無いボタン、フォームに無いフィールドなど）も、どれもベクタが断る行である（`stale`、`event`、`form`）。

画面が隠すフィールドに値を入れる行は、次の三つが全部そろうときだけ、画面が送らない行と数え、サーバーへは送らない。ベクタがその行を、そのフィールドで断る（`form`）。そのフィールドは条件つきで見せる（`shown when`）。画面が送るアクションが、行からそのフィールドだけを除いた物と同じ（与えるフィールドと、空にするフィールド）。このとき画面は、条件のとおりにフィールドを隠していて、値を入れさせない。上の 2 回がこれで、`contract` と `contract_ja` のベクタの 225 行である。承認者が、`kind is not nda` のときだけ見せる `risks` に、秘密保持契約で値を入れる。screens のモジュールの `TestReplayVectors` も、同じように数える。

同じ行を、素の HTML のページの上でも作る（`examples/pages_test.go`。Node は要らない）。行ごとに HTTP でページを読み、行の値を入れ、ページのボタンで行を足したり消したりし（450 回）、ファイルを選んで、ブラウザと同じ形でフォームを送る。2,054 行で送った 2,142 回のうち、サーバーに届いた 1,752 回は、どちらのストアでもベクタのとおりに答える。1,664 回は受け付け（303）、88 回は断って、入れた値のままページをもう一度見せる。残りの 390 回はベクタが断る行で、ページが先に止める。ボタンが無くて 142 回（画面と同じ）、ページ自身の確かめで 146 回、ページを見せたあとにチケットが変わっていて 70 回、本人が開けないページで 12 回、ページが選ばせない値で 20 回（フォームが代わりに出させない人、申請者にだけ差し戻すカードでの差し戻し先のステップ、ページが隠しているフィールド）である。ページが送らない行も、どれもベクタが断る行である。

その人ができることは、種類の操作を全部かけても確かめる（`TestCanIsWhatIsTaken`。ベクタにあるのはそのうちのいくつかである）。ベクタのチケットがある 350 の場所それぞれで、チケットの人、その代理、ほかの一人に、すべてを許す gate でどのイベントも尋ねる（16,969 のイベント。承認者を足すことと部分の引き継ぎは、だれがしてよいかを gate だけが言うので、一人について尋ねる）。`Can` の答えにイベントがあるのは、そのイベントの操作のどれかが、試すと（`Try`）受け付けられるか、8.1 とは別の理由で断られるときで、そのときに限る。試す操作は 27,263 で、足す承認者と引き継ぐ先には組織の人を全部、引き継ぐ元にはステップの承認者を全部、差し戻し先にはステップを全部かける。

素の HTML のページのフォームを、スクリプトの無いブラウザが二度押したボタンを送るように送る（`examples/twice_test.go`。どちらのストアでも）。ファイルのある新しい申請、ファイルのある物と無い物のもう一度の申請、承認者の編集を、それぞれ同時に 5 回、続けて 2 回送り、あとの二つは、チケットが先へ進んだあとにも、もう一度送る。どの送信にも、同じチケットへの 303 を返す。増えるのはチケット一つか動き一つだけで、ファイルは一度だけ預かる。コメントか値が違う物、ほかの時刻に見せたページから送った物、ほかの人が送った物は、もう一度の申請と編集では `stale` で断る。ファイルが違うか、同じ中身でも名前かタイプが違う新しい申請は、別の申請になる。

自分のストアを確かめるには、空のストアを返す関数と、生成したパッケージを渡して、試験を呼ぶテストを書く（README を見てほしい）。

## 13. 管理

管理する人は、ほかの所で変えて確かめた物を反映する。種類の新しい版（ritsu が生成したパッケージで、サーバーは古い版と並べて受け持つ。2.1）と、組織のデータの新しい版である。反映する前に、ritsu のツールが何が変わるかを言い（種類の二つの版は `ritsu ringi diff`、組織の二つの版は `ritsu ringi diff --versions`）、変更を求めた人がそれを読んで、確かめたことを残す。フレームワークがするのは最後の一歩である。変更を反映し、その記録を残し、記録をチケットの動きと一緒に読ませる。だれが何をしてよいかは gate に尋ねる（13.4）。部署の管理する人が、その部署が持つ物だけを変え、読むようにするためである。

### 13.1 記録

管理する人がした変更は、どれも記録として残す。種類の版の切り替え（13.2）と、組織のデータの取り込み（13.3。起動のときの取り込みも）である。記録は、動きと同じく、変えることも消すこともない。記録は次の物を持つ。

- `id`：`Config.NewRecordID` が作る（無ければ、チケットの ID と同じく `NewID`）。空の ID と、`/` を含む ID は、サーバーの失敗である。ストアが持っている記録と同じ ID もサーバーの失敗で、ストアは変更を断り、何も残さない。
- `at`：作った時刻。`person`：作った人（サーバー自身の物、つまり起動のときの取り込みは `""`）。
- `via`：入ってきた入り口。`api`、`pages`、`start` のどれか。
- `what`：`kind`（種類とその版を持つ）か `organization`。
- `before` と `after`：前にあった物と、変わったあとの物（13.2、13.3）。
- `reason`：管理する人が書いたとおりの理由。500 字まで（`server.MaxReason`。CR LF で送った改行は一字と数える）で、改行のほかの制御文字、書式文字（Unicode の `Cf`。U+202E や U+2066〜U+2069 のように文の向きを変える字と、U+200B や U+FEFF のように幅の無い字）、行と段落の区切り（U+2028、U+2029）は持たない。書かなければ無い。ページも同じ表で理由を確かめる。

記録として読むのは、チケットの動きと、この記録である（13.5）。

### 13.2 申請の種類の版を切り替える

新しい申請は、その時刻に効いている版のうち、いちばん新しい版を取る（2.1）。版は始まりから効く。始まりは、ふだんは説明が書く日（`effective_from`）の、種類の UTC オフセットでの始まりで、書いていなければ初めからである。管理する人は、サーバーのパッケージにある版の始まりを、四つのどれかで決める（`start`）。

| `start` | 版の始まり |
|---|---|
| `"now"` | 操作の時刻 |
| 日付 `"YYYY-MM-DD"` | その日の、種類の UTC オフセットでの始まり。今日か、それよりあとの日 |
| `"description"` | 説明のとおりに戻す。決めたことを取り消す |
| `"never"` | 無し。新しい申請はこの版を取らない |

始まりは、その版を使える日（説明の `usable`）の中に限る。版のコードはその外の日の申請を断り、サーバーもその日には並べない（10.1）。そのため、版の最初の日より前の始まりと、最後の日よりあとの始まりは断る（400）。版は、説明が書く日より遅く始めることも、使わないこともできるが、早く始めることはできない。`effective_from` を書いていない版は、パッケージを出したときから取られる。管理する人に日を決めさせるには、説明に、始められるいちばん早い日を書く（`.ringi` のファイルの `effective from`）。

ほかは前と同じで、始まりがその時刻以前の版のうち、いちばん新しい版を取る。今すぐ始めるようにした古い版も、新しい版が効いているあいだは取らない。古い版に戻すには、新しい版を `never` か、あとの日にする。どの版も効いていないあいだは、種類は一覧に出ず、申請は 404 である（10.1）。チケットは、あとで何を決めても、出したときの版を終わりまで持つ（11 章）。切り替えは、チケットも時計もすることも変えない。

版の始まりは番号を持つ。何も決めていなければ 0 で、チケットの番号と同じく（4.1）、記録のたびに一つ上がる。切り替えは、決めたときに読んだ番号を言う（`number`。0 からの整数で、JSON の数の書き方ならどれでもよい。チケットの版の番号と同じく、`0`、`0.0`、`0e0` はどれも 0 である）。そのあとで始まりが変わっていれば、切り替えを `stale` で断り（409）、ページは今の始まりを見せる。番号を言わない切り替えは 400 である。言わなければ、その人が始まりを読んだあとにされた切り替えを、気づかずに戻してしまうからである。版が今持つ始まりへの切り替え（同じ形で、日付なら同じ日。`now` はいつも別の物）は、記録を残さず、何も変えない。ストアは、番号が読んだときのままのときだけ、切り替えを記録と一緒に、全部か何もなしかで保管する（13.8）。

プロセスは始まりをメモリに持ち、30 秒たったら読み直す。組織の最新のスナップショットと同じく（9.1）、操作の前と `Run` のループで読み直す。切り替えをしたプロセスは、すぐに読み直す。同じプロセスのほかの要求がそれより前に読み始め、あとで読み終えても、その読んだ始まりで置き換えない。そのため、どのプロセスも、記録から 30 秒のうちに切り替えを取る。ある日への切り替えは、その日の 30 秒以上前にしたなら、その日が始まったときに、どのプロセスも取る。`Server.Current` は、プロセスが最後に読んだ始まりから答える。

版の持ち主は、説明が挙げるチームである（`owner`。挙げなければ無し）。種類の持ち主は、今効いている版の持ち主である。どの版も効いていない間は、管理する人が `never` にした版と、後の日に始まるようにした版の持ち主が、種類を持つ。そのチームのどれかが効いている版を無くしたのかもしれず、どれがそうしたかは、始まりからは分からないからである。種類の一覧は、それが一つのチームなら種類の持ち主として挙げ、そうでなければ無し（null）とする。

### 13.3 組織のデータを取り込む

`ImportOrganization` は `Config.Organization` を読み、読んだ物をスナップショットとして保管する（9.1）。管理する人は、組織のデータを二つの段で取り込む。

1. **確かめる。** データを、今の `Config.Organization` から、または管理する人がアップロードしたアーカイブから読み、どの取り込みとも同じに確かめる（下の「確かめ」）。通ったデータは、その人の下書きとして保管し、最新のスナップショットとの差を、その差のダイジェストと一緒に返す。通らないデータには、その問題を返し、何も保管しない。
2. **取り込む。** 管理する人は、下書きと、読んだ差のダイジェストを言う。下書きが最新のスナップショットになるのは、最新が、差を作ったときのままのときだけである。そうでなければ取り込みを `stale` で断り（409）、下書きを、今の最新との差と一緒に見せ直す。下書きのどの差のダイジェストでもない物（今の最新との差でもなく、下書きのページが答えた、新しいほうから 100 件の取り込みの記録が最新だったときの差でもない物。`staleLook`）は、`stale` でなく読めない本文である（400）。サーバーの日に効く下書きのデータでは、その人が組織のデータを取り込めなくなるとき（その版で `import_organization` を gate に尋ねる。13.4）、下書きの答えがそれを言い（`locks_out`）、その取り込みは、本文に `"locks_out": true` があるときだけ受け付ける。なければ `locks_out` で断り（409）、何も保管しない。

ダイジェストは、最新のスナップショットの ID と下書きの ID から作る。どちらもデータのダイジェストなので（9.1）、二つで差が決まり、ダイジェストを言う取り込みは、見せた物を取り込む。最新と同じデータの下書きには、取り込む物が無い。取り込みは何も保管せず、記録も返さない。前のスナップショットと同じデータの下書きは、起動のときの取り込みと同じく、そのスナップショットをまた最新にする。前に保管したデータのスナップショットは足さない。ストアは、最新がサーバーの読んだときのままのときだけ、一つのロックの下で、スナップショットを記録と一緒に最新にする（13.8）。一つの最新に対して同時にした二つの取り込みは、一つが断られる。

**アップロード。** 種類の説明が挙げるプロジェクトのファイルを、説明が挙げるとおりの、プロジェクトの根（`ritsu gen` に `--root` で渡したディレクトリ）からのパスで入れた zip のアーカイブである。組織のディレクトリ（例では `org`）とその版と `proxies.csv`、種類が読むマスタのファイルである。どれも `ritsu ringi check` が確かめたファイルで、サーバーはファイルのソースと同じに読む（`server.ReadOrgFiles`。パッケージ `orgfiles` もこれを呼ぶ）。アーカイブを読む前に gate に尋ねる（13.4）。次のアーカイブは、どのファイルかを言って断る。

- zip のアーカイブでない。`Config.MaxOrganization`（無ければ 64 MiB）より大きい（413）。
- 中の名前が、アーカイブの中のパスでない（`..`、先頭の `/`、`\`、ドライブの名前、空の名前、二度ある名前）。リンクや、ファイルでもディレクトリでもない物を指す。読まないファイルを指す（組織のディレクトリとマスタのファイルの外のファイルや、組織のディレクトリの中の、版のファイルでも `proxies.csv` でもないファイル。ファイルマネージャーが作るアーカイブは、`__MACOSX/` や `.DS_Store` のような自分のファイルを持つことがある）。版のディレクトリでも、読むファイルへの途中のディレクトリでもないディレクトリを指す。
- ファイルが暗号化されている。deflate のほかの方式で圧縮されている（圧縮しないのはよい）。
- 項目のローカルヘッダーが読めないか、ローカルヘッダーのファイルの名前がセントラルディレクトリの名前と違う（その項目の問題として、セントラルディレクトリの名前の所で言う）。
- 項目が 10,000 より多い（`server.MaxOrgFiles`。どの項目も読む前に、アーカイブの終わりから、Go の `archive/zip` と同じ読み方でセントラルディレクトリをたどって数える。アーカイブのバイトは数えないので、ヘッダーのバイトを持つコメントやファイルは数に入らない）。一つのファイルか、全部のファイルが `Config.MaxOrganization` を越える。アーカイブが言う大きさでも、取り出したときの大きさでも数え、アーカイブが何と言っても、越えれば断る（413）。

**差。** どちらかのデータで版の日である日ごとに、最新のスナップショットでその日に効いている版と、下書きでその日に効いている版を比べる。新しい版を、その日に変わる物で言うためである。一つの日の中では、人を ID で（データが与えるどのフィールドも）、チームを ID で（名前、種類、親、長）、所属を人とチームで（役職と、主な所属か）比べる。どれも、足す、除く、変わるのどれかで、変わる物はフィールドごとに前と後を言う。代理は、人、代理人、種類、最初の日で比べ（最後の日と、挙げる申請の種類）、マスタの行は、キーと `valid_from` で比べる（ほかの列）。日ごとに、サーバーの日（13.5）で、過去か、今日効いているか、これからかを言う。

**効くとき。** どのプロセスも、30 秒のうちに新しい最新を取る（9.1）。取り込んだプロセスは、すぐに取る。それからは、サーバーがイベントのたびに読む組織は新しいデータである。効いている代理、チケットを見られる人（10.3）、gate が求める部署（`TeamOf`）、知らせの名前とアドレス、イベントに渡す組織の部分（9.2）である。進んでいるチケットは、ステップが始まったときに決まった承認者のままである。新しいデータが承認者に効くのは、次に始まるステップからである（どの申請者の承認者が変わるかは、ritsu の `ringi diff --versions` が言う）。動きは渡されたスナップショットを持つので、`Replay` は前と同じデータを渡す。種類を使える日はパッケージの物で（13.2）、もっと先まで届くデータでも延びない。データと一緒にパッケージを生成し直せば延びる。

**確かめ。** どの取り込みも同じに確かめる。起動のときも、ソースからでも、アップロードからでもである（`Server.CheckOrganization`）。見つけた問題は全部言う。ファイルから読んだデータならファイルと行を、ほかは版と行を付ける。次のデータは断る。

- 組織のディレクトリが無い、版が無い、版でないディレクトリがある（版は日付の名前 `YYYY-MM-DD` で、0001-01-01 から 9999-12-31 まで。名前がドットで始まるディレクトリと、版の横の `proxies.csv` のほかのファイルは読まない）。版に CSV のファイルと `organization.json` の両方がある、どちらも無い、CSV のファイルが一部しか無い。
- ファイルが UTF-8 でない（先頭のバイト順マークと、CR LF で終わる行は受ける）。CSV のファイルに要る列が無い、列が二度ある、そのファイルの物でない列がある、値の数の違う行がある、CSV として読めない。CSV の行（一行か、引用符の中の改行でまたがる行）が 1 MiB より長いか、値が 1,000 個より多い。行を値に読む前に断る。`organization.json` が読めない、最上位に `teams`、`people`、`members` のほかのキーがある（大文字小文字の違うキーも知らないキー）、またはその `teams`、`people`、`members` が、それぞれのキーのオブジェクトの配列（どのキーも一度で、値は文字列か null。`primary` は `true` と `false` も取る）でない。セルは、ritsu と同じく両端の空白を除いて読み、空の行は行にしない。
- 版の中で：ID の無い人やチーム、ID が二度ある人やチーム、名前の無い人。200 字より長い人やチームの名前。254 バイトより長いか、一つのアドレスの形でない人のアドレス（`email`。空でもよい。両端の空白を除いて、`@` がちょうど一つあってその前後に文字があり、空白と `"(),:;<>[\]` を含まない）。同じ版で、大文字小文字をたたんで比べると（アドレスでログインする確かめと同じ。`strings.EqualFold`）同じアドレスを持つ二人の人。組織が宣言しない種類のチーム、親がその版のチームでないチーム、長がそのチームに所属していないチーム。親をたどると下のチームに戻る、または 30 段より深い。人かチームがその版に無い所属、組織が宣言しない役職の所属、`primary` が `yes`、`no`、空のどれでもない所属（JSON では `true`、`false`、null も取る）。一人と一つのチームの所属が二つある。主な所属の無い人、主な所属が二つある人。
- 代理で：人か代理人が無い、自分が自分の代理である、種類が `approve` でも `submit` でもない、最初か最後の日が読めない、最後の日が最初の日より前、人か代理人が、その行のどれかの日に効いている組織の版にいない。
- 種類が読むマスタで：ファイルが無い。列が無い（optional なフィールドの列のほか。9.1）、二度ある、そのマスタの物でない（フィールドの列は、9.1 のとおり、フィールドの名前でも、`.ringi` のファイルがフィールドを書いた名前でも書ける。両方で書けば列が二度あることになり、問題は二つの名前を挙げる）。値の数の違う行、キーの無い行。読めない `valid_from` か `valid_to`、`valid_from` より前の `valid_to`。一つのキーの二つの行で、日が重なる。
- 255 バイトより長い ID（人、チーム、マスタの行の）。確かめが断る文字（`UnfitChar`。制御文字。書式文字＝Unicode の Cf：あとに続く文字の向きを変える U+202A から U+202E と U+2066 から U+2069、幅を持たない物や文字をつなぐ U+200B から U+200D と U+FEFF。行と段落の区切りの Zl と Zp、U+2028 と U+2029）か、UTF-8 でないバイトを持つ、ID、名前、人のアドレス、マスタの行の値。キーと日のほかの、マスタの行の値で、1,000 字より長い物。
- 今日（サーバーが受け持つ種類の版を使える最初の日のほうがあとなら、その日）から、その版を使える最後の日までに、組織の効いている版が無い日か、版が読むマスタに選べる行が無い日がある。種類のコードは、その日のどのイベントも断ってしまう。問題は、欠けている最初の日と、その日にそれが欠ける種類を全部言う。

チームの種類と所属の役職は、ritsu と同じく、組織が宣言する名前か、ファイルに書いた名前で読み、宣言した名前で持つ。ritsu がデータを経路と合わせて確かめること（だれも答えないステップ）は、ritsu の受け持ちである。`ritsu ringi check` を通ったデータは、これらの確かめも通る。起動のときに通らないデータは、サーバーを止める（2.5）。問題をそれぞれ一行で言い、`ritsu ringi check` でデータを確かめるように言う。

**起動のとき。** `ImportOrganization` がソースのデータを取り込むのは、ソースが前に取り込んだとき（ソースからの取り込みの最新の記録。起動のときか、管理する人による物）に返したデータと違うときだけで、そのときは人の無い記録を残す（`via: "start"`）。違わなければ、最新は、アップロードした物でも、そのままにする。比べるのも取り込むのもストアのロックの下でする。ストアは、ソースからの取り込みの最新の記録がサーバーの読んだときのままで（`Change.LastImport`。13.8）、かつ最新のスナップショットが読んだときのままのときだけ取り込むので、同時に起動した二つのプロセスのうち、取り込むのは一つである。ほかの取り込みの前に最後の取り込みを読んだプロセスが、同じデータをもう一度取り込むこともない。`ImportOrganization` を自分で呼ぶプロジェクト（2.2）も同じである。

**下書き。** 下書きは、確かめた管理する人の物で、ほかのだれも読めない。`Config.DraftsKept`（無ければ 1 日）のあいだ、一人に 5 つまで持ち（6 つ目は、その人のいちばん古い物を消す）、取り込めば消す。スナップショットと同じく、全員の名前とアドレスを持つ。

**回数。** スナップショットを足す取り込みは、24 時間に `Config.ImportsPerDay` まで受け付ける（無ければ 20。負の数なら制限しない）。どのプロセスも同じに数えるように、記録から数える。どのデータの取り込みでも（スナップショットを足す物と、最新の前のスナップショットをまた最新にする物）、その 10 倍までで（`changesPerImport`）、ストアもそのロックの下で守る（`Change.ChangesMax`。13.8）。どちらかを越えた取り込みには 429 を返し、`Retry-After` に、そのうちいちばん古い物が 24 時間前になるまでの秒数を書き、何も保管しない。確かめは数えない。プロセスは、確かめを同時に 2 件までしかしない（`checksAtOnce`。どれもデータをメモリに読んで確かめる）。それを越える確かめは 429 と `Retry-After: 1` を返す。API ではアップロードを読む前に、ページでは読んだあとに断る（13.7）。

### 13.4 gate

| 動作 | 対象 | 尋ねるとき |
|---|---|---|
| `change_request_type` | 種類の版：`Kind`、`Version`、`Owner`（版の持ち主） | 種類を並べる前に、版ごとに（13.6）。版を切り替える前に、版の持ち主で尋ね、種類の持ち主が別のチームなら、それでも尋ねる。切り替えを読んだあと、それが種類を移す元か先のほかの持ち主それぞれでも尋ねる（下） |
| `read_audit` | 種類の版：`Kind`、`Version`、`Owner`（版の持ち主） | 記録のページを読む前に、どの種類のどの版にも一度ずつ。種類を並べる前と、切り替えの答えを作る前に、並べる種類の版ごとに（13.6） |
| `import_organization` | 組織 | 組織のページ、確かめ、下書き、取り込みの前。アップロードを読む前 |
| `read_organization_audit` | 組織 | 記録のページを読む前に一度 |

問いは、`Action`、`Person`（ログインで分かった役割を持つ）、`Kind`、`Version`、`Owner`、`Now`、`Org` を持つ。`Org` は今日効いている組織の版で（種類なら種類のオフセットでの日、組織ならサーバーの日で読む）、人の部署を求める gate のためにある（`TeamOf`）。組織についての問いは種類を持たない。ほかの値は空である。

種類をあるチームから別のチームへ移す切り替えは、gate が両方を許すときだけする。切り替えを読む前に、版の持ち主と、今の種類の持ち主で gate に尋ねる。読んだあと、サーバーは、切り替えによって新しい申請が取る版が変わる時を、今と、今より後の始まりそれぞれについて調べる。始まりは、プロセスが最後に読んだ物でなく、切り替えのためにストアから読み直した物を使う。その時ごとに、種類を移す元の版の持ち主（効いている版が無かった所では、そのときに種類を持つチームそれぞれ。13.2）と、移す先の版の持ち主で gate に尋ねる（切り替えで効いている版が無くなる所では、移す先は無い。そのときに種類を取るチームは無いからである）。こうして、どのチームも、ほかのチームについての許しなしには、自分の版を止めて種類をほかのチームの版に戻すことも、ほかのチームが止めた種類を取ることも、後の日にほかのチームの版の前へ自分の版を置くこともできない。gate が版の持ち主では許し、そのようなほかの持ち主では許さない切り替えは、`owner` で断る（409）。いつから、どのチームからどのチームへ種類を移すか、ほかにだれについて許しが要るかを言い、何も保管しない。ストアは、種類のほかの版の始まりが、サーバーが読んだときのままのときだけ、切り替えを保管する（`Change.Kept`。そうでなければ `stale`）。記録の行は、版ごとに、gate がその版の持ち主で許す人が読む。部署は、自分が持っていた版の行を読む。

gate が許さない管理する人には、どの入り口でも、無い物と同じに答える（404）。切り替えられない版は、切り替える方法なしに並び、その切り替えは見つからない。どの版も切り替えられない種類は並ばない。組織のページ、下書き、取り込みは見つからず、アップロードは読まない。読めない行は読まない。その人が送った物は何も変えず、読めない物は、答えにも、数にも、`more` にも入らない。並ぶ版のうち行を読めない版（13.6）は、始まりを決めた記録とチケットの数を null にし（`may_read` が false）、チケットも数えない。

エラーを返す gate は、どの操作とも同じく、サーバーの失敗（500）である（8 章）。違うのは一か所で、ホームが管理へリンクするかを尋ねる所（13.7）では、エラーをログに書いてリンクを出さない。これらの動作を持たない gate のサーバーでも、ホームが出るようにするためである。

`server.AllowAll` は四つとも許す。これを使えば、だれでもどのチケットも見られるのと同じく（10.3）、だれでも全部を管理する。

例の gate は、役割 `staff`、`workflow_admin`（自分の部署を管理する人）、`system_admin`（全部を管理する人）、`auditor`（どの記録も読み、何も変えない）を宣言する。部署を管理する人は、自分の部署が持つ版を切り替え、その行を読む。`staff` の人は、どちらもしない。役割はログインから来る。プロバイダーのクレーム（`oidc.Roles`）、プロキシのヘッダー（`web.ProxyHeader`）、手元のコンピューターでは `-dev-login` のページとヘッダー `X-Roles` である。sekisho が生成した gate は、宣言していない役割を持つ人を、何を尋ねても断る。そこで例の gate は、宣言した役割だけを添えて尋ね、ほかの役割は、それぞれ一度だけログに書く（ログに書く名前は 100 まで）。役割が一つも残らない人は、`staff` として尋ねる。版の持ち主と人の部署を比べる gate は、組織に見つからない人も、持ち主の無い版も、どこかの部署の物として扱ってはいけない。例の gate は、その版で主な所属の無い人を読まない。持ち主の無い種類には、その版のどのチームも持たない ID を持ち主として渡し、部署を読まない規則（システムを管理する人の規則）のほかは、その種類について何も許さないようにする。効いている組織の版が無い間は、例の gate はだれも見つけないので、管理の画面からはだれも組織を取り込めない。そのときは、gate に尋ねない起動のときの取り込み（13.3）がデータを入れる。

### 13.5 記録を読む

記録は、新しい順に一ページずつ読む。チケットの動き（チケットを動かした動きのどれも。時計やエージェントの答えのような、サーバー自身の物も含む。そのままの形で入れたチケットは、イベントの無い行になる）と、13.1 の記録である。断った操作は何も動かさないので、行にならない。`Config.AuditLog` を渡す配置では、断りはそちらにある（2.3）。同じ時刻の行は、チケットの ID か記録の ID の大きい順に、次に動きの番号の大きい順に並べる（Go が文字列を比べる順。バイトの順）。クエリで絞れる。

| クエリ | 残す物 |
|---|---|
| `from`、`to` | その日々の行。両端の日を含み、サーバーの日で読む。`YYYY-MM-DD` |
| `kind` | 一つの種類の行。そのチケットの動きと、その版の切り替え（サーバーが受け持たない種類は、10.4 と同じく 400） |
| `what` | `move`、`kind`、`organization` のどれか |
| `person` | 一人の行。チケットを動かした人、記録を作った人 |
| `ticket` | 一つのチケットの動き |
| `limit`、`before` | ページ。1 から 100 行（無ければ 50）で、`before` が言う行（その `id`）の次から |

サーバーの日は、最初の種類の UTC オフセットでの日で、今日の人、チーム、マスタを読む日である（9.1）。

ページを読む前に、サーバーが受け持つ種類の版ごとに `read_audit` を、それと `read_organization_audit` を、gate に尋ねる（13.4）。ストアは、許された版と組織の行だけを読む。許されない行は、ページにも、`more` の数にも入らず、`before` からも見えない。そういう行や、クエリが残さない行を言う `before` は、一覧に無いチケットと同じく、空のページになる（10.2）。どの版も許されない `kind` も、空のページになる。行の ID の書き方でない `before`（動きの番号を Go が整数を書くとおりに書いていない物。`T/01`、`T/+1`、`T/1.0` はどの行も言わない）も空のページで、サーバーはストアに聞かずに答え、ストアも同じに答える（`conformance.Administration`）。ストアは、`before` が言う行の時刻を、キーだけで引いて読む。

動きの行は、チケット、種類と版、番号、動かした人と入り口（`via`）、イベントの名前、動きが作った履歴の行（`get_ticket` が答えるとおり：したこと、ステップ、だれが、だれの代わりに、コメント、注記）、動きが持つとおりのイベント（フォームの値も含む）、渡された組織のスナップショットを持つ。`read_audit` は、その版のどの動きも、フォームも含めて読ませるので、gate はそれを読んでよい人に与える。切り替えの行は、前と後の始まりとその番号を持つ。取り込みの行は、ソース、前と後のスナップショット、部分ごとに足した数、除いた数、変わった数を持つ。

同じ行を CSV でも返す。動きが作った履歴の行ごとに一行（何も作らなかった動きは一行）、記録ごとに一行である。一つの答えは 10,000 行と 16 MiB までで、一つの動きの行は分けない。続きがあれば、次の部分へのリンク（`Link: <…>; rel="next"`）を付ける。先頭はバイト順マークで、`=`、`+`、`-`、`@`、タブか CR で始まるセルは、表計算ソフトが式として読まないように、前に `'` を付けて書く。フォームは入れない。列は決まっていて、足すときは終わりにだけ足す。`at`、`line`、`what`、`ticket`、`kind`、`version`、`number`、`person`、`via`、`event`、`did`、`stage`、`by`、`proxy_of`、`to`、`comment`、`note`、`before`、`after`、`reason` である。時刻は、サーバーの日のオフセットでの RFC 3339 である。

### 13.6 API

管理の操作は、約束の物ではなく、フレームワーク自身の物である。`openapi.json` にはどれも無い（`Load` は、フレームワークが受け持たない操作を受け付けない。2.1）。API とページが同じ呼び出しをするように、`Do` が operationId で動かす。HTTP のハンドラーは、アップロードのほかは、約束の操作と同じように出す（人を求め、本文は `application/json` で `Config.MaxBody` まで、POST ごとに `Idempotency-Key`。7.1、7.3）。エージェントのツールには、どれも無い。

| メソッド | パス | 操作 | すること |
|---|---|---|---|
| GET | `/admin/kinds` | `admin_list_kinds` | その人が切り替えられる版のある種類。版ごとに、使える日、始まり、チケット |
| POST | `/admin/kinds/{kind}/versions/{version}` | `admin_switch_kind` | 版を切り替える（13.2） |
| GET | `/admin/organization` | `admin_get_organization` | 最新のスナップショットのあらましと、サーバーにソースがあるか |
| POST | `/admin/organization/drafts` | `admin_check_organization` | ソースかアップロードを読んで確かめ、下書きを保管し、その差を返す（13.3） |
| GET | `/admin/organization/drafts/{id}` | `admin_get_draft` | その人の下書きと、今の最新との差 |
| POST | `/admin/organization/imports` | `admin_import_organization` | 下書きを取り込む（13.3） |
| GET | `/admin/records` | `admin_list_records` | 記録の一ページ（13.5） |
| GET | `/admin/records.csv` | `admin_records_csv` | 同じ行の CSV（13.5） |

種類の一覧：

```
{"kinds": [{"name": "leave", "title": "Leave request", "owner": "hr", "current": 1,
  "versions": [
    {"version": 1, "owner": "hr", "effective_from": null,
     "usable": {"from": "2026-04-01", "through": null}, "may_switch": true, "may_read": true,
     "start": {"how": "description", "at": null, "day": null, "number": 0, "record": null},
     "tickets": {"open": 3, "all": 120}},
    {"version": 2, "owner": "hr", "effective_from": "2027-04-01",
     "usable": {"from": "2027-04-01", "through": null}, "may_switch": true, "may_read": true,
     "start": {"how": "day", "at": 1809097200, "day": "2027-05-01", "number": 1,
               "record": {"id": "01KA…", "at": 1794961800, "person": "e6001", "reason": "…"}},
     "tickets": {"open": 0, "all": 0}}]}]}
```

`current` は新しい申請が今取る版（無ければ null）、`owner` は種類の持ち主である（13.2）。版の `at` は始まりの Unix 秒（`never` と、日を書かない説明（初めから）では null）、`day` はその始まりの、種類のオフセットでの日である。`may_switch` は、gate がその人にその版の切り替えを許すかを言い、`may_read` は、その版の記録の行を読むのを許すか（`read_audit`。13.4）を言う。読めない版では、`start.record` と `tickets` は null である。`start.record` は始まりを決めた記録（その `number` を作った記録）で、記録が決めたのでない始まりでは null である。`open` はその版のチケットのうち終わっていない物（`in_progress`、`paused`、`with_requester`）の数、`all` はその版のチケットの全部の数である。

切り替え：`POST /admin/kinds/leave/versions/2` に、次の本文を送る。

```
{"start": "2027-05-01", "reason": "Start with the payroll month", "number": 0}
```

`reason` は、無くても null でもよい。`number` は要る。答えは `{"record": {…}, "kind": {…}}` で、記録の行としての記録（13.5。何も変わらなければ null）と、一覧のとおりの種類である。

確かめ：`POST /admin/organization/drafts` に、`{"source": "config"}` を `application/json` で、またはアーカイブを `application/zip` で送る。答えは下書きである。

```
{"draft": "3f2a…", "latest": "9c1b…", "digest": "5e7d…", "source": "upload", "same": false,
 "locks_out": false,
 "days": [{"day": "2026-04-01", "when": "now", "change": "changed",
           "people": {"added": […], "removed": […],
                      "changed": [{"key": "e1203", "before": {…}, "after": {…}}]},
           "teams": {…}, "members": {…}},
          {"day": "2027-04-01", "when": "later", "change": "added", …}],
 "proxies": {"added": […], "removed": […], "changed": […]},
 "masters": [{"master": "company_masters.systems", "added": […], "removed": […], "changed": […]}],
 "counts": {"people": {"added": 1, "removed": 0, "changed": 2}, "teams": {…}, "members": {…},
            "proxies": {…}, "rows": {…}}}
```

ストアにスナップショットが無ければ、`latest` は null である。`locks_out` は、今日効く下書きのデータでその人が取り込めなくなるかを言う（13.3）。変わった行はそれぞれ `key` で言う。キーが一つの値（人やチームの ID）ならその値で、二つ以上の値ならその値の JSON の配列を文字列の中に入れて言う（所属なら `"[\"e1201\",\"sales1\"]"`。代理の四つ、マスタの行のキーと最初の日も同じ）。どの二つのキーにもならない書き方である。一つのキーの行は多重集合として数えるので、取り込みが足し、除き、変える行はどれも差に出る。通らないデータには、その問題を、それぞれファイルと行を付けて、英語と日本語で返す。先頭の 100 件（`ShownProblems`）と、あと何件見つかったか（`more`）、1,000 件で確かめを止めたか（`stopped`。ほかにあるかもしれない）である。

```
{"error": "the data does not pass its checks",
 "problems": [{"at": "org/2027-04-01/members.csv:4", "text": "…", "text_ja": "…"}, …],
 "more": 17, "stopped": false}
```

取り込み：`POST /admin/organization/imports` に `{"draft": "3f2a…", "digest": "5e7d…", "reason": …, "locks_out": true}` を送る。`reason` は、無くても null でもよい。`locks_out` を true にするのは、答えがそう言う下書きのときだけ要る（13.3）。答えは `{"record": {…}, "organization": {…}}` で、記録の行（何も変わらなければ null）と、`GET /admin/organization` が答えるとおりの組織である。

| 起きたこと | HTTP | 本文 |
|---|---|---|
| した、または読んだ | 200 | 上の答え |
| 読めない本文やクエリ：空の本文、JSON のオブジェクトでないか UTF-8 でない本文、JSON の値が二つあるか値のあとに何かある本文、操作が取らないキー（大文字と小文字が操作のキーと違うキーも）を持つか同じキーを二度持つ本文。四つのどれでもなく日付でもない `start`、種類のオフセットで今日より前の日。長すぎる `reason`、制御文字、書式文字、行か段落の区切りを持つ `reason`（13.1）。無いか、0 からの整数でない `number`。`config` でない `source`。`draft` か `digest` が無い。範囲の外の `limit`、1〜255 バイトの制御文字の無い文字列でない、クエリの `person`、`ticket`、`before`、読めない絞り込みの日、三つのどれでもない `what`、サーバーが受け持たない `kind`。キーの無い POST、キーが 1〜255 バイトの表示できる ASCII でないか、別の操作に使われていた POST | 400 | `{"error": …}` |
| 版を使える日の外の始まり | 400 | `{"error": …}`。版が持つとおりに、最初の日か、最後の日か、その両方を言う |
| 通らないデータ、断ったアーカイブ（13.3） | 400 | `{"error": …, "problems": […]}` |
| gate が許さない物。その種類、パッケージにあるその版、その人のその下書きが無い。`Config.Organization` の無いサーバーでのソースの確かめ | 404 | `{"error": …}` |
| 版の始まりが `number` のあとで変わったか、切り替えを決めている間に種類のほかの版の始まりが変わった。最新のスナップショットがダイジェストのあとで変わった。そのダイジェストが、下書きのページがそれより前の最新に対して答えた物であるとき（13.3） | 409 | `{"refused": "stale", "text": …, "text_ja": …, "at": null}` |
| 下書きのどの差のダイジェストでもない物（13.3） | 400 | `{"error": …}` |
| gate が版の持ち主では許し、ほかの持ち主では許さない人の、種類をほかのチームへ移す切り替え（13.4） | 409 | `{"refused": "owner", "text": …, "text_ja": …, "at": null}` |
| 取り込むとその人が取り込めなくなるデータの下書きを、`locks_out: true` なしに取り込む（13.3） | 409 | `{"refused": "locks_out", "text": …, "text_ja": …, "at": null}` |
| `Config.MaxBody` より大きい本文。`Config.MaxOrganization` より大きいアーカイブ、ファイルが多すぎるアーカイブ、取り出すと大きすぎるファイル（13.3） | 413 | `{"error": …}` |
| `application/json` で送っていない本文（確かめは、`application/json` か `application/zip`） | 415 | `{"error": …}` |
| その日の `Config.ImportsPerDay` か、どのデータでもその 10 倍を越えた取り込み。プロセスが二つ確かめている間の確かめ（13.3） | 429 | `{"error": …}`。`Retry-After` を付ける |
| 人がいない | 401 | `{"error": …}` |
| サーバーの失敗（失敗したストアや gate） | 500 | `{"error": "the server failed"}`。失敗はログに書く |

操作のキーは、約束の操作と同じく、返事と一緒に保管する（`operations`、4.4）。同じキーには同じに答え、もう何も変えない。確かめのためにソースを読んだときの、ソース自身のエラーは、起動のときの `ImportOrganization` と同じく（2.5）、データの問題として言う（400）。

### 13.7 ページ

`web` は、管理のページを `/pages/admin/` の下で、ほかのページと同じ守りの後ろで出す（`web` の DESIGN.md の 3 章：人、ほかのサイトから送られた物を断ること、大きさ、ヘッダー）。ページは、パッケージ `screens/page` がホームと並べて作り、言葉は英語と日本語である。ページは API と同じ操作をし（`Do`、`Via: "pages"`）、ほかのページと同じく、選んだ言語で見せる（`web` の DESIGN.md の 9.4）。最初のページのほかは、頭の下に、最初のページへ戻るリンクを置く。スクリプトは要らない。

| ページ | 見せる物と、送ったとき |
|---|---|
| `/pages/admin/` | その人が使える部分：種類、組織、記録。どれも使えなければ「見つかりません」（404） |
| `/pages/admin/kinds` | その人が切り替えられる版のある種類ごとに、その版を見せる。版ごとに、使える日、始まりとそれを決めた人（説明か、記録のだれが、いつ、なぜ）、チケットの進行中の数と全部の数（その人が行を読めない版では「—」で、記録も見せない）。新しい申請が今使う版。切り替えられる版ごとのフォーム（今すぐ、日付から、説明のとおり、使わない、理由、ボタン）。送ると 303 でこのページに戻り、したことを言う。`stale` は、今の形でページを見せ直し、頭に断りを書く（409） |
| `/pages/admin/organization` | 最新のスナップショット：いつ、だれが取り込んだか、版の日（過去、効いている、これから）、版ごとの人とチームの数。ソースのデータを確かめるボタン（サーバーにソースがあるとき）と、アップロードするファイル（`Config.MaxOrganization` までのファイルを持つフォームで送る）。確かめると 303 で下書きのページへ行く。問題のあるデータは、先頭の 100 件をそれぞれファイルと行を付けて、あと何件あるかと、確かめを止めたかを添えてページの言語で書き、ページを見せ直す（422。13.3）。プロセスがほかの二つを確かめている間の確かめは 429 で、あとでもう一度と言う |
| `/pages/admin/organization/drafts/{id}` | 日ごとの差（13.3）と、代理とマスタ、それぞれの数。理由と、取り込むボタン。取り込むとその人が取り込めなくなるデータの下書きは、注意を出し、必須のチェックボックス（`locks_out=yes`）でそれを知っていることを確かめる。チェックがなければ取り込みを断る（`locks_out`）。取り込むと 303 で組織のページへ行き、したことを言う。`stale` は、今の最新との差で下書きを見せ直し、確かめたあとに組織が変わったと言う。最新と同じデータの下書きは、違う所が無いと言い、ボタンを出さない |
| `/pages/admin/records` | 13.5 の絞り込み（GET で送るフォーム）、行（1 ページに 50 行）、もっと前の行へのリンク、同じ行を CSV で落とすリンク |

ホーム（`/pages/`）は、その人が部分のどれかを使えるとき、`/pages/admin/` へリンクする（`Server.Administers`。13.4 と同じく gate に尋ね、最初に許されたところで止まる。ここでの gate のエラーは、ログに書いてリンクを出さない）。ページは、データのどの名前も、記録の行のどの値も、自分の向きを持つ要素（`<bdi>`）の中で言う。あとに続く文字の向きを変える文字を持つ名前が、ページの文の向きを変えないためである。送るときのキーは、ほかのページと同じに作る（ページ、その値、ページを見せた時刻から。`screens/page` の DESIGN.md の 4.5）ので、二度送ったページは一つの操作になる。

ページは、人が自分のフォームで間違えうることを、操作を送る前に確かめ、ページの言語で、見せ直したページで言う（422）。`YYYY-MM-DD` と読めない日（日付の入力の無いブラウザは、打ったままを送る）、今日より前の日、版を使えない日（今すぐなら今日）、500 字より長い理由と、残せない文字（貼り付けたタブや、文の向きを変える字。13.1 の表）を持つ理由、選んでいないファイル、サーバーが受け取るより大きいファイル、その日の数を越えた取り込み（429。待つ分数を言う）である。切り替えのフォームの日は、ブラウザが、版を始められる最初の日（今日か、使える最初の日のほうがあとならその日）から最後の日までに限る。その人が切り替えられない版は、送った物を読む前に、見つからない（404）。フォームが作らない投稿（番号が無い、フォームに無い始まり）は、「送った内容を読めませんでした」（400）である。最後に決めるのはサーバー自身の確かめで、フォームが作らない物は、サーバーの文で、英語で言う。

ページは、見せない物を言う。版が何を変えるか、組織の新しいデータでだれが承認者になるかは、ritsu の `ringi diff` が言う。ページは、サーバーの外へはリンクしない。

### 13.8 ストア

ストアは、記録を、それが変える物と一緒に、一度の書き込みで、全部か何もなしかで保管する。

- `SaveChange(ctx, c *Change)`：記録を書く。切り替えなら、版の始まりを、番号が読んだときのままのときだけ（違えば `ErrConflict`）書き、番号を一つ上げる。種類のほかの版の始まり（切り替えが読んだときの `Kept`）の番号も、それぞれそのままのときだけである（違えば `ErrConflict`）。取り込みなら、スナップショットを、最新が読んだときのままのときだけ（違えば `ErrConflict`）、かつ `LastImport` が nil でなければ、ソースからの取り込みの最新の記録の ID がその言う物のときだけ（無ければ `""`。違えば `ErrConflict`）、どの「最新にする」も取るロック（`SaveOrganization` も取る）の下で、最新にし、その人の下書きを消す。スナップショットを足す取り込みは、`ImportsSince` からのスナップショットを足した取り込みが `ImportsMax` より少ないときだけ、どの取り込みでも、`ImportsSince` からの取り込みの記録（スナップショットを足したかにかかわらない）が `ChangesMax` より少ないときだけである（そうでなければ `ErrTooManyImports`。`ImportsMax` と `ChangesMax` が 0 以下なら数で限らない）。操作のキーと返事は、`Save` と同じく、その人のそのキーに返事がまだ無いときだけ書く（あれば `ErrKeyTaken`）。ストアがすでに持っている ID の記録は断り、何も書かない。
- `Starts(ctx)`：管理する人が決めた、版ごとの始まり（それぞれの最新の記録の物）。
- `Record(ctx, id)`：ID で引いた記録一つ（無ければ `ErrNotFound`）。始まりを決めた記録を読む。
- `Records(ctx, q)`：記録の一ページ（13.5）。`q` が許す版と組織の物。
- `CountTickets(ctx, versions)`：渡した版ごと、状態ごとのチケットの数。渡すのは、その人が行を読める版である。
- `Imports(ctx, since)`：ある時刻からの、スナップショットを足した取り込みの時刻。`LastImport(ctx)`：ソースからの最新の取り込み。
- `KeepDraft(ctx, d, max)`、`Draft(ctx, person, id)`、`ForgetDrafts(ctx, before, limit)`。

PostgreSQL の表は、これまでの手順に続く二つの手順で足す（4.3）。

| 表 | 持つ物 | キー |
|---|---|---|
| `records` | 13.1 の記録：時刻、人、入り口、何の記録か、種類と版、前と後（JSON）、理由。取り込みなら、そのソース、スナップショット、スナップショットを足したか | `id` |
| `starts` | 管理する人が決めた、版ごとの始まり：形、時刻、日、番号、それを決めた記録 | `kind`、`version` |
| `drafts` | 下書き：だれの物か、データ（JSON）、ソース、時刻 | `person`、`id` |
| `moves`（列） | 動きごとの `kind`、`version`、`via`。記録の行を版と入り口で読むため | |

一つ目の手順は、これらと、記録の順のインデックスを足す（`moves` を時刻、チケット、番号で。種類と版、続けて同じ順で。人、続けて同じ順で。`records` を時刻と ID で。種類と版で。スナップショットを足した取り込みを時刻で）。数のために、`tickets` を種類、版、状態で引くインデックスも足す。二つ目の手順は行を書き換える（それだけで一つの手順。4.3）。それより前に保管した動きのそれぞれに、そのチケットの種類と版を入れる。入り口は `""` のままである。

どちらのストアも同じに答える。

- 時刻はマイクロ秒に切り、UTC で持つ（4.1）。記録の時刻も動きと同じで、比べるために渡す時刻も同じに切る。
- 記録の順は、新しい順、次にチケットか記録の ID の大きい順（Go が文字列を比べる順。`collate "C"`）、次に動きの番号の大きい順である。
- クエリが残す行のどれも言わない `before` は、空のページになる。
- 版の最新の記録は、その始まりが言う記録である。始まりの番号は、記録のたびに一つ上がる。
- 最新のスナップショットは、読んだ物を確かめながら、ロックの下で最新にする。PostgreSQL ではトランザクションの間だけ効くアドバイザリーロックで、`SaveOrganization` と起動のときの取り込みも、これを取る。メモリでは、ストアのロックである。
- 一人の 6 つ目の下書きは、その人の下書きのうち、保管した時刻、次に ID の順でいちばん前の物を消す。
- `ForgetDrafts` は、`ForgetOperations` と同じく（4.4）、古い物から千件ずつ消す。

適合の試験 `Administration`（13.10）が、ストアをこの全部に照らして確かめる。

### 13.9 サーバーの時計と片づけ

サーバーの時計（`Config.Clock`）が、次の物を決める。`now` への切り替えの時刻、日付への切り替えを限る今日（種類のオフセットで）、始まりを限る日（13.2）、記録と下書きの時刻、`ImportsPerDay` の 24 時間、記録の絞り込みの日である。プロセスは、組織と同じく（9.1）、この時計で 30 秒ごとに始まりを読み直す。切り替えには、チケットの時計（5 章）を掛けない。始まりは時刻で、新しい申請のたびに、その時刻と比べる。

`Sweep` は、`Config.DraftsKept` を過ぎた下書きを、千件ずつ消す（負の長さなら、取り込むか、置き換わるまで持つ）。記録、始まり、スナップショットは残す。管理の操作のキーは、ほかの操作のキーと一緒に消す（`Config.OperationsKept`。4.4）。

### 13.10 守りと試験

管理も、ほかの部分と同じ守りを持つ。

- **読めない物。** どの答えも、gate がその人に許す物だけを持つ（13.4）。切り替えられない版を切り替える方法も、ほかの人の下書きも、読めない版の行も、ページにも、数にも、`more` にも、CSV にも入らない。記録の行はイベントを、フォームも含めて持つので、`read_audit` は、チケットを読むことと同じように与える。
- **ブラウザが送る物。** ページは `web` の守りの後ろにある。API は本文を `application/json` で、アップロードを `application/zip` で受け、どちらもほかのサイトのフォームは送れない。どの POST にもキーが要り、そういうフォームはそれも送れない。
- **だれがログインするか。** 管理する人にする役割は、ログインが確かめた物から来る。だれも確かめない二つのログイン（`devlogin`）は、これまでと同じく、サーバーが動くコンピューターでだけ答える。プロキシの三つのヘッダーは、それぞれ一つだけ受ける（`web.ProxyHeader`）。
- **大きさ。** 本文は `Config.MaxBody` まで。アップロードと、読んだそのファイルは `Config.MaxOrganization` と 10,000 項目まで。理由は 500 字、ページは 100 行、CSV は 10,000 行と 16 MiB、下書きは一人に 5 つと 1 日、スナップショットを足す取り込みは `Config.ImportsPerDay` まで、どのデータの取り込みの記録もその 10 倍まで（`changesPerImport`。ストアも守る）。組織のデータの確かめは、一つのプロセスで同時に 2 件まで（`checksAtOnce`）。組織のデータ自身は（13.3）、名前は 200 字、人のアドレスは 254 バイト、ID は 255 バイト、マスタの行のキーと日のほかの値は 1,000 字、CSV の行は 1 MiB と 1,000 個の値まで。答える問題は、多くても 1,000 件見つけるうちの先頭の 100 件（`ShownProblems`、`MaxOrgProblems`）。人、チケット、行を言うクエリの値は、1〜255 バイトの、制御文字の無い文字列である。日は 0001-01-01 から 9999-12-31 まで読む（ほかは 400）。
- **gate より先に読む物。** 無い。アップロードは、API でもページでも、gate がその人に取り込みを許してから読む。
- **一つのチームだけでできる物。** ほかのチームの種類を動かすことはできない（13.4）。切り替えは、今から先のどの時についても、種類を移す元と先の版の持ち主すべてで、ストアが持つ始まりによって gate に尋ねる。
- **表計算ソフトが読む物。** CSV の、`=`、`+`、`-`、`@`、タブか CR で始まるセルは、前に `'` を付けて書く。

適合の試験 `Administration` は、ほかの試験と同じく、どのストアにもかけられる（12 章）。切り替え（四つの形、番号、古い番号、記録を残さない同じ始まりへの切り替え、それぞれのあとで新しい申請が取る版）、記録（切り替えを `what` で読むこと、1 から行の数より一つ多い数までのどの `limit` のページでも、同じ行が二度出ず、抜ける行も無いこと）、取り込み（ソースのデータそのまま、人を足したデータとその差、ダイジェスト＝下書きのどの差でもないダイジェストは 400 で断る、一つの最新に対して同時の二つの取り込み、スナップショットを足す一日の取り込みと `Retry-After` と、どのデータの取り込みの記録もその 10 倍までであること（ストアも `ImportsMax` と `ChangesMax` で守る）、前のスナップショット、一人の下書きとその片づけ、ソースが変わらないときと変わったときの起動のときの取り込み、同時の二つのプロセス、取り込みが読んだソースの最後の取り込みをストアが比べること（`Change.LastImport`））、変更を決めたほかの版の始まり（`Change.Kept`）、gate（許されない人は何も変えず、何も読まず、アップロードも読まれない）、ID で読む記録と同じ ID の記録、`before` の書き方を確かめる。フレームワーク自身のパッケージの試験が、残りを両方のストアで確かめる。始まりを限る日、種類をあるチームからほかのチームへ移す切り替え（版を止める、先に延ばす、ほかのチームが止めた種類、後の日、ほかの切り替えの前に始まりを読んだプロセス）、30 秒のうちに切り替えを取る二つのプロセス、自分の部署の版だけを切り替えて読む部署の管理する人（種類の一覧でも、読めない版の記録とチケットは null で、チケットを数えない）、記録ごとに決めた始まり、記録のそれぞれの絞り込みと同じ時刻の行の順、PostgreSQL で `before` が指す行の時刻をキーだけで読むこと、操作が断る本文（13.6）と理由の文字、CSV とその部分、版 7 の表を上げること、13.6 のどの断りもである。

## 14. あとの段

ここにある物を変えずに、次の物を足せるように作ってある。

- **管理の続き。** 種類の版を使える最後の日の前に、種類の持ち主へ、足すデータを言う知らせ（13.2）。ritsu でした確かめの記録を、それによる切り替えと並べること。断った操作とログインを記録に入れること（13.5）。記録をいつまで持つか。
- **ほかの入り口。** RPC のエンドポイントも、HTTP のハンドラーと同じく `Do` の上の一つの層である。
- **ほかの言語。** Python（ASGI）と TypeScript も、同じ約束と同じ適合の試験を守る。
- **ほかの最初のファイル。** `ritsu ringi init`（上の「プロジェクトの最初のファイル」）が書くのは Go のプロジェクトで、エージェントのツールと Temporal の flow はオプションで足す。ほかの言語のプロジェクトは、あとで足す。
