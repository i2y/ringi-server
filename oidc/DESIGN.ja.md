# OpenID Connect でログインする（`oidc`）

英語：[DESIGN.md](DESIGN.md)。

このパッケージは、組織の OpenID Connect のプロバイダーで利用者をログインさせ、ログインした人をセッションに保ち、リクエストごとに送り主がだれかをフレームワークに伝えます。中身は、フレームワークの `Authenticator`（`server.Authenticator`）、ログインとログアウトの三つのルート、ログインした人にだけ出すページを包むラッパーです。依存するのは、標準ライブラリとフレームワークの本体（パッケージ `server`）だけです。日本語の文に名前を置く決まり（`internal/jatext`）も使います。

フレームワークは、セッションもトークンもクッキーも自分では読みません（本体の DESIGN 2.3 節）。それらに関わることは、すべてこのパッケージにあります。

## 1. 何をするか

- プロバイダーの設定を issuer の URL から読み（ディスカバリー）、署名の鍵を持っておきます。知らない鍵で署名されたトークンが来たら、鍵を読み直します。
- 認可コードフローでログインさせます。PKCE（`S256`）、`state`、`nonce` を使い、トークンエンドポイントにはクライアントシークレットを渡します。
- ID トークンを確かめます。署名、発行者、宛先、時刻、nonce です。
- トークンのクレームから、フレームワークの人（`server.Person`）を作ります。作り方は、配備する側が関数で渡します。パッケージにも一つ付いています。組織のデータの中から、トークンの確認済みのメールアドレスを持つ人を探す関数です。
- その人をクッキーに入れ、AES-256-GCM で暗号化します（改ざんもこれで見つかります）。鍵は入れ替えられます。ログインからの上限と、使われない時間の上限があります。
- リクエストの送り主を答えます（`Authenticate`）。セッションのないブラウザがページを開けばログインへ送り、セッションのないそれ以外のリクエストには 401 を返します。

トークンは持ち続けません。ID トークンを確かめて人を作ったら、トークンは捨てます。

## 2. API

```go
signin, err := oidc.New(ctx, oidc.Config{
	Issuer:       "https://login.example.test/tenant",            // プロバイダーが書くとおりに
	ClientID:     "ringi",
	ClientSecret: os.Getenv("RINGI_OIDC_SECRET"),
	RedirectURL:  "https://requests.example.test/login/callback", // プロバイダーに登録した URL
	Keys:         keys,                                           // oidc.ParseKeys(os.Getenv("RINGI_SESSION_KEYS"))
	Map: oidc.ByEmail(func(ctx context.Context) ([]server.OrgPerson, error) {
		return oidc.PeopleOf(ctx, srv) // 今日有効な組織のデータの人
	}),
})
```

`New` は、プロバイダーのディスカバリーの文書と鍵を読みます。そのため、issuer の誤りや、プロバイダーにつながらないことは、最初のログインのときではなく、サーバーを起動したときにエラーになります。守れない設定も受け付けません（9 節）。

`*SignIn` のメソッド：

| メソッド | すること |
|---|---|
| `Authenticate(r) (server.Person, error)` | リクエストのセッションの人を返す。セッションがない、復号できない、期限が切れたときは `server.ErrNoPerson` |
| `Mount(mux)` | `GET /login`、リダイレクト URL のパスへの `GET`、`POST /logout` のルートを、`Handle(pattern string, h http.Handler)` を持つものに足す。`*http.ServeMux` か、画面のハンドラー（`*web.Handler`） |
| `Require(h) http.Handler` | ログインした人に `h` を出す。セッションがなければ、ブラウザが開くページはログインへ送り、それ以外のリクエストには 401 を返す（8 節） |
| `LoginURL(returnTo) string` | ログインのあと `returnTo` に戻ってくる、ログインのアドレス |

人を作る部品：

```go
// Map は、すべての確かめを通った ID トークンのクレームから、フレームワークの人を作る。
// *Refusal を返すとログインさせず、その言葉を見せる。ほかのエラーは失敗（500）になる。
type Map func(ctx context.Context, c Claims) (server.Person, error)

type Claims struct {
	Issuer, Subject string
	Email           string
	EmailVerified   bool
	Name            string
	All             map[string]json.RawMessage // トークンのクレームすべて。書かれたとおりに
}

func (c Claims) Strings(name string) []string // 文字列か、文字列のリストのクレーム

func ByEmail(people People) Map              // 確認済みのメールアドレスの人
func Roles(m Map, claim string) Map           // m の作る人に、クレームにあるロールを足す
func PeopleOf(ctx context.Context, srv *server.Server) ([]server.OrgPerson, error)
type People func(ctx context.Context) ([]server.OrgPerson, error)
```

