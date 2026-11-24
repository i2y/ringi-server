# mcp

ringi のサーバーの、エージェントのツールを MCP で出すモジュールです。ritsu は、申請の種類の一式のツールを
`requests/mcp/tools.json` に書き出します。このモジュールは、それを MCP のサーバーとして出します。人のエージェントが
ツールを呼ぶと、その呼び出しはフレームワークの `Server.Do` の一つの操作になり、その人として行われます。gate、
チケットの版（`revision`）、断りは、画面や API から来たときと同じに効き、チケットの履歴に残るのは人です。

フレームワークのモジュールの横にある、それだけの Go のモジュールで、MCP の Go の SDK
（`github.com/modelcontextprotocol/go-sdk` v1.8.0）を使います。設計は [DESIGN.ja.md](DESIGN.ja.md) にあります。

英語版：[README.md](README.md)。

## エージェントが使える物

- `tools.json` のツールそのまま：その人が出せる申請の種類（`list_requests`）、その人を待っているチケット
  （`list_waiting`）、その人の申請（`list_mine`）、チケット（`get_ticket`）、種類ごとの申請（`submit_…`）、出し直し
  と直し、承認、差し戻し、却下、取り下げ、差し戻された申請の破棄、承認の取り消し、コメント、組織、マスタ、人とチームとマスタの
  行を言葉で近い順に探す物（`find_people`、`find_teams`、`find_master_…`）。`list_requests` は、どの種類も答えの
  言語の題と一緒に言います。二つの一覧は、新しい物から一ページずつ答え、次のページがあれば、その読み方を言います。チケットを管理する操作（`pause`、`resume`、
  `add_approver`、`hand_over`）のツールはありません。
- `check_operation`：チケットを変えるツールを、何も保存せずに試します。呼び出しが作るはずのチケットと、だれを待つ
  ことになるか、または受けるはずの断りを答えます。帳簿にも、その呼び出しを受けるかを尋ねるので、使い切った予算も
  わかります。エージェントは、出す前に食い違いを指摘できます。
- 申請の種類ごとの説明（`types/<kind>.json`）のリソース：フィールドの意味のために。
- サーバーの説明（instructions）：種類は `list_requests` で探す、変える前に確かめる、チケットを読んでその版を渡す、
  新しい操作には新しい `idempotency_key` を使う。

断りは、断りの種類と文、場所があればフォームの中の場所（`at`）を持つツールのエラーになり、その意味を言う前置きが
付きます。前置きも断りの文も、見る人の言語（英語か日本語）です。

## つなぎ方

```go
import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/mcp"
)

srv, err := server.New(cfg)
tools, err := mcp.New(srv, mcp.Config{})

// streamable HTTP で。Verify が、ベアラートークンの表す人を答えます。
h, err := tools.Handler(mcp.HTTP{Verify: verify})
mux.Handle("/mcp", h)

// または、一人の人のツールを stdio で。
err = tools.Serve(ctx, &sdkmcp.StdioTransport{}, server.Person{ID: "e1201"})
```

パッケージ `serve` が動かすサーバーなら、ツールを `/mcp` に置けます。ページや API と同じ守りとログインの後ろです。
そこでは、ベアラートークンの代わりに `HTTP.Person` が人を見つけます。サーバーのログインが見つけた人です（手元の
コンピューターでは `X-Person` ヘッダーか `-dev-login` のクッキー、人に使ってもらうサーバーではプロバイダーの
セッション）。だれもログインしていない要求には、ツールに届く前に 401 を返します。

```go
st, err := serve.Open(ctx, flags, log)
st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
	tools, err := mcp.New(srv, mcp.Config{Logger: log})
	if err != nil {
		return nil, err
	}
	return tools.Handler(mcp.HTTP{Person: serve.Person})
})
err = st.Serve(ctx, cfg, nil)
```

