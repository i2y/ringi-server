# mcp

ringi のサーバーで、エージェントが使うツールを Model Context Protocol（MCP）で出すモジュールです。ritsu は、申請の種類のツールを `requests/mcp/tools.json` に書き、このモジュールがそれを出します。ツールの呼び出しは、フレームワークの一つの操作になり、そのエージェントを使う人として実行されます。gate、チケットの `revision`、拒否は、ページや API と同じです。チケットの履歴には、その人の名前が残ります。

Go の独立したモジュールで、MCP の Go の SDK（`github.com/modelcontextprotocol/go-sdk` v1.8.0）を使います。設計は [DESIGN.ja.md](DESIGN.ja.md) にあります。英語版は [README.md](README.md) です。

## エージェントができること

| ツール | すること |
|---|---|
| `list_requests` | その人が出せる申請の種類を、題と一緒に並べる |
| `list_waiting`、`list_mine` | その人を待っているもの、その人の申請を、新しい順に 1 ページずつ並べる |
| `get_ticket` | チケットを読む |
| `submit_…`、`resubmit_…`、`edit_…` | 申請を出す、差し戻された申請を出し直す、承認者が入れるフィールドを直す |
| `approve`、`reject`、`send_back`、`retract`、`comment`、`withdraw`、`discard` | その人がチケットにしてよいことをする |
| `get_organization`、`get_master_…` | 組織とマスタを読む |
| `find_people`、`find_teams`、`find_master_…` | 人、チーム、マスタの行を、言葉で近い順に探す |
| `check_operation` | チケットを変えるツールを試し、何も保存しない。チケットがどうなり、だれを待つか、または拒否を返す。帳簿にも尋ねるので、使い切った予算もわかる |

申請の種類ごとの説明（`types/<kind>.json`）は、フィールドの意味を調べるためのリソースです。サーバーの説明（instructions）には、種類は `list_requests` で探す、変える前に確かめる、チケットを読んでその `revision` を渡す、新しい操作には新しい `idempotency_key` を使う、と書いてあります。チケットを管理する操作（`pause`、`resume`、`add_approver`、`hand_over`）のツールはありません。

拒否は、ツールのエラーです。種類、文、フォームの場所（あれば `at`）と、その意味の説明が入ります。

文と拒否は、英語か日本語です。`Accept-Language` が求める言語、なければ組織のデータにあるその人の言語、なければ英語になります。`mcp.Config.Language` で、別の選び方にもできます。

## つなぎ方

```go
import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/mcp"
)

srv, err := server.New(cfg)
tools, err := mcp.New(srv, mcp.Config{})

// streamable HTTP で。Verify は、ベアラートークンが表す人を返します。
h, err := tools.Handler(mcp.HTTP{Verify: verify})
mux.Handle("/mcp", h)

// または、一人の人のツールを stdio で。
err = tools.Serve(ctx, &sdkmcp.StdioTransport{}, server.Person{ID: "e1201"})
```

パッケージ `serve` が動かすサーバーでは、ツールを `/mcp` に、ページや API と同じログインの後ろに置きます。ベアラートークンの代わりに、`HTTP.Person` が、サーバーのログインで見つかった人を返します。だれもログインしていないリクエストには、ツールに届く前に 401 を返します。

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

どのパッケージの requests のディレクトリにも、`openapi.json` の隣に `mcp/tools.json` が要ります。生成されたほかのファイルと一緒に、`requests/mcp` も埋め込みます。呼び出しは `Server.Do` に渡り、ツールから来たことが記録されます（`Call.Via` が `"mcp"`。監査ログに残ります）。`check_operation` は `Server.Try` に渡ります。

```go
func verify(ctx context.Context, token string, r *http.Request) (mcp.Token, error) {
	p, expires, err := lookUp(ctx, token) // 配置する人が書くもの：イントロスペクション、JWT、表など
	if err != nil {
		return mcp.Token{}, fmt.Errorf("%w: %v", server.ErrNoPerson, err) // 401 になります
	}
	return mcp.Token{Person: p, Expires: expires}, nil
}
```

## 例で試す

例のモジュールの `cmd/mcp` は、例の十種類を、API を `/` に、ツールを `/mcp` に置いて、`127.0.0.1:8081` で動かします。チケットはメモリに置きます。ベアラートークンは、そのまま人の ID です。試すためのもので、本人を確かめないので、自分のコンピューターのアドレスでだけ待ち受け、そこへ送られたリクエストにだけ応答します。

```sh
cd examples
go run ./cmd/mcp
```

別の端末で、ツールを一回呼んで結果の文を表示する、小さな関数を作ります（構造のある内容は `.result.structuredContent` にあります）。

```sh
call() {  # call <トークン> <ツール> <引数>：ツールを一回呼び、結果の文を表示する
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

同じキーの呼び出しでは、チケットは一つです。別のキーでは、別のチケットになります。一覧では、構造のある内容の `more` に続きがあるかどうかが入り、続きがあれば、次のページの読み方が文にあります。

申請の種類が受け付けないフォームを確かめると、次のようになります。

```sh
HIGH='{"category":"purchase","items":[{"what":"Server","price":1000001}],"purpose":"A server for the test lab","used_on":"2026-11-16","receipt":"receipt.pdf"}'
call e1201 check_operation "{\"tool\":\"submit_expense\",\"arguments\":{\"form\":$HIGH}}"
```

```text
何も保存していません。いま行うと、断られます（form）。フォームに、この申請が受け付けない値があります。
`1_000_001JPY` は、`items[0].price` の範囲（`>=1JPY <=1_000_000JPY`）の外です
```

二行目が、拒否の文です。構造のある内容には、場所も `"at":"items[0].price"`（一行目の `price`）のように入ります。

`-stdio` を付けると、一人（`-person`。指定がなければ `e1201`）のツールを、標準入力と標準出力で出します。コマンドを起動するクライアントは、この形で使います。stdio にはヘッダーがないので、言語は `-lang` で選びます。

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

`-requests <ディレクトリ>` を付けると、例のものの代わりに、ritsu が書いたばかりのもののような、ディスクの requests のディレクトリを使います。一緒に使えないフラグ、ほかのコンピューターから届くアドレス、プロバイダーのログインの設定がある環境では、何かを出す前に止まります。

## 試験

```sh
cd examples
go test ./mcp/ ./cmd/mcp/
cd ../mcp
go test ./...
```

例を動かす試験は、例のモジュールにあります。ここの試験が例を読み込むと、ツールを使うプロジェクトのすべてに、公開しないモジュールが要るようになるためです。確かめるのは、`tools/list` が例の `tools.json`（66 のツール）と `check_operation` であること、人の操作のベクタがすべて API と同じになること（2,990 のうち 2,058、すべて一致）、読むツールが API と同じものを返すこと、ページに分けた一覧、チケットを見られる人、二つの言語の拒否、トークンがあるときとないときの HTTP、パッケージ `serve` のサーバーでのツール、呼び出しの送り直し、監査ログ、確かめるだけなら何も保存しないことです。ここの試験は、種類のツールをどの版から出すかの選び方と、ツールの引数から作る呼び出しを確かめます。