セッションを上限より前に終わらせる仕組みは、使わなくてもかまいません（6.4 節）：

```go
type Revocations interface {
	Revoke(ctx context.Context, session string, until time.Time) error
	Revoked(ctx context.Context, session string) (bool, error)
}

func NewMemoryRevocations() *MemoryRevocations // プロセスが一つのとき
```

`ParseKeys(text)` は、クッキーの鍵をテキストから読みます。鍵は base64 で一つ 32 バイト、カンマか空白で区切り、新しいものを先に書きます。

## 3. ログインの流れ

```
Browser                     Server (this package)                        Provider
  | GET /tickets/T-1, no session     |                                          |
  |--------------------------------->| Require: a page and no session           |
  | 303 /login?return_to=/tickets/T-1                                           |
  |<---------------------------------|                                          |
  | GET /login?return_to=...         |                                          |
  |--------------------------------->| makes state, nonce, PKCE verifier        |
  | 303 authorization_endpoint?...   | Set-Cookie __Host-ringi-signin-<id>      |
  |<---------------------------------|  (sealed: state, nonce, verifier,        |
  |                                  |   return_to, the time)                   |
  | GET authorization_endpoint?response_type=code&client_id                     |
  |     &redirect_uri&scope&state&nonce&code_challenge                          |
  |     &code_challenge_method=S256                                             |
  |---------------------------------------------------------------------------->|
  |                  (the person signs in at the provider)                      |
  | 302 redirect_uri?code&state[&iss]                                           |
  |<----------------------------------------------------------------------------|
  | GET /login/callback?code&state, Cookie __Host-ringi-signin-<id>             |
  |--------------------------------->| opens the cookie, deletes it,            |
  |                                  | compares state (and iss)                 |
  |                                  | POST token_endpoint: code, verifier,     |
  |                                  |  redirect_uri, the client's secret       |
  |                                  |----------------------------------------->|
  |                                  | {id_token, access_token, token_type}     |
  |                                  |<-----------------------------------------|
  |                                  | checks the ID token (5)                  |
  |                                  | Map: claims -> server.Person             |
  | 303 /tickets/T-1                 | Set-Cookie __Host-ringi-session          |
  |<---------------------------------|                                          |
  | GET /tickets/T-1, Cookie __Host-ringi-session                               |
  |--------------------------------->| Authenticate -> the person               |
```

1. セッションのないブラウザがページを開くと、`Require` がそのページを `return_to` にして `/login` へ送ります。
2. `/login` は state、nonce、PKCE の verifier を作ってログインのクッキーに入れ、ブラウザをプロバイダーの認可エンドポイントへ送ります。
3. 利用者がプロバイダーでログインすると、プロバイダーはコードと state を付けてコールバックへ送り返します。
4. コールバックは、ログインのクッキーを復号して消し、state（と `iss`）を比べ、コードと verifier をトークンエンドポイントに渡して ID トークンを受け取ります。
5. ID トークンを確かめ（5 節）、`Map` で人を作り、セッションのクッキーを付けて、元のページへ送ります。

それぞれの決まり：

