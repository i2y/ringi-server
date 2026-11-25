# 設計：画面（screens）

このパッケージは、ritsu がプロジェクトの申請の種類ごとに書く画面を配る。申請書、承認カード、二つの一覧（見る人の答えを待つチケットの一覧と、見る人自身の申請の一覧）、履歴、新しい ritsu で生成したパッケージの持つ `offered` の画面の A2UI 1.0 のテンプレート（`a2ui/<種類>/<画面>.<言語>.json`）と、ritsu の部品と関数のカタログ（`a2ui/catalog.json`）、関数の中身（`a2ui/functions.js`）である。することは二つで、どちらも純粋な関数である。

- テンプレートに、一人の見る人のためにチケットの値を入れ、レンダラに送る A2UI 1.0 の三つのメッセージを返す。
- ボタンが押されたときにレンダラが送るアクションを、API（`openapi.json`）の操作にする。操作の `operationId`、パスのチケット、本文、冪等キーである。

このパッケージは何も保管せず、何も送らず、だれにも尋ねない。チケットを読み、gate に尋ね、人とファイルに名前を付け、操作を実行するのはサーバー（このリポジトリのルートのパッケージ）で、このパッケージは値を置き場に入れ、取り出すだけである。依存するのは標準ライブラリだけで、申請の種類を一つも知らない。どの種類も、そのテンプレート、説明（`types/<種類>.json`）、API が言うことに従って、同じコードを通る。

画面のデータの置き場と、それぞれに入れる物は ringi の DESIGN 13.8 に、操作とその本文、ボタンが送るチケットの版は 12.4 にある。この文書は、このパッケージがそれをどう守るかと、どこでサーバーが受け持つかを書く。

## 1. 画面の一式を読む

```go
// Load は `ritsu gen --target requests` が書くディレクトリ（openapi.json、types/、a2ui/）を fsys として読む。
func Load(fsys fs.FS) (*Set, error)
```

`*Set` は、生成したパッケージ一つの画面である。`Load` のあとは変わらず、いくつもの goroutine から使ってよい。サーバーは、動かすパッケージごとに一つの `Set` を読む。チケットは終わるまで申請の種類の版を変えないので、古い版の画面は、コードと同じく古いパッケージから読む。

`Load` は `openapi.json`、すべての `types/<種類>.json`、見つけたすべての言語の、すべての種類の五つの画面（`a2ui/<種類>/<画面>.<言語>.json`）、`a2ui/catalog.json` と `a2ui/functions.js` を読む。ベクタと組織は読まない。六つ目の画面 `offered` は、すべての言語のファイルがあれば同じように読み、無ければ読まない。約束に任意の画面として加わった物で、加わる前に生成されたパッケージ（約束の同じ版の物）には無い。無い種類は画面なしで読み、その値を入れる関数は、種類に無い画面と同じく `ErrKind` を返す。次のときは、何がおかしいかを言って断る。

| どんなとき | エラー |
|---|---|
| `openapi.json` の `x-ritsu-contract` か、説明の `contract` が、このパッケージの読む数（`screens.Contracts`。`[3]`）でない | `ErrContract` |
| ある種類が、持っている言語で五つの画面（`form`、`card`、`list`、`mine`、`history`）のどれかを欠く。または、画面が、版 `1.0` の `createSurface`、`updateDataModel`、`updateComponents` の三つのメッセージで、どれも面 `<種類>.<画面>` を持つ形になっていない | `ErrSet` |
| カタログの `catalogId`、`functions.js` の `export const catalogId`、各画面が ritsu の部品と関数に書くカタログが、一つの ID になっていない。または、画面が、それと A2UI の標準カタログのほかのカタログを書いている | `ErrSet` |
| 画面のデータに、このパッケージが値を入れない置き場がある（一覧は ringi の DESIGN 13.8、ここでは 2.2）。または、`/ticket` の下に、API のチケット（`components.schemas.ticket`）に無いキーがある。または、`/refused` に `text`、`field`、`comment` のほかのキーがあるか、画面を見せるときの値（`text` と `field` は null、`comment` は偽）で始まらないキーがある。または、画面が名前を引く表が、`/people`、`/files`、マスタから選ぶフィールドの `/masters/<フィールド>` のどれでもない。または、一覧の行が、その一覧の行に無いキーを読む（承認待ちの一覧の `made_by`、自分の申請の一覧の `requester`） | `ErrSet` |
| ボタンのイベントが API の操作でない。または、パスにチケットの無い操作に `ticket` を送る（逆も同じ）。または、操作の本文に無いキーを送るか、本文が必須とするキーを送らない | `ErrSet` |
| ある画面の言語どうしで、ボタン（ID、イベント、送る物）か、始めのデータが違う | `ErrSet` |

知らない置き場は、空のまま送らずに断る。テンプレートのままの置き場が、いつも害が無いとは限らないからである。`/edit` を空で送ると、承認者の直しが全部のフィールドを空にする。置き場を足す後の約束には、このパッケージがそれを覚える必要がある。`/ticket` だけは渡された物を受ける。API のチケットのキーならどれでも受け、チケットの同じ名前のキーで埋めるので、チケットをもっと見せるようになった画面も、ここを変えずに読める。

カタログの ID は、プロジェクトが `ritsu gen --catalog` に渡した物で、名前であり、カタログを配る場所ではない（DESIGN 13.2）。`Set.CatalogID` が答える。

```go
func (s *Set) CatalogID() string
func (s *Set) Kinds() []Kind                                  // 名前、版、言語、持っている画面。名前の順
func (s *Set) Actions(kind, screen string) ([]string, error)  // 画面の /can のキー
```