どのパッケージの requests のディレクトリにも、`openapi.json` の横に `mcp/tools.json` が要ります。生成したファイルと
一緒に `requests/mcp` も埋め込んでください。ツールの呼び出しは、その人として `Server.Do` に渡り、ツールから来たという
印（`Call.Via` の `"mcp"`。監査ログに残ります）を持ちます。`check_operation` は、何も保存しない `Server.Try` に渡ります。

```go
func verify(ctx context.Context, token string, r *http.Request) (mcp.Token, error) {
	p, expires, err := lookUp(ctx, token) // 配置する人が書く物：イントロスペクション、JWT、表など
	if err != nil {
		return mcp.Token{}, fmt.Errorf("%w: %v", server.ErrNoPerson, err) // 401 で答えます
	}
	return mcp.Token{Person: p, Expires: expires}, nil
}
```

## 例で試す

例のモジュールの `cmd/mcp` は、例のサーバー（`examples/` の十の種類。チケットはメモリに置きます）を、API を `/` に、
ツールを `/mcp` に置いて、`127.0.0.1:8081` で動かします。ベアラートークンは、そのまま人の ID として扱います。API の
`X-Person` ヘッダーと同じく、試すためだけの物です。どちらも本人を確かめないので、ループバックのアドレスでだけ待ち受け、
ループバックのアドレスに送られたリクエストにだけ答えます。例のパッケージは、組み込んだ物を使います。`-requests <ディレクトリ>`
を付けると、代わりにディスクの requests のディレクトリを読みます。

```sh
cd examples
go run ./cmd/mcp
```

コマンドは、何かを読んだり出したりする前に、フラグと環境変数を確かめ、誤りがあればそれを言って止まります。止まる
のは、`-lang` が `en` でも `ja` でもないとき、`-stdio` と一緒に `-addr` を渡したとき、`-stdio` で `-person` が
空のとき、`-stdio` 無しで `-person` を渡したとき（HTTP では、呼び出しごとのベアラートークンが人を表します）、
ループバックでないアドレスのとき、プロバイダーでサインインする設定（`RINGI_OIDC_CLIENT_SECRET`、
`RINGI_SESSION_KEYS`）が環境にあるときです。人を表すどちらの方法も本人を確かめないので、その設定のある所では
動きません。

別の端末で、ツールを一回呼んで答えの文を出す小さな関数を作ります（構造のある内容は `.result.structuredContent`
にあります）。`Accept-Language: ja` を送ると、文と断りの文は日本語になります。

```sh
call() {  # call <トークン> <ツール> <引数>：ツールを一回呼び、答えの文を出す
  curl -s http://127.0.0.1:8081/mcp -H "Authorization: Bearer $1" -H 'Accept-Language: ja' \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$2\",\"arguments\":$3}}" |
    jq -r '.result.content[0].text'
}
FORM='{"category":"purchase","items":[{"what":"Server","price":800000},{"what":"Storage","price":400000}],"purpose":"A server and its storage for the test lab","used_on":"2026-11-16","receipt":"receipt.pdf"}'
call e1201 check_operation "{\"tool\":\"submit_expense\",\"arguments\":{\"form\":$FORM}}"
call e1201 submit_expense "{\"idempotency_key\":\"try-1\",\"form\":$FORM}"
call e1201 submit_expense "{\"idempotency_key\":\"try-1\",\"form\":$FORM}"
call e1201 submit_expense "{\"idempotency_key\":\"try-2\",\"form\":$FORM}"
call e1101 list_waiting '{}'
call e1201 list_mine '{"limit":1}'
```

```text
何も保存していません。いま行うと、チケット（expense）は in_progress になります。ステップは first です。e1101 を待ちます。
受け付けました。チケット 01M7NQQD6XF71RQVBZFJ08MKEV（expense）は in_progress です。ステップは first です。e1101 を待っています。
受け付けました。チケット 01M7NQQD6XF71RQVBZFJ08MKEV（expense）は in_progress です。ステップは first です。e1101 を待っています。
受け付けました。チケット 01M7NQQD7D7NBWSNVV06JN4MNQ（expense）は in_progress です。ステップは first です。e1101 を待っています。
この人を待っているチケットの、新しい物から2件です：01M7NQQD7D7NBWSNVV06JN4MNQ（expense）、01M7NQQD6XF71RQVBZFJ08MKEV（expense）。
この人の申請の、新しい物から1件です：01M7NQQD7D7NBWSNVV06JN4MNQ（expense）。続きがあります。before に 01M7NQQD7D7NBWSNVV06JN4MNQ を渡すと、次のページを読めます。
```