- **進行中のログイン**は、専用のクッキーに入れます。名前は `state` の先頭 16 文字で決まるので、二つのタブからログインを始めても、両方とも終えられます。セッションと同じく暗号化し（6.1 節）、10 分もちます。コールバックが読んだら、その先がどうなっても消します。コードと state は一度しか使えません。
- **`state`、`nonce`、PKCE の verifier** は、それぞれ 32 バイトの乱数を base64url にしたもの（43 文字）です。challenge は `BASE64URL(SHA-256(verifier))` です。コールバックは、受け取った state を、このブラウザのクッキーにある state と定数時間で比べます。ブラウザが始めていないコールバックは、これで受け付けません（クロスサイトリクエストフォージェリ）。verifier と nonce がコードをこのブラウザのログインに結び付けるので、別のログインから取ったコードは、プロバイダー（PKCE）とこのパッケージ（`nonce`）の両方で通りません。
- **コールバックの `iss`**（RFC 9207）：あれば、issuer と同じでなければなりません。プロバイダーのディスカバリーが送ると言っていれば（`authorization_response_iss_parameter_supported`）、なければなりません。
- **トークンのリクエスト**は、`grant_type=authorization_code`、コード、リダイレクト URL、verifier の POST です。クライアントシークレットは、`Authorization` ヘッダー（`client_secret_basic`。RFC 6749 2.3.1 のとおり、クライアント ID とシークレットを先にフォームエンコードする）か、本文（`client_secret_post`）で送ります。リダイレクトには従わないので、シークレットはトークンエンドポイントのほかには届きません。答えは、`token_type` が `Bearer` で `id_token` を含む JSON でなければなりません。それ以外の中身は捨てます。
- **コールバックのあと**は、303 で元のページへ送ります。コードがアドレスバーや履歴に残らないようにするためです。コールバックの答えには `Referrer-Policy: no-referrer` を付けます。
- **レスポンスモードはクエリだけ**です。コールバックは GET なので、ログインのクッキー（`SameSite=Lax`）も、クロスサイトのリクエストへの守りも通ります。

### 3.1 戻り先

`return_to` はこのサーバーのパスに限ります。`/` 一つで始まり、バックスラッシュ、制御文字、空白、スキーム、ホスト、ユーザーを含まず、2,048 文字以内でなければなりません。そうでない `return_to` は、プロバイダーに何も送らないうちに、`/login` で 400 にします。ないときは `/` とし、このパッケージ自身のルートを指すときも `/` にします。ログインのあとにまたログインへ戻らないようにするためです。確かめは、ブラウザが読むとおりの文字列に対して行います。`//host` や `/\host` は別のホストのアドレスとして読まれ、ブラウザは URL を読む前にタブと改行を取り除くからです。

## 4. プロバイダー

### 4.1 ディスカバリー

`New` は `<issuer>/.well-known/openid-configuration` を読みます（issuer の末尾の `/` は、パスを足す前に取ります）。文書の `issuer` は、設定の issuer と一文字も違わず同じでなければなりません。ID トークンはすべて issuer と照らし合わせるので、`/` 一つ違うだけでも別の issuer です。別の issuer を答えるプロバイダーは受け付けません。文書には、`authorization_endpoint`、`token_endpoint`、`jwks_uri`（どれも https の URL）、`code` を含む `response_types_supported`、設定が許すアルゴリズムを一つ以上含む `id_token_signing_alg_values_supported` が要ります。`code_challenge_methods_supported` があれば `S256` を、`token_endpoint_auth_methods_supported` があれば設定の方式を含んでいなければなりません。issuer は https でなければなりません。

文書を読むのは一度だけです。プロバイダーがエンドポイントを移したら、サーバーを起動し直してください。

### 4.2 鍵

鍵は `jwks_uri` からだけ取り、トークンのヘッダーからは取りません（`jku`、`x5u`、`jwk` は読みません）。使う鍵は、`use` がないか `sig` で、`key_ops` がないか `verify` を含み、`alg` がないかトークンのアルゴリズムと同じものです。RSA の鍵は 2,048 ビット以上（RFC 7518 3.3）、8,192 ビット以下です。ES256 の EC の鍵は P-256 の上の点でなければなりません。

| とき | どうなるか |
|---|---|
| トークンの `kid` が手元の鍵にない | 鍵を読み直す。ただし 1 分に 1 回まで。それでもない `kid` は受け付けない |
| トークンに `kid` がない | トークンのアルゴリズムの鍵を一つずつ試す。どれでも確かめられなければ、上と同じく読み直す |
| 鍵を読んでから 1 時間たった | 使う前に読み直す。読めなければ古い鍵を使い、読めなかったことをログに書く |

読み直しを 1 分に 1 回までにしているのは、でたらめな `kid` のトークンを大量に送られても、そのたびにプロバイダーに問い合わせないためです。読み直しは一度に一つだけ走り、ほかはその終わりを待ちます。

## 5. ID トークンの確かめ

確かめる順に並べます。一つでも通らなければ受け付けず、ログにはどの確かめで止まったかを書きます（トークンは書きません）。