`Actions` は、見る人ができるかを画面が尋ねる物を並べる。その画面の `/can` のキーで、申請書では `submit`、`resubmit`、`discard`、承認カードではその種類の操作と `comment` である。二つの一覧と履歴は何も尋ねない。サーバーは、それぞれを gate に尋ね、チケットがその人からそれを受けるかを種類に尋ねて（`Server.Can`。本体の DESIGN 8.1）、その人ができる物を値を入れる関数に渡す。

画面を自分で作るパッケージ（素の HTML のページの [`page`](page/DESIGN.ja.md)）のために、読む関数がもう二つある。

```go
func (s *Set) Description(kind string) (json.RawMessage, error) // ritsu が書いたままの types/<kind>.json
func (s *Set) Buttons(kind, screen string) ([]Button, error)     // イベントを送るボタン

type Button struct {
	ID    string   // "button.approve"、"line.open"
	Event string   // 送るイベント。operationId で "approve"、"submit_expense"
	Keys  []string // 送るキー。順に "ticket"、"revision"、"comment"
}
```

`Buttons` は ID の順に並べる。レンダラの中だけで動くボタン（ファイルを開くボタン）は入れない。経費の承認カードの `button.edit` は `edit_expense` を送り、キーは `ticket`、`revision`、`fields`、`clear`、`comment` である。履歴の画面にはボタンが無い。

## 2. 画面に値を入れる

```go
func (s *Set) Form(in Form) (Screen, error)
func (s *Set) Card(in Card) (Screen, error)
func (s *Set) List(in List) (Screen, error) // 見る人の答えを待つチケットの一覧
func (s *Set) Mine(in List) (Screen, error) // 見る人自身の申請の一覧
func (s *Set) History(in History) (Screen, error)
func (s *Set) Offered(in Offered) (Screen, error) // 今は効いている版が無い種類の画面

// Screen は値を入れた画面で、送るメッセージを順に持つ。
type Screen struct {
	SurfaceID string
	Messages  []json.RawMessage // createSurface、updateDataModel、updateComponents
}
```

どれも、サーバーが持っている物だけの小さな構造体を受け取る。画面と同じやり方で割り出す必要のある物は受け取らない。

```go
type Form struct {
	Kind      string          // 種類の ASCII の名前。Ticket があれば省いてよい
	Language  string          // "en"、"ja"：その種類の画面が書かれた言語
	Ticket    json.RawMessage // もう一度申請するときの、差し戻されたチケット。新しい申請では nil
	SubmitFor []string        // 新しい申請で、見る人が代わりに申請できる人（DESIGN 5.8、5.13）
	MaySubmit bool            // `submitted by` のある種類で、見る人が自分で申請できるか
	Can       []string        // 見る人がここで今できること（Set.Actions）
	Names     Lookup          // nil なら ID をそのまま見せる
	Surface   string          // 2.3
	Serving                   // サーバーが画面に入れる物。下に書く
}

type Card struct {
	Kind     string          // 省いてよい
	Language string
	Ticket   json.RawMessage // 必須
	Can      []string
	Names    Lookup
	Surface  string
	Serving
}

// Serving は、サーバーが自分の配る申請書と承認カードに入れる物である（ringi の DESIGN 13.2、13.8）。
type Serving struct {
	Find    string   // 部品が値を探すアドレス（/find）。"" なら null のままで、部品は何も見つけない
	Upload  string   // 添付を預けるアドレス（/upload）。"" なら null のままで、ファイルを添付できない
	Refused *Refusal // 画面のボタンが送った物の断り（/refused。3.2）。無ければ nil
}

// List は一覧のチケットである。見る人の答えを待つ物（Set.List）か、見る人自身の申請（Set.Mine）。
type List struct {
	Kind     string            // 必須。一覧のチケットはどれもこの種類
	Language string
	Tickets  []json.RawMessage // 見せる順
	More     bool              // Tickets の最後のあとに続きのチケットがある（/more）
	Names    Lookup
	Surface  string
}

type History struct {
	Kind     string          // 省いてよい
	Language string
	Ticket   json.RawMessage // 必須
	Records  []Record        // チケットの履歴、その順
	Names    Lookup
	Surface  string
}

// Record はチケットの履歴の一行で、サーバーが保存した処理の `history` と、その時刻である。
type Record struct {
	At      time.Time
	History json.RawMessage // {"did": …, "stage": …, "by": …, "proxy_of": …, "comment": …, "cleared": …, "note": …, …}
}

// Offered は、今は効いている版が無い種類の `offered` の画面で、出せるようになる日と、戻る先である。
type Offered struct {
	Kind     string // 必須
	Language string
	From     string // 日付、YYYY-MM-DD（Server.Offered）。"" なら /from は null のままで、文は見えない
	Home     string // 戻るボタンが開くアドレス。"" なら /home は null のままで、ボタンは見えない
	Surface  string // 2.3
}
```

チケットは `next` が返した JSON で、サーバーが保管しているとおりの物である。チケットは指定した種類の物（`request`）でなければならず、申請書、承認カード、履歴では、`Set` が持つその種類の版の物でなければならない（違えば `ErrVersion`）。この三つは、種類の版で操作を送り、ステップを名前で見せるからである。一覧はどの版のチケットも見せる。行が読むフィールドのうち、チケットの版に無い物は空になる。

二つの一覧は、同じ `List` に、サーバーが選んだチケットを入れて作る。`List` は見る人の答えを待つチケットの一覧、`Mine` は見る人自身の申請（見る人が申請者の物と、見る人が代わりに出した物）の一覧で、API の `list_waiting` と `list_mine` が返すとおりに入れる（ringi の DESIGN 12.4）。1 ページずつで、`More` は API の `more`、つまりページのあとに続きのチケットがあるかである。あれば画面はボタン `more` を見せ、ボタンは一覧の操作を、ページの最後のチケットを `before` にして送る（3.1）。違うのは、行がチケットから持つ物である（2.2）。