同じキーの同じ呼び出しは同じチケットを答えます。チケットは一つで、別のキーなら別のチケットになります。一覧の構造の
ある内容は、ページのあとに続きのチケットがあるかを言い（`"more":true`）、そのときは文が次のページの読み方を言います。
最後のページは、次のページのことを言いません。申請が受け付けない値のフォームを確かめると：

```sh
HIGH='{"category":"purchase","items":[{"what":"Server","price":1000001}],"purpose":"A server for the test lab","used_on":"2026-11-16","receipt":"receipt.pdf"}'
call e1201 check_operation "{\"tool\":\"submit_expense\",\"arguments\":{\"form\":$HIGH}}"
```

```text
何も保存していません。いま行うと、断られます（form）。フォームに、この申請が受け付けない値があります。
`1_000_001JPY` は、`items[0].price` の範囲（`>=1JPY <=1_000_000JPY`）の外です
```

二行目は、申請の種類のコードが答えた断りの文で、見る人の言語（ここでは日本語）で出ます。構造のある内容は、場所も
`"at":"items[0].price"`（一行目の `price`）で言います。ベアラートークンの無い要求には 401 を返します。

`-stdio` を付けると、一人の人（`-person`。既定は `e1201`）のツールを標準入力と標準出力で出します。コマンドを起動
するクライアントが使う形です。stdio にはヘッダーが無いので、文の言語は `-lang` で決めます。

```sh
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"try","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_organization","arguments":{}}}'
  sleep 3; } |
  go run ./cmd/mcp -stdio -person e1101 -lang ja 2>/dev/null | jq -r 'select(.id == 2) | .result.content[0].text'
```

```text
今日の組織の版です：24人、14チーム。
```

（2026-11-18 に動かした結果です。チケットの ID は動かすたびに変わります。）

## 試験

```sh
cd examples
go test ./mcp/ ./cmd/mcp/
cd ../mcp
go test ./...
```

例を動かす試験は、コマンドと一緒に例のモジュールに置いています。ここに置くと、ツールを使うどのプロジェクトにも、
公開しない例のモジュールが要るようになります。プロジェクトで `go mod tidy` を回すと、読み込んだパッケージの試験も
読まれるからです。ここにあるのは、例の要らない試験です。種類のツールをどの版から出すかの選び方と、ツールの引数から
作る呼び出しを確かめます。

例のモジュールの試験は、フレームワークのメモリのストアと、例のパッケージと組織を使います。データベースは使いません。確かめるのは、
`tools/list` が例の `tools.json`（十の種類の 66 のツール。うち五つは探す物）と `check_operation` であること、人のイベントのベクタが
全部、API と同じにツールを通ること（例のベクタ 2,990 件のうち 2,058 件で、全部合い、断りの場所も合います。うち 10 件
は、API が 400 で答えるので正しくない呼び出しになります）、読むツールが API と同じ物を答えること、一覧のページ、約束が
取るキーの形、チケットを見られる人、断りが英語と日本語でツールのエラーになること、トークンがあるときと無いときの
streamable HTTP、`serve` が動かすサーバーの `/mcp` のツールが、そのログインが見つけた人（`X-Person` ヘッダー、
`-dev-login`）として動き、だれもいなければ 401 になること、同じ呼び出しをもう一度送ってもチケットが一つであること、
ツールの呼び出しの監査ログ、確かめが帳簿に尋ね、何も保存しないこと、例のコマンドが止まる設定です。