| 確かめること | 理由 |
|---|---|
| トークンが、パディングのない base64url の三つの部分からなり、64 KiB 以内 | コンパクト形式の JWS。暗号化したトークン（五つの部分）は読まない |
| ヘッダーの `alg` が `Config.Algorithms`（既定は RS256、PS256、ES256）のどれか。`none` と HS256 は受け付けず、`crit` のあるヘッダーも受け付けない | アルゴリズムはトークンの側が名乗るので、どれを信じるかは一覧で決める（RFC 8725 3.1） |
| 署名が、そのアルゴリズムのプロバイダーの鍵（4.2 節）で確かめられる | だれでも作れるトークンではなく、プロバイダーのトークンである |
| `iss` が issuer と完全に同じ | 別のプロバイダーのトークンではない（OpenID Connect Core 3.1.3.7 の 2） |
| `aud` にクライアント ID があり、クライアント ID と `Config.Audiences` のほかは含まない | このクライアントのために作られたトークンである（3） |
| 宛先が二つ以上なら `azp` があってクライアント ID と同じ。`azp` があれば、クライアント ID と同じ | トークンを渡された相手がこのクライアントである（4、5） |
| `exp` があり、今からずれの幅を引いた時刻より後 | 期限の切れたトークンは受け付けない（9） |
| `iat` があり、今にずれの幅を足した時刻より後でなく、このログインを始めた時刻からずれの幅を引いた時刻より前でない | このログインより前や未来に作られたトークンは、このログインのものではない（10） |
| `nbf` があれば、今にずれの幅を足した時刻より後でない | RFC 7519 4.1.5 |
| `nonce` が、このブラウザのログインに入れた nonce と同じ（定数時間で比べる） | このログインへの答えであり、ほかのログインのものを差し込まれたのではない（11） |
| `sub` があり、1〜255 文字の文字列 | プロバイダーでのその人の ID（OpenID Connect Core 2） |

ずれの幅は、プロバイダーの時計がサーバーの時計からどれだけずれてよいかです（`Config.Skew`、既定は 1 分）。トークンはトークンエンドポイントから TLS で届くので、OpenID Connect では署名の確かめを省いてもかまいません。それでも署名は確かめます。経路がどうであれ、鍵、発行者、アルゴリズムを同じように確かめるためです。

`email_verified` を真とするのは、トークンが `true`（JSON の真偽値）と書いているときだけです。`at_hash` は確かめません。アクセストークンを使わないからです。

日時（`exp`、`iat`、`nbf`）は秒で読み、小数はミリ秒まで読みます。前後に 10^12 秒（およそ 3 万年）を超える日時は、そこで止めます。範囲を大きく外れた値も、遠い未来か遠い過去として読み、反対側に回り込むことはありません。

## 6. セッション

### 6.1 クッキー

名前は `__Host-ringi-session` で、`Secure`、`HttpOnly`、`SameSite=Lax`、`Path=/` を付け、`Domain` は付けません（`__Host-` の接頭辞。ブラウザはこの形でしか保存せず、ほかのホストからは設定できません）。値は、セッションを AES-256-GCM で暗号化したものです。96 ビットの乱数の nonce、暗号文、タグを base64url にしています。暗号化の追加データは、クッキーの名前、issuer、クライアント ID です。そのため、ログインのクッキーをセッションとして読むことはできず、別のプロバイダーの設定のセッションは復号できません。

暗号化する前の JSON の中身：

| キー | 中身 |
|---|---|
| `v` | この形の版（1） |
| `id` | セッションの ID。16 バイトの乱数の base64url で、ログインのたびに新しくする |
| `p`、`r` | `Map` が作った人の ID とロール |
| `s` | トークンの `sub` |
| `at` | ログインした時刻（Unix 秒） |
| `seen` | 最後に更新した時刻（6.3 節） |

トークンのほかの中身は残しません。ブラウザが保存できる大きさ（クッキーの名前と属性を含めて 4,096 バイト）を超えるセッションは、ログインのときにエラーのページを出し、ログに書きます。大きくなるのはロールだけです。

### 6.2 鍵の入れ替え