申請書と承認カードには、サーバーが画面に入れる物（`Serving`）も渡す。部品が人、チーム、マスタの行を探すアドレス（`/find`。`<find>/people`、`<find>/teams`、`<find>/masters/<マスタのパス>` を、どれも `GET` で尋ねる。ringi の DESIGN 13.2）と、`FileAttachment` が添付を預けるアドレス（`/upload`。添付のある種類の画面）と、画面のボタンが送った物の断り（`/refused`。3.2）で、断りは、そのあとで画面にもう一度値を入れるときに渡す。アドレスはサーバーが決める。テンプレートに無い置き場には何もせず、`""` のままの物は、テンプレートのとおり null のままにする。

`Can` は操作の並びで、画面の物と照らし合わせる。画面に無い操作はエラーにするので、綴りを間違えた操作がボタンを黙って隠すことはない。新しい申請の申請書が受けるのは `submit` だけで、もう一度申請する申請書は `resubmit` と `discard` を受ける。

### 2.1 名前、マスタ、ファイル

画面は、ID とキーを、データの中の表を通して見せる。人とチームの名前は `/people`、マスタの行の名前は `/masters/<フィールド>`、ファイルの URL は `/files` にある。このパッケージは、画面が見せる ID を割り出し、その ID だけを、表ごとに一度、サーバーに尋ねる。

```go
type Lookup interface {
	// PeopleNames は、これらの ID の人とチームの、lang での名前を答える。
	PeopleNames(lang string, ids []string) (map[string]string, error)
	// MasterNames は、マスタ（"company_masters/vendors"：マスタのファイルの名前とマスタの名前）の、
	// これらのキーの行の、lang での名前を答える。
	MasterNames(lang, master string, keys []string) (map[string]string, error)
	// FileURLs は、これらの ID のファイルをブラウザが開く URL を答える。
	FileURLs(ids []string) (map[string]string, error)
}
```

ID は並べ替えてあり、一つずつで、空の物は無い。尋ねる ID が無ければ、メソッドを呼ばない。答えに無い ID はそのまま見せ（画面の `lookup` は、表に無いコードをそのまま見せる）、エラーは値を入れるのを止める。`Tables` は三つの map の上の `Lookup` で、試験と、名前を先に読んでおくサーバーのための物である。

画面が見せる ID は、`Set` を読むときに、画面そのものから読み取る。テンプレートが三つの表のどれかで引く値（部品、`List` か `LineItems` の行、レンダラの中だけで動くボタンの操作に書いた `lookup(value, table)`）、`PersonPicker` の値と、選ぶ範囲の並び（`among`）、`ApprovalRoute` が見せる経路の人（部分ごとの `approvers` と `team`、答えの `by` と `seat`、`sole` の人。部分の `of` はステップか flow の名前で、人ではない）である。マスタの表はフィールドの名前で呼び、フィールドのマスタは種類の説明から読む（`choice from m.vendors` で、`m.vendors` は `company_masters/vendors`）。

### 2.2 置き場

サーバーが渡す物と、このパッケージがそれを置き場ごとにどうするか（ringi の DESIGN 13.8）。

| 置き場 | 画面 | サーバーが渡す物 | このパッケージが入れる物 |
|---|---|---|---|
| `/form` | 申請書 | `Ticket`（もう一度申請するとき） | テンプレートの空のフォーム。もう一度申請するときは、空のフォームにあるフィールドごとにチケットの値を入れ、`ChoicePicker` が書く値は一つだけの配列にする（`"annual"` → `["annual"]`。明細の行の中も同じ） |
| `/form` | 承認カード | `Ticket` | 空のフォームの上に、チケットのフォームを API の形のまま |
| `/before` | 申請書 | `Ticket` | もう一度申請するときは、チケットのフォームをそのまま。新しい申請では null |
| `/requester` | 申請書 | | null（見る人が選ぶ） |
| `/submit_for` | 申請書 | `SubmitFor` | 新しい申請ではその ID。もう一度申請するときは空 |
| `/may_submit` | `submitted by` のある種類の申請書 | `MaySubmit` | その値 |
| `/ticket` | 申請書、承認カード、履歴 | `Ticket` | テンプレートの `/ticket` にあるキーごとに、チケットの値をそのまま。ritsu のテンプレートでは `id`、申請書と承認カードでは `revision`、申請書では `sent_back_by`、承認カードでは `status`、`stage`、`requester`、`made_by` |
| `/route` | 承認カード | `Ticket` | チケットの `stages` をそのまま |
| `/edit` | 承認者が入れるか直すフィールドのある種類の承認カード | `Ticket` | そのフィールドごとに、チケットの値を、入力の部品が持つ形で（選択は一つだけの配列）。値が無ければ空の値 |
| `/comment` | 申請書、承認カード | | `""` |
| `/send_back`、`/hand_over` | 承認カード | | 何も選んでいない、テンプレートの始めのまま |
| `/add_approver` | 承認カード | `Ticket` | 承認者は選ばないまま。`parts` には、チケットが今いるステップ（`revision.stage`）の部分のうち、`state` が `active` か `stopped` の物ごとに `{"part": <決め方>}` をその順に。ステップにいなければ空 |
| `/can` | 申請書、承認カード | `Can` | 渡された操作は true、ほかは false |
| `/refused` | 申請書、承認カード | `Serving.Refused` | `text` と `field` は null、`comment` は偽。断りがあれば、`text` は画面の言語の文（`Refusal.In`）。`field` は、`form` の断りなら場所の最初の名前（`Refusal.Field`）、ほかは null。`comment` は、種類が `comment` の断りなら真（3.2） |
| `/upload` | 添付のある種類の申請書、承認カード | `Serving.Upload` | そのアドレス。`""` なら null |
| `/find` | 申請書。人、チーム、マスタの行を選ぶ部品のある承認カード | `Serving.Find` | そのアドレス。`""` なら null |
| `/people` | 全部 | `Names.PeopleNames` | 画面が見せる ID の名前（2.1） |
| `/masters/<フィールド>` | 承認カード、二つの一覧 | `Names.MasterNames` | 画面が見せるキーの名前。フィールドのマスタから |
| `/files` | 承認カード | `Names.FileURLs` | 画面が開くファイル（フォームと明細の行の添付）の URL |
| `/tickets` | 二つの一覧 | `Tickets` | チケットごとに、承認待ちの一覧では `{"id", "status", "stage", "requester", "form"}`、自分の申請の一覧では `{"id", "status", "stage", "made_by", "form"}`。フォームは、行が読むフィールドだけ |
| `/history` | 履歴 | `Records` | 記録ごとに、テンプレートの行が読むキーを持つ行。`at` は記録の時刻で、その時刻の持つオフセットの RFC 3339 |
| `/from` | offered | `From` | その日付。`""` なら null |
| `/home` | offered | `Home` | そのアドレス。`""` なら null |

