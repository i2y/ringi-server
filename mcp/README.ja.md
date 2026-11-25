# エージェントのツール（mcp）

パッケージ `mcp` は、ringi のサーバーの操作を、AI エージェントが使うツールにします。つなぐのは Model Context Protocol（MCP）です。ツールは、ritsu が申請の種類から `requests/mcp/tools.json` に書いたものです。エージェントは、それを使う人として動きます。できることはその人と同じで、拒否はページや API と同じです。申請の履歴には、その人の名前が残ります。英語版は [README.md](README.md) です。

| したいこと | 読むもの |
|---|---|
| `ritsu ringi init` で作ったサーバーを、エージェントに使わせる | [エージェントに申請と承認をさせる](../docs/agents.ja.md)。ツールの足し方、呼び方、それぞれのツールがすること |
| Go で組み立てたサーバーにツールを置く | 下の[使い方](#使い方) |
| 送るものと返るものを見る | 下の[例で試す](#例で試す) |
| しくみとその理由を知る | [設計](DESIGN.ja.md) |

MCP の Go の SDK（`github.com/modelcontextprotocol/go-sdk` v1.8.0）を使うので、独立した Go のモジュール（`github.com/i2y/ringi-server/mcp`）にしてあります。

## 使い方

サーバーに渡すどのパッケージの requests のディレクトリにも、`openapi.json` の隣に `mcp/tools.json` が要ります。ritsu が書いたほかのファイルと一緒に、`requests/mcp` も埋め込みます。

```go
import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/mcp"
)
```

ツールを出す方法は三つあり、だれが使っているかの見つけ方が違います。

### ベアラートークンで

```go
srv, err := server.New(cfg)
tools, err := mcp.New(srv, mcp.Config{})
h, err := tools.Handler(mcp.HTTP{Verify: verify})
mux.Handle("/mcp", h)
```

ツールを streamable HTTP で出します。`Verify` は自分で書く関数で、トークンがだれのもので、いつまで使えるかを返します。

```go
func verify(ctx context.Context, token string, r *http.Request) (mcp.Token, error) {
	p, expires, err := lookUp(ctx, token) // 自分で書く。トークンを確かめ、だれのものかを返す
	if err != nil {
		return mcp.Token{}, fmt.Errorf("%w: %v", server.ErrNoPerson, err) // 401 になる
	}
	return mcp.Token{Person: p, Expires: expires}, nil
}
```

### ページのログインの後ろで

パッケージ `serve` のサーバーでは、ツールを、ページや API と同じログインの後ろに置きます。`HTTP.Person` は、トークンの代わりに、そのログインで見つかった人を取ります。だれもログインしていないリクエストには、ツールに届く前に 401 を返します。

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

### 一人のツールを、標準入力と標準出力で

```go
err = tools.Serve(ctx, &sdkmcp.StdioTransport{}, server.Person{ID: "e1201"})
```

### ツールが返すもの

- ツールは、文と、その文が述べているもの（チケットや一覧）の JSON（`structuredContent`）を返します。
- 拒否は、ツールのエラーです。拒否の種類、その意味を説明する文、フォームの拒否ならその場所（`at`）が入ります。
- 文は、英語か日本語です。`Accept-Language` が求める言語、なければ組織のデータにあるその人の言語、なければ英語になります。`mcp.Config.Language` で、別の選び方にもできます。
- 申請の種類ごとの説明（`types/<種類>.json`）は、MCP のリソースです。エージェントは、フィールドの意味をここで読みます。
- ツールでした操作は、操作の記録に `mcp` と残ります（`AuditEntry` の `Via`）。

## 例で試す

例のモジュールの `cmd/mcp` は、例の申請の種類（英語が五つ、日本語が五つ）を、`127.0.0.1:8081` で動かします。API は `/`、ツールは `/mcp` です。チケットはメモリに置きます。ベアラートークンは、人の ID そのままです。本人を確かめない、試すためだけのものなので、自分のコンピューターのアドレスでだけ待ち受け、そこへ送られたリクエストにだけ応答します。

```sh
cd examples
go run ./cmd/mcp
```

別の端末で、ツールを呼んで、返った文を表示する小さな関数を作ります。JSON は `.result.structuredContent` にあります。

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

同じキーの呼び出しでは、チケットは一つです。別のキーでは、別のチケットになります。一覧の JSON の `more` は、続きがあるかどうかです。続きがあれば、次のページの読み方が文にあります。

### 申請の種類が受け付けないフォーム

```sh
HIGH='{"category":"purchase","items":[{"what":"Server","price":1000001}],"purpose":"A server for the test lab","used_on":"2026-11-16","receipt":"receipt.pdf"}'
call e1201 check_operation "{\"tool\":\"submit_expense\",\"arguments\":{\"form\":$HIGH}}"
```

```text
何も保存していません。いま行うと、断られます（form）。フォームに、この申請が受け付けない値があります。
`1_000_001JPY` は、`items[0].price` の範囲（`>=1JPY <=1_000_000JPY`）の外です
```

二行目が、拒否の文です。JSON には、場所も `"at":"items[0].price"`（一行目の `price`）のように入ります。

### 標準入力と標準出力で

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

`-requests <ディレクトリ>` を付けると、例のものの代わりに、ディスクの requests のディレクトリを使います。ritsu が書いたばかりのものを試せます。一緒に使えないフラグを渡したとき、ほかのコンピューターから届くアドレスのとき、環境にプロバイダーのログインの設定があるときは、起動しません。

## 試験

```sh
cd examples
go test ./mcp/ ./cmd/mcp/
cd ../mcp
go test ./...
```

例を動かす試験は、例のモジュールにあります。ここの試験が例を読み込むと、ツールを使うプロジェクトのすべてに、公開しないモジュールが要るようになるためです。確かめるのは次のことです。

- `tools/list` が、例の `tools.json`（66 のツール）と `check_operation` である
- 人の操作のベクタが、すべて API と同じになる（2,990 のうち 2,058。すべて一致）
- 読むツールが API と同じものを返す。一覧がページに分かれる
- チケットを見られる人、二つの言語の拒否、呼び出しの送り直し、操作の記録、確かめるだけなら何も保存しないこと
- トークンがあるときとないときの HTTP、パッケージ `serve` のサーバーでのツール

ここの試験は、種類のツールをどの版から出すかの選び方と、ツールの引数から作る呼び出しを確かめます。