`Config.Keys` は、一つ 32 バイトの乱数です（`New` が受け付けない鍵は 9 節）。暗号化には先頭の鍵を使い、復号にはどの鍵も使います。入れ替えるときは、新しい鍵を先頭に置き、古い鍵をその後ろに残します。新しいセッションと更新したセッション（6.3 節）は新しい鍵で暗号化され、古いセッションも復号できます。古い鍵で復号したセッションは、前の更新からの時間にかかわらず、その人が最初に開いたページで更新し、新しい鍵に移します。古いセッションが終わったら（ログインからの上限。既定は 12 時間）、古い鍵を外します。その鍵で暗号化したクッキーは復号できなくなり、その人はログインし直します。GCM の乱数の nonce の限界から、一つの鍵で暗号化するクッキーは 2^32 個までです。それよりずっと前に入れ替えてください。

### 6.3 上限

- **ログインから**（`Config.SessionLimit`。既定は 12 時間）：使っていてもいなくても、この時間でセッションは終わります。クッキーの `Max-Age` を残りの時間にしているので、ブラウザもクッキーを捨てます。
- **使われない時間**（`Config.IdleLimit`。既定は 2 時間、負の値なら上限なし）：この時間更新されなかったセッションは終わります。`Require` は、利用者が開くページと送るフォームでセッションを更新します。更新は 1 分に 1 回まで（上限の 4 分の 1 のほうが短ければ、その間隔に 1 回まで）です。`Authenticate` は更新できません。フレームワークが渡すのはリクエストだけで、クッキーは答えに付けるものだからです。そのため、API のリクエスト、画面の fetch、毎分サーバーに新着を尋ねるページでは、セッションは延びません。ページを使っている利用者のセッションは延びます。

### 6.4 上限より前にセッションを終わらせる

ログアウトするとクッキーを消します。それより前に取られたコピー（別のタブのものや、ほかのだれかが取ったもの）は、上限までは復号できます。すぐに終わらせる必要がある配備では、`Config.Revocations` を渡します。すると、ログアウトはセッションの ID をログインからの上限まで記録し、ログインし直すと置き換わるセッションを記録し、`Authenticate` は記録された ID を受け付けません。`NewMemoryRevocations` はメモリーに記録するので、プロセスが一つのとき向けです。PostgreSQL のストア（`*postgres.Store`）は専用の表に記録するので、サーバーのあるプロセスでログアウトすると、ほかのプロセスでもそのセッションは使えなくなります。記録は、そのセッションがもともと終わるはずだった時刻を過ぎると消します。

## 7. だれがログインしたか

`Map` はクレームからフレームワークの人を作ります。返すのは、人か、`*Refusal`（その言葉を利用者の言語で 403 とともに見せ、ログインさせない）か、それ以外のエラー（500）です。

`ByEmail(people)` は、`email_verified` が真のとき、トークンの `email` をアドレスに持つ人を組織のデータから探します。アドレスは前後の空白を除き、大文字と小文字を区別せずに比べます。次のときはログインさせません。

| とき | 返すもの |
|---|---|
| トークンにメールアドレスがないか、確認済みでない | `ErrNoVerifiedEmail` |
| そのアドレスの人がいない | `ErrNotInOrganization` |
| そのアドレスの人が二人以上いる | `ErrAmbiguousEmail` |

読むのは ID トークンのクレームです。プロバイダーの userinfo エンドポイントは呼びません。`ByEmail` で人を見つけるには、プロバイダーが ID トークンに `email` と `email_verified` を入れている必要があります（スコープ `email` を求めれば、多くのプロバイダーは入れます。ID トークンのクレームの設定が要るプロバイダーもあります）。

プロバイダーが確認済みと言うアドレスを信じるので、組織自身のプロバイダーと組み合わせてください。ほかの組織と共有するプロバイダーは、別の組織のアドレスを確認済みとして渡すことがあります。その場合は、`sub` やその組織のクレームで人を探す `Map` を書いてください。

`people` は、今日有効な組織の版の人を返します。`PeopleOf(ctx, srv)` はそれをサーバーから読みます（`Server.People`）。サーバーが持つスナップショットを読むので、どのチケットでもサーバーが使うのと同じ人になります。人をほかから読む配備では、その関数を渡してください。

`Roles(m, claim)` は、`m` が作る人に、クレーム（`groups` のような、文字列か文字列のリスト）にあるロールを足します。ゲートが使うロールです。人のロールは、ログインしたときにプロバイダーが渡したものです。プロバイダーでの変更は、次のログインで反映されます。

組織のデータから外れた人や、ロールが変わった人も、今のセッションは上限まで使えます。もっと早く終わらせるには `Config.Revocations` を使います。

## 8. 答え