置き場の値は、テンプレートが始めに持つ形を保つ。オブジェクトはキーを保ち（チケットに値の無いフィールドはテンプレートのまま）、配列は配列のままである。このパッケージのコードには、申請の種類ごとの事情を一つも書いていない。どのフィールドが配列で持つ選択か、行がどのキーを読むか、どの値が人、ファイル、マスタのキーかは、どれも `Set` を読むときに、テンプレートと種類の説明から読み取る。

値は約束が書く形のままである（フィールドは ASCII の名前、選択はコード、数は単位の刻みの整数、日付は `YYYY-MM-DD`）。数は書かれたとおりに写し、浮動小数点数を通さない。

### 2.3 面

面の ID は、レンダラの中で一つに決まらなければならない（A2UI 1.0）。値を入れた画面の面の ID は、テンプレートの `<種類>.<画面>` に、`Surface` があれば `.<Surface>` を、無ければ承認カード、履歴、もう一度申請する申請書では `.<チケットの ID>` を続けた物である。同じ種類の一覧を二つ並べて見せるサーバーは、それぞれに別の `Surface` を渡す。

新しい申請の申請書には名前にするチケットが無く、見せるたびに別の `Surface` を渡さなければならない（無ければ `ErrInput`）。ランダムな ID でよい。申請の冪等キーは面から作るので（3.1）、一つの面で見せた二つの申請書に同じ値を入れれば一つの申請になり、申請を出した申請書を同じ面でもう一度見せれば、前の申請の答えが返る。

### 2.4 A2UI 1.0 の名前より前のレンダラ

```go
func (sc Screen) WithOldNames() (Screen, error)
```

A2UI 1.0 は、部品の四つのプロパティを `@checks`、`@accessibility`、`@metadata`、`@catalogId` と呼ぶ。1.0 より前の名前で読むレンダラに版を固定したサーバーは、`sc.WithOldNames()` を送る。各部品の直下のこの四つから `@` を外した、同じ画面である（DESIGN 13.5）。テンプレートは ritsu が書いたままにする。

## 3. ボタンのアクションを操作にする

```go
// ParseAction は、ボタンが押されたときにレンダラが送るメッセージを読む。
func ParseAction(message []byte) (Action, error)

// Operation は、アクションを、それに当たる API の操作にする。
func (s *Set) Operation(a Action) (Operation, error)

type Action struct {
	Name      string                     // イベントの名前：operationId
	SurfaceID string
	Component string                     // sourceComponentId：ボタンの ID
	Timestamp time.Time
	Context   map[string]json.RawMessage // イベントの context を、レンダラが値にした物
}

type Operation struct {
	ID     string          // operationId："approve"、"submit_expense"、"get_ticket"、"list_waiting"
	Method string          // "GET" か "POST"
	Path   string          // チケットを入れたパス："/tickets/T-1/approve"
	Ticket string          // パスのチケット（{id}）。無ければ ""
	Body   json.RawMessage // POST の本文。GET では nil
	Query  url.Values      // 一覧のボタン more が送るクエリー（before と kind）。無ければ nil
	Key    string          // POST の冪等キー（3.1）。GET では ""
}
```

メッセージは、A2UI 1.0 のレンダラからエージェントへのメッセージ `{"version": "1.0", "action": {"name", "surfaceId", "sourceComponentId", "timestamp", "context", …}}` である。`ParseAction` はこの形だけを受け、スキーマが必須とする五つのキーを求める（`userMessage` と `metadata` はあってよく、読まない）。ほかのメッセージ（レンダラのエラー、関数の答え）は `ErrMessage` である。

`Operation` は、ringi の DESIGN 13.1 が、申請の種類ごとのコードを持たないサーバーがすると書いたことをする。

1. 面が画面を指す。面の ID の最初の二つが種類と画面である（そのあとはサーバーの物）。`Set` のどの画面でもない面は `ErrSurface` である。
2. 部品がボタンを指す。その画面の `Button` で、アクションが同じ名前のイベントの物である。ボタンでない部品と、レンダラの中だけで動くボタン（添付を開くのは `openUrl` で、何も送らない）は `ErrButton` である。
3. context は、ボタンが送るキーをちょうど持たなければならない。キーが欠けるか、送らないキーがあれば `ErrContext` である。そういうメッセージは別の版の画面から来た物で、`clear` が欠ければ、人が空にした値が残ってしまう。
4. イベントの名前が `operationId` である。`ticket` はパスのチケット（`{id}`）で、空でない文字列でなければならない。操作がクエリーで受けるキー（一覧の操作の `before` と `kind`）は `Query` に入れる。文字列か数ならそのまま、null なら外し、ほかの値は `ErrContext` である。ほかのキーが本文で、ボタンが送る順に、レンダラが書いたままの値で並べる。操作が本文でもクエリーでも受けないキーを送るボタンは、読み込めない（`ErrSet`）。
5. API がそのキーに null を受けないときの null は、値が無いのと同じである（ringi の DESIGN 12.4）。本文が必須とするキーなら `ErrContext` で（null の `revision`、選んでいない `approver`、選んでいない引き継ぎの `of` か `to`）、ほかのキーは本文から外す。API が null を受けるキーは null のままにする。`requester`（見る人自身の申請）、`to`（申請者へ戻す）、`part`（待っている最初の部分）、`comment`（何も書いていないとき。コメントの操作を何も書かずに送れば `{"comment": null}` で、種類が `comment` で断る）である。

本文はここではこれ以上確かめない。サーバーは、HTTP から来ても画面から来ても、どの本文も同じように操作のスキーマで確かめ、`next` は `next` にしか確かめられない物（マスタのキー、組織にいる人、カレンダーが開いている日。DESIGN 13.7）を確かめる。コメントは、画面が作ったまま送る（`comment(text)` が両端の空白を除き、何も書いていなければ null を送る）。このパッケージは変えない。

だれの操作かは本文に無い（DESIGN 12.4）。だれがメッセージを送ったかはサーバーが知っており、HTTP から来た同じ操作と同じに、本文とその人からイベントを作る。

### 3.1 冪等キー

API のどの POST も冪等キーを受けるが（ringi の DESIGN 12.4）、レンダラは送らない。A2UI のアクションには、それを書く所が無い。`Operation` はアクションからキーを作る。`screen-` に、もとにした物の SHA-256 の 16 進を続けた物である。

| 操作 | もとにする物 | 結果 |
|---|---|---|
| 新しい申請（`submit_<種類>`。パスにチケットの無い唯一の POST） | 面、ボタン、本文 | 同じ申請書で二度押しても一つの申請。断られたあとに申請書を直せば別の申請 |
| チケットへの操作（ほかの POST 全部） | 面、ボタン、本文、timestamp | 押すたびに別の操作。同じメッセージの送り直し（レンダラのやり直し）は同じ |

新しい申請が二度通れば、チケットが二つでき、予算を押さえる種類では仮押さえも二つになるので、二度押しは一つにしなければならない。本文をキーに入れるのは、サーバーが、前に見たキーが別の本文で来たら Invalid で答えるからである。面だけでは、断られたあとに直した申請書を、二度と送れない。チケットの版を受けるチケットへの操作は、二度押せば、一度目がチケットを進め、二度目は版が古いとして断られるので、キーで止める必要は無い。同じ承認カードで同じ言葉のコメントを二度書けば、二つのコメントである。時刻の無いキーでは、二度目に一度目の答えが返り、何も残らない。

timestamp は時点として読む（`2026-11-16T18:00:00+09:00` と `2026-11-16T09:00:00Z` は同じ一押し）。だれの操作かはキーに入れない。サーバーはキーを人ごとに持ち、二人から来た同じキーは二つの操作である。

残ること：断られた新しい申請を、何も変えずにもう一度押すと、断った物（gate、組織）がその後変わっていても、一度目の断りが返る。新しい `Surface` で申請書をもう一度見せれば、もう一度送れる。また、ボタンを押したときでなく送るときの時刻を timestamp に書くレンダラでは、送り直しが新しい操作になる。

### 3.2 断り

サーバーが操作を断ったとき、API は `{"refused": 種類, "text": …, "text_ja": …, "at": …}` を返す（ringi の DESIGN 12.4）。種類は ringi の DESIGN 7.2 の物の一つで、文は英語と日本語の二つ、`at` は `form` の断りが指すフォームの場所である。空のコメントと長すぎるコメントは、どの操作で送っても、種類 `comment` で、場所を指さずに断られる。`Refusal` がそれを持つ。

```go
type Refusal struct {
	Refused string // 種類："form"、"comment"、"stale"、"person"…
	Text    string // 英語の文
	TextJa  string // 日本語の文
	At      string // フィールド（"purpose"）、明細の行のフィールド（"items[1].price"。行は 0 から）、明細の行で計算するセル（"lines[0].subtotal"）、明細（"lines"）。"" はどこも指さない
}

func (r Refusal) In(lang string) string // "ja" なら日本語の文（無ければ英語の文）、ほかは英語の文
func (r Refusal) Field() string         // At の最初の名前："items[1].price" なら "items"。無ければ ""
func (r Refusal) Place() (field string, row int, column string, ok bool)

// ReadPlace は、ringi が答えの中で場所を書く書き方で、場所を読む。
func ReadPlace(at string) (steps []Step, ok bool)

type Step struct {
	Name string
	Row  int // 指す明細の行。0 から数える。無ければ -1
}
```