このパッケージのルートの答えには、どれにも `Cache-Control: no-store` と、画面と同じヘッダー（`Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`、`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`）と、`Referrer-Policy: no-referrer` を付けます。ページはスクリプトもスタイルもない素の HTML で、英語か日本語で出します。ページで選んだ言語（パッケージ `web` のクッキー `ringi-lang`）、無ければ `Accept-Language` が優先するほう（どちらもなければ英語）です。人の設定の明るい配色か暗い配色で描くことを言い（`<meta name="color-scheme" content="light dark">`）、文言は `<main>` に置きます。

| ルート | とき | 答え |
|---|---|---|
| `GET /login` | `return_to` が正しい | プロバイダーへの 303。ログインのクッキーを付ける |
| | `return_to` がこのサーバーのパスでない | 400、ページ |
| `GET` コールバック | ログインできた | `return_to` への 303。セッションのクッキーを付ける |
| | このブラウザで進行中のログインがない（クッキーがない、10 分を過ぎた、state が違う）、コードがない、`iss` が issuer と違う | 400、やり直しを求めるページ |
| | プロバイダーが `error=access_denied` を返した | 403、ページ |
| | プロバイダーがほかのエラーを返した、つながらない、トークンのリクエストにトークン以外を返した、ID トークンが確かめを通らない | 502、ページ |
| | トークンエンドポイントが `invalid_grant` を返した（使用済みのコード、またはコードに合わない verifier） | 400、やり直しを求めるページ |
| | `Map` がログインさせない | 403、その言葉のページ |
| | `Map` が失敗した、またはセッションを作れない | 500、ページ |
| `POST /logout` | このサイトから、またはブラウザ以外から | `Config.AfterLogout`（`/`）への 303。セッションのクッキーを消す |
| | 別のサイトから（`http.CrossOriginProtection`） | 403 |
| `Require` | セッションがなく、ブラウザがページを開いた（GET か HEAD） | `/login?return_to=<そのページ>` への 303 |
| | セッションがなく、ブラウザがフォームを送った | 401、ログインへのリンクのあるページ |
| | セッションがなく、それ以外 | 401、API と同じく `{"error": "no one is signed in"}` |
| | `Revocations` が失敗した（6.4 節） | 500。ブラウザのページかフォームにはページ、それ以外には `{"error": "the server failed"}` |

ブラウザが開くページとは、`Sec-Fetch-Mode: navigate` のリクエストのことです。それを送らないブラウザでは、`text/html` を受け付けるリクエストのことです。

コールバックでプロバイダーが返すエラーは、コード（英数字と `_`、64 文字まで）だけをログに書きます。`error_description` はログにも書かず、ページにも出しません。だれでも、好きな説明を付けたコールバックへのリンクを作れるからです。

プロバイダーからもログアウトする設定（`Config.ProviderLogout`）では、クッキーを消したあと、プロバイダーの `end_session_endpoint`（OpenID Connect RP-Initiated Logout。`client_id` と `post_logout_redirect_uri` を付ける）へ進むページを返します。進むのはページの再読み込みとリンクで、リダイレクトにはしません。画面のポリシーはフォームの送り先をこのサイトに限っていて（`form-action 'self'`）、ブラウザはフォームのあとのリダイレクトにもそのポリシーを当てるからです。既定では切ってあります。このサーバーからログアウトしても、組織のほかのサービスにはログインしたままです。

## 9. 設定