`ReadPlace` が読むのは、ASCII の名前（英字か `_` で始まり、英字、数字、`_` が続く）を `.` でつないだ物である。明細の行は、明細の名前のあとの角括弧に書き、0 から数え、先頭に 0 を付けない。`items[1].price` や、入口のエラーが本文の値を指すときの `submit.form.items[1].price` である。ほかの書き方の場所（`items.1.price`、`items[01]`）と空の場所は読まない（`ok` が false）。`Place` は、`At` を、`form` の断りが指す場所として読む。フィールド（`purpose`。行の数の断りなら明細の `lines`）、明細の行（`items[1]`：`items` と 1）、明細の行のフィールド（`items[1].price`：`items`、1、`price`）のどれかである。行のフィールドは、人が入れるフィールドか、フォームが行ごとに計算するセル（`lines[0].subtotal`。小計が範囲の外になった最初の行）で、行が無ければ -1、列が無ければ `""` である。ほかの場所は場所にならない（`ok` が false）。`Field` は、`ReadPlace` が読める場所なら、どれでもその最初の名前を返す。

申請書と承認カードは、断りを `/refused` で言う（ringi の DESIGN 13.8）。`text` は画面の言語の文、`field` は `form` の断りが指すフィールド（`lines[0].subtotal` なら `lines`）で、指さなければ null、`comment` は種類が `comment` の断りかどうかである。画面は、文をそのフィールドの横か、コメントの下に見せ、どちらでもない断りは画面の頭に見せる（ringi の DESIGN 13.1）。どこに見せるかは、断りの種類と場所だけで決まる。場所の無い `form` の断りは、コメントの操作でも画面の頭に、`comment` の断りは、承認でもコメントの下に見せる。サーバーがそこに入れるやり方は二つある。`Serving.Refused` に断りを入れて、画面にもう一度値を入れるか（2 章）、`Set.Refused` が作るメッセージを送るかである。このメッセージは `/refused` だけを変え、本人が入れた値はそのまま残す。

```go
// Refused は、見せている申請書か承認カードで断りを言うメッセージを作る：{"version": "1.0",
// "updateDataModel": {"surfaceId", "path": "/refused", "value": {"text", "field", "comment"}}}。
// nil は、何も断られていないこと（どちらも null、comment は偽）を言う。
func (s *Set) Refused(surfaceID, lang string, r *Refusal) (json.RawMessage, error)
```

断りとともにサーバーが本人に言う一文は、断りの種類だけから、画面の言語で作る。断りの文からは作らない。断りの文は、イベント、フィールド、種類の行を、種類が書いたままの名前で言うからである（`` `self approval not allowed` ``）。

```go
func RefusalWords(lang, kind string) string // "ja" なら日本語、ほかは英語
func RefusalKinds() []string                // 表に一文がある断りの種類。並べた物
```

表は一つで、A2UI の画面の操作（パッケージ `web` が断りとともに返す）と素の HTML のページ（`page`。もう一度見せるページの頭）が両方使う。どちらでも、本人は同じ一文を読む。

| 種類 | 日本語 | 英語 |
|---|---|---|
| `state` | この申請は、今の状態ではこの操作を受け付けません。 | The request is not in a state that takes this now. |
| `clock` | この申請の期限の処理が、まだ終わっていません。少し待ってから、もう一度送ってください。 | The request is waiting for a deadline to be dealt with. Try again in a moment. |
| `stale` | このページを開いたあとで、申請が変わりました。今の申請を表示しています。確かめてから、もう一度送ってください。 | The request changed after this page was shown. Here it is as it is now: check it, and try again. |
| `person` | この操作はできません。 | You cannot do this on this request. |
| `event` | この申請の種類には、この操作がありません。 | This kind of request does not take this. |
| `limit` | この操作の回数か日数の上限に達しています。 | This has been done as many times, or for as many days, as the request allows. |
| `form` | 受け付けられない値があります。印の付いた項目を確かめてください。 | Some values were not accepted. Check the fields marked below. |
| `submit` | この申請を出せる人に入っていません。 | You are not one of the people who may make this request. |
| `data` | 会社が用意したデータでは、この日付の申請を扱えません。この申請を管理している人に問い合わせてください。 | The request cannot be dealt with on this date with the data the company has set up. Ask the people who keep this kind of request. |
| `book` | この申請は、あなたの所属では帳簿に記録できません。この申請を管理している人に問い合わせてください。 | The request cannot be recorded in the books for you. Ask the people who keep this kind of request. |
| `unread` | この申請を処理できませんでした。この申請を管理している人に問い合わせてください。 | The request could not be dealt with. Ask the people who keep this kind of request. |
| ほかの種類 | 申請は受け付けられませんでした。 | The request was not accepted. |

`LimitedWords(lang, wait)` は、同じやり方で、コメントを今は受け付けないこと（API の 429）を言う。`wait` 秒待ってから送り直すように言い、`wait` が 0 なら、その人のコメントをその申請がもう受け付けないことを言う（チケットのコメントがサーバーの受け付ける数に達したときと、その人が一人ぶんの数に達したときの両方に当てはまる文）。`OfferedWords(lang, from)` は、同じやり方で、今は効いている版が無い種類をある日から出せることを言う（`Server.Offered`。"This request may be made from 2027-04-01."、「この申請は2027-04-01から出せます。」）。A2UI の申請書の 404（`offered` の画面が無いパッケージの物で、画面のある物は日を画面で言う）、ページ、エージェントのツールが、この文を言う。

## 4. カタログと関数

レンダラは ritsu のカタログ（`catalog.json`）を読み、その関数（`functions.js`。ブラウザがそのまま読む ES モジュール）を動かす。サーバーはこの二つを配る。

```go
type Asset struct {
	Name        string // "catalog.json"、"functions.js"
	ContentType string // "application/json"、"text/javascript; charset=utf-8"
	Body        []byte
	ETag        string // 中身の SHA-256 の強い ETag
}

func (s *Set) Asset(name string) (Asset, error) // ほかの名前は ErrNotExist
func (s *Set) Assets() fs.FS                     // 二つを、その名前で
```

名前は中身が変わっても変わらず、ETag は変わる。だからサーバーは `Cache-Control: no-cache` で配り、一致する `If-None-Match` には 304 で答える。Content-Type は名前から推さずに渡す。システムの型の表が `.js` に別の型を言うことがあるからである。カタログの ID と、サーバーがカタログを配る場所は、同じでなくてよい（DESIGN 13.2）。

## 5. エラー

| エラー | いつ |
|---|---|
| `ErrContract` | `Load`：このパッケージが読まない約束 |
| `ErrSet` | `Load`：ファイルどうしが合わない（1 章） |
| `ErrKind` | 値を入れるとき：`Set` に無い種類か言語、種類を言わない一覧、別の種類のチケット。`Refused`：その種類の画面が書かれていない言語 |
| `ErrVersion` | 値を入れるとき：種類の別の版のチケット |
| `ErrInput` | 値を入れるとき：約束の形に読めないチケット、画面に無い操作を `Can` に渡した（またはその申請書が受けない操作）、もう一度申請するときに渡した `SubmitFor`、`Surface` の無い新しい申請の申請書、時刻の無い履歴の記録 |
| `ErrMessage` | `ParseAction`：A2UI 1.0 のアクションのメッセージでない |
| `ErrSurface`、`ErrButton`、`ErrContext` | `Operation`（3 章）。`ErrSurface` は `Refused` も返す：`Set` のどの申請書と承認カードでもない面か、`/refused` の無い画面の面 |

どれも、何がおかしかったかを添えて包んであり、`errors.Is` で見分けられる。`Lookup` の失敗は、そのまま包んで返す。

## 6. しないこと

- 見る人に何ができるかを決めること。`Can` は、gate と種類に尋ねたサーバーの答えで、このパッケージはそれでボタンを見せるか隠すだけである。
- 値を確かめること。画面は確かめられる物を確かめ、`next` が全部をもう一度確かめる（DESIGN 13.7）。
- 見る人がだれか、だれの代わりに申請できるか（`SubmitFor`、`MaySubmit`）、マスタのどの版がキーに名前を付けるかを知ること。組織と日付を知っているのはサーバーである。
- HTTP を話すこと、ファイルを保管すること、冪等キーを保管すること。ボタンが送る操作のキーは作るが（3.1）、保管して答えるのはサーバーである。

## 7. 試験

このディレクトリで `go test .`。6 秒ほどで、`-race` では 65 秒ほどである（`./...` なら、ページの `page/` も含む）。試験は、`testdata/` に置いた生成物を読む。

- `requests/`：ritsu の五つの例の申請に `ritsu gen --target requests` が書く物。どれも英語の名前で一度、日本語の名前で一度書いてあり、十の種類が、それぞれ六つの画面を英語と日本語で持つ（テンプレートは 120）。
- `vectors/`：十の種類のベクタ（2,990 行）。
- `gen/`：同じ例から ritsu が生成した Go のパッケージ。その申請の種類の一覧（`ticketkinds`）を、試験はサーバーと同じに動かす。

`testdata/regen.sh` が三つを ritsu のチェックアウトから書き直し、`testdata/bless.sh` が golden のファイルを書き直す。画面をレンダラと同じに読む試験は、Node（24）で `testdata/driver.mjs` を動かす。Node が無ければ、`SKIP:` の行を出して飛ばす。ドライバは、画面を `root` から描き（`Show` は `when` が成り立つあいだ子を、`List` と `LineItems` は項目ごとに行を描き、行の中で読む）、描いた部品の値を全部、`functions.js` と標準カタログの関数で値にし、ボタンを押す。押したときに A2UI 1.0 がレンダラに送らせるメッセージと、成り立たないボタンの検査を返す。