| フィールド | 既定 | 受け付けないとき |
|---|---|---|
| `Issuer` | 必須 | https の URL でない、クエリかフラグメントがある |
| `ClientID`、`ClientSecret` | 必須 | 空、端に空白か改行がある、制御文字がある（ファイルから読んだ秘密は、ファイルの最後の改行を持つことがある） |
| `ClientAuth` | `client_secret_basic` | `client_secret_basic` でも `client_secret_post` でもない |
| `RedirectURL` | 必須 | https の URL でない、クエリかフラグメントがある、パスがない |
| `Scopes` | `openid email profile` | （`openid` がなければ足す） |
| `Params` | なし | パッケージが設定するパラメーターを含む（`response_type`、`client_id`、`redirect_uri`、`scope`、`state`、`nonce`、`code_challenge`、`code_challenge_method`、`response_mode`、`request`、`request_uri`） |
| `Algorithms` | RS256、PS256、ES256 | ほかのアルゴリズム（`none` と HS256 を含む）がある、またはプロバイダーの署名のアルゴリズムが一つも入っていない |
| `Audiences` | なし | |
| `Skew` | 1 分。負の値ならなし | |
| `Keys` | 必須 | 一つもない。32 バイトでない鍵がある。32 バイトの乱数でない鍵がある：文字列（どのバイトも英字・数字・記号・空白。パスフレーズを base64 にした物など）か、同じバイトの繰り返し（違うバイトが 16 より少ない）。同じ鍵が二度ある |
| `SessionLimit` | 12 時間 | |
| `IdleLimit` | 2 時間。負の値ならなし | |
| `Revocations` | なし | |
| `Map` | 必須 | |
| `LoginPath`、`LogoutPath` | `/login`、`/logout` | パスでない、パッケージのほかのルートと同じ |
| `AfterLogout` | `/` | このサーバーのパスでない（3.1 節） |
| `ProviderLogout`、`PostLogoutRedirectURL` | 切、なし | 入にしたのにプロバイダーに `end_session_endpoint` がない。戻り先が https の URL でない |
| `HTTPClient` | タイムアウトが 10 秒のクライアント | |
| `Clock`、`Logger` | システムの時計、`slog.Default()` | |

プロバイダーへの呼び出しで読む答えは、1 MiB までです。`Config.Check` は、上の表で `New` が受け付けない物を、プロバイダーに届かずに断ります。サーバーが、何かに届く前に何が違うかを言うためです。`New` はもう一度確かめてから、ディスカバリーの文書と鍵を読みます。32 バイトの乱数が文字列になるのは 10^13 個に一つより少なく、違うバイトが 16 より少なくなるのは 10^16 個に一つより少ないです。

## 10. フレームワーク、ページ、画面との組み立て

```go
var srv *server.Server
signin, err := oidc.New(ctx, oidc.Config{…, Map: oidc.ByEmail(func(ctx context.Context) ([]server.OrgPerson, error) {
	return oidc.PeopleOf(ctx, srv)
}), Revocations: store})
srv, err = server.New(server.Config{…, Store: store, Authenticator: signin})
h, err := web.New(srv, web.Config{Require: signin.Require, LoginURL: signin.LoginURL, SignOut: "/logout"})
signin.Mount(h) // GET /login、GET /login/callback、POST /logout
http.ListenAndServe(addr, h)
```

`Mount` は、ルートを、ページと画面のハンドラーの守り（`web.Handler.Handle`）の後ろに置きます。クロスサイトのリクエストへの守りとヘッダーです。ログインのルートには、ログインした人は要りません。API、画面、ページは、サーバーの `Authenticate` で人を知ります。その `Authenticate` がこのパッケージのものです。`web.Handler` は `Mux` を満たします。例の試験が、そのことをコンパイルのときに確かめます。

`web` のハンドラーは、ログインの三つのフックを受け取ります。`Require` はページの各ルートを包み、セッションがないままページを開くとログインへ進んで、あとで戻ります。`LoginURL` は、「ログインしていません」と伝えるページのリンクを作ります（セッションなしで送られたフォームや、入力している間にセッションが終わったとき）。`SignOut` は、各ページの上の帯にあるボタンの送り先のパスです。

`web` を使わずに単独で使うときは、`Mount` に `*http.ServeMux` を渡します。その場合も、パッケージのルートは自分のヘッダーとクロスサイトのリクエストへの守りを持ちます（ログアウトは POST です）。

このパッケージの試験は、`web` の代わりに `*http.ServeMux` を使って、この組み立てを動かしています（12 節）。`examples/signin_test.go` は、`oidc/oidctest` を使って、`web` との組み立てを動かしています。

## 11. ログに書くこと

`Config.Logger` に書くのは、ログインした人（その ID とトークンの `sub`）、ログインさせなかったこと（理由、`sub`、メールアドレス）、失敗したログイン（どの段階で、なぜか。確かめの言葉のとおり）、ログアウトした人（その ID）、鍵の読み直しです。トークン、コード、state、nonce、verifier、クッキーの値、クライアントシークレット、クッキーの鍵は書きません。

## 12. 試験