| 試験 | 確かめること |
|---|---|
| `TestFillEveryScreen` | ベクタから値を入れた 5,542 の画面。ベクタに出てくるチケット 2,174 枚（行の前と答えのチケット。同じ物は一度）それぞれの承認カード（言語は交互）、差し戻されたチケットごとの、もう一度申請する申請書（568）、履歴を残す答えごとの履歴（2,280）、種類ごとの全チケットの一覧（20）、人ごとの自分の申請の一覧（その人が申請者のチケットと、その人が代わりに申請したチケット。両方の言語で 460）、自分の申請と代わりの申請の新しい申請書（40）。どれもテンプレートと突き合わせ（版 1.0 の三つのメッセージが一つの面を持つ、テンプレートの `createSurface` と部品がそのまま、置き場が始めの形とキーを保つ）、値をチケットと突き合わせ（テンプレートでなく種類の説明から読み直す。一覧の行は、入れたチケットをその順に持ち、承認待ちの一覧では申請者を、自分の申請の一覧では代わりに申請した人を持つ）、`Lookup` の約束（表ごとに一度、並べ替えて、ID は一つずつ）を確かめる。Node で描き（部品 276,484）、名前の見つからない `lookup` が無いこと、null を受け取る書式の関数が無いこと、例外が無いこと、経路が見せる人に名前があることを確かめる |
| `TestReplayVectors` | 人がするイベントの行を、その画面で行う。行の前のチケットで値を入れた画面に、イベントの言うこと（フォーム、承認者が直すフィールド、空にしたフィールド、差し戻す先のステップ、足す承認者、コメント）を入れ、Node でボタンを押し、メッセージを操作に、操作とその人を種類が受けるイベントにする。2,990 行のうち 848 行は画面の物でない。時計 626、flow の答え 82、フィールドを今と同じ値に直す 2（画面は何も変えない物として送る）、画面が送らない物を求める 138（その種類の承認カードにボタンの無いイベント 68、フォームに無いフィールド 16、値を入れて同時に空にするフィールド 12、申請者が入れないフィールド 10、版の無いイベント 10（画面は、見せているチケットの版を送る）、承認カードにボタンの無い所に足す承認者 8、承認者が直さないフィールド 6、申請者にだけ戻す承認カードでの、差し戻す先のステップ 6、行が無くてよい明細を空にする物 2）で、種類はどれも断る。残りが 2,230 回の押下になる（申請をやめる操作は、申請書でも押す）。2,052 回は送られ、ベクタと同じ答えになる（同じイベント 1,984、同じことを別の言い方で言ったイベントが同じ答えになる物 68。ベクタが部分を書かずに、待っている一つの部分に任せる所で、画面が部分の名前を送る物 56 と、ベクタがもう空のフィールドを空にする所で、画面は何も空にしない物 12）。送った 2,052 回のうち 310 回は断られる（`person` 114、`stale` 84、`clock` 40、`limit` 30、`form` 20、`state` 12、`submit` 10）。176 回はボタンの検査で止まり、種類もどれも断る（`form` 106、`comment` 56、`stale` 14）。2 回は、画面が隠すフィールド（`shown when` が成り立たない）に値を入れる行で、画面はそのフィールドだけを除いて行のとおりに送り、種類は行をどれも断る（`form`） |
| `TestEveryButton` | どの種類も持つ 100 の画面の 336 のボタンを、進んでいるチケット、差し戻されたチケット、二つの一覧で値を入れた画面で押す。イベントを送る 324 は、その名前の操作（操作は 40）を、チケットを入れたパスで作り、POST にはキーが付く。ファイルを開く 12 は断る（`ErrButton`）。新しい申請の申請書で押したチケットのボタンも断る（`ErrContext`）。承認カードの操作は種類に通し、`event` で断られる物は無い |
| `TestOffered` | 種類の画面 `offered` に、両方の言語で、日とホームのアドレスを入れた物とどちらも入れない物。`/from` と `/home` がそれを持つか null であり、Node で描けること。この画面のファイルの無いパッケージと、一つの言語の分が無いパッケージは、画面なしで読め、`Offered` は `ErrKind` になること |
| `TestRefusalsWhereTheScreensSayThem` | ベクタの `form` と `comment` の断り 230 を、両方の言語で、種類の申請書と承認カードの `/refused` に入れる（`Set.Refused`）。場所を指す 158（うち 12 は明細の行のフィールド、4 は行の中でフォームが計算する値）は、そのフィールドを持つ画面（少なくとも一つ）ではその横に出て、持たない画面では頭に出る。コメントの 56 はコメントの下に、場所を指さない 16 は頭に出る |
| `TestWaitingParts` | `/add_approver/parts` が、`active` か `stopped` の部分だけを持つこと。進んでいるチケットの部分に、状態を一つずつ入れて確かめる |
| `TestDriverSees` | ドライバが見るべき物を見ること：表に無い名前、null を受け取る書式、描かれないボタン |
| `TestLoad`、`TestLoadRefuses`、`TestTicketPlaces` | `Set` の種類、版、言語。壊した十六の一式を、`Load` が、何がおかしいかを言う文とエラーで断ること（`/refused` に値を持って始まる画面、このパッケージが値を入れない `/refused` のキー、自分の申請の一覧の無い一式、申請者を読むその一覧の行を含む）。申請者を名前で見せるようにした承認カードを読め、値と名前が入ること |
| `TestParseAction`、`TestOperation`、`TestOperationKeys` | メッセージの形。操作、エスケープしたパス、本文の null（何も書いていないコメントは null のまま送り、種類が断る）。3 章の断りのそれぞれ。3.1 のキー |
| `TestReadPlace` | `ReadPlace` が読む場所と読まない場所。それぞれを `Refusal.Place` と `Refusal.Field` がどう読むか |
| `TestFillRefuses`、`TestWithOldNames`、`TestAssets` | 値を入れるときのエラー。四つの名前。二つのファイル、その型と ETag、ファイルシステムとしての `Assets`（`testing/fstest`） |
| `TestDescriptionAndButtons` | 種類ごとの説明がファイルのままであること。画面ごとのボタンが並べてあり、どれもイベントとキーを送ること。経費の申請書と承認カードのボタンがテンプレートのとおりであること。無い種類と無い画面を断ること（`ErrKind`） |
| `TestRefusalWords` | 十二の断りの種類が、両方の言語で、それぞれ自分の文を持つこと。どの文も、ほかの種類の文とも、表に無い種類に言う文とも違い、コードも規則の名前も含まないこと。ほかの言語は英語で言うこと |
| `TestLimitedWords` | サーバーがいまは受け付けないコメントについて、画面が両方の言語で言うこと。待つ秒数（1 秒と、それより長いとき）と、待っても変わらないときに、その人のコメントをこの申請はもう受け付けないこと |
| `TestGolden` | 値を入れた六つの画面（一つは、断りを入れた、もう一度申請する申請書）、`Set.Refused` が作るメッセージ、七つの操作を、`testdata/golden/` に置いた物と比べる。golden のファイルは読むためにも置いてある。違えば落ち、`testdata/bless.sh` が書き直す |

パッケージを手で壊すと、試験はそれに気づく。承認カードの `/edit` をテンプレートのままにすると `TestFillEveryScreen` が、申請書の `/before` を null のままにすると `TestFillEveryScreen` と `TestReplayVectors` が、選択を一つだけの配列にしないと `TestFillEveryScreen` が、自分の申請の一覧で、代わりに申請した人の名前を引かないか、その行に代わりに申請した人として申請者を入れると `TestFillEveryScreen` が、`stopped` の部分を `/add_approver/parts` から落とすと `TestWaitingParts` が落ちる。