試験は、自前のプロバイダーを動かします（`httptest`、TLS）。ディスカバリー、鍵、認可エンドポイント（試験が選んだ人をすぐにログインさせる）、トークンエンドポイントです。トークンエンドポイントは、クライアントシークレット、リダイレクト URL、PKCE、コードが一度しか使われないことを確かめ、入れ替えのできる RSA（RS256、PS256）と ECDSA（ES256）の鍵で ID トークンに署名します。試験は、次のトークンのクレームやヘッダーを変えることも、別の署名を付けることも、トークンエンドポイントに別の答えを返させることもできます。クッキーを保存するクライアントが、リダイレクトを一つずつたどって、ブラウザと同じようにルートを回ります。時刻は試験用の時計で進めます。

| ファイル | 試すこと |
|---|---|
| `flow_test.go` | セッションのないページから、プロバイダーを通ってその人としてページを開き、ログアウトするまで。アルゴリズムごと、シークレットの送り方ごと。`Require` の三つの答え。プロバイダーからのログアウト。別のサイトからのログアウト。ヘッダー。ログに秘密が出ないこと |
| `refuse_test.go` | ID トークンの確かめを一つずつ。通らない形ごと、通る境目ごとにトークンを作る。設定が許さないアルゴリズム。鍵の読み直し。使わない鍵。state。二つのタブ。コールバックの `iss`。PKCE。二度使うコード。プロバイダーのエラー。トークンエンドポイントの失敗。`return_to`。`New` が受け付けない設定 |
| `session_test.go` | 二つの上限。復号できないクッキー（書き換えたもの、別のクライアントのもの、ログインのもの）。鍵の入れ替え。`Revocations` でセッションを終わらせること、`Revocations` が失敗したとき |
| `people_test.go` | `ByEmail`、`Roles`、ログインさせないときのページ（日本語のもの、ページで選んだ言語のもの、マークアップを含むアドレスのもの。明るい配色か暗い配色で描き、`<main>` を持つこと）、`Map` が自分でログインさせないときと失敗したとき |
| `framework_test.go` | 10 節の組み立てを、`web` の代わりに `*http.ServeMux` で。手で書いた申請の種類と、取り込んだ組織のデータを持つフレームワークのサーバー。その API が、セッションがなければ 401、あればその人で答えること。`PeopleOf` がアドレスを読むこと |
| `postgres_test.go` | `RINGI_TEST_DATABASE` がデータベースを指すとき、PostgreSQL のストアを `Revocations` として。同じ表を使う二つのストアを、サーバーの二つのプロセスに見立てる。一方でログアウトしたセッションを、もう一方が受け付けないこと。セッションがもともと終わる時刻を過ぎた記録が消えること。同じセッションを二度記録すると、遅いほうの終わりが残ること |
| `internal_test.go` | JWK の読み取り、base64url、`Accept-Language`、プロバイダーのエラーのコード、`email_verified`、日時 |

さらに、決まりをコードから一つずつ外して試験を回しました。外したのは、宛先の二つの決まり、`azp`、nonce、署名、アルゴリズムの一覧、`crit`、`iss`、`exp`、`iat`、`nbf`、鍵の読み直しを 1 分に 1 回までにすること、state、ログインの 10 分、コールバックの `iss`、`return_to`、PKCE の verifier を送ること、クッキーの鍵、暗号化の追加データ、セッションの二つの上限、`Revocations`、ログアウトの守り、確認済みのメールアドレス、ログ、ヘッダーです。どれを外しても、試験が一つ以上落ちました。

`oidc/oidctest` は、このパッケージを使うサーバーの試験のためのプロバイダーです。試験の自前のプロバイダーより小さく、ID トークンには RSA の鍵一つ（RS256）で署名し、わざと失敗させることはできません。クライアント、リダイレクト URL、PKCE、コードが一度しか使われないことは、試験のプロバイダーと同じように確かめます。

## 13. フレームワークから使うもの

- `Server.People(ctx)`：今日有効な組織の版の人。`PeopleOf` が使います。
- PostgreSQL のストアの `Revoke` と `Revoked`。プロセスをまたいで `Revocations` を使うためです（6.4）。

## 14. あとの段

- トークンエンドポイントでクライアントが自分を示すほかの方式（`private_key_jwt`、相互 TLS）。
- プロバイダーが「この人はログアウトした」と知らせてきたときにセッションを終わらせること（OpenID Connect Back-Channel Logout）。セッションを `sub` ごとに記録する必要があります。
- `max_age` と `acr`。最近のログインや、より強いログインを求める申請の種類のため。
