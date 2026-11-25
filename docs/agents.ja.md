# エージェントに申請と承認をさせる

AI エージェントから、申請を出したり、待っている申請を調べて答えたりできるようにします。英語版は [agents.md](agents.md) です。

エージェントは、それを使う人として動きます。できることはその人と同じで、権限も、申請の決まりも、ページから操作するときと変わりません。履歴には、その人の名前が残ります。

エージェントが使う機能（ツール）は、サーバーが申請の種類から用意します。ツールの呼び出し方は、Model Context Protocol（MCP）という、エージェントとほかのプログラムをつなぐ決まりに沿っています。

承認の経路のステップを一つエージェントに任せる方法は、[承認の経路でエージェントに確かめさせる](agent-steps.ja.md)にあります。

## 1. プロジェクトに足す

[1. 動かして、申請を承認する](start.ja.md)で作ったプロジェクトに、`tools.go` を足します。新しいプロジェクトなら、`ritsu ringi init` に `--mcp` を付けると、この節のものが入った形で作られます。

```go title="tools.go"
package shinsei

import (
	"log/slog"
	"net/http"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/mcp"
	"github.com/i2y/ringi-server/serve"
)

// HandleTools は、エージェントのツールを /mcp に置く。
func HandleTools(st *serve.Stores, log *slog.Logger) {
	st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
		tools, err := mcp.New(srv, mcp.Config{Logger: log})
		if err != nil {
			return nil, err
		}
		return tools.Handler(mcp.HTTP{Person: serve.Person})
	})
}
```

`cmd/server/main.go` の、`stores.Serve` を呼ぶ行の前に一行足します。

```go title="cmd/server/main.go" hl_lines="1"
	shinsei.HandleTools(stores, log)
	return stores.Serve(ctx, cfg, func(srv *server.Server) { shinsei.Started(cfg, srv) })
```

モジュールを足して、起動します。

```sh
go get github.com/i2y/ringi-server/mcp@v0.1.0
go mod tidy
go run ./cmd/server
```

`-dev-login` を付けずに起動すると、リクエストのヘッダー `X-Person` に書いた人の操作になります。自分のコンピューターで試すための方法で、ほかのコンピューターからは使えません。

## 2. 試す

エージェントのアプリには、MCP のサーバーとして次の設定をします。

| 設定 | 値 |
|---|---|
| つなぎ方 | Streamable HTTP |
| URL | `http://127.0.0.1:8080/mcp` |
| 付けるヘッダー | `X-Person: s1201`（営業担当 1 として使う） |

アプリがなくても、curl と jq で呼べます。ツールを一つ呼んで、結果の文を表示する関数を作ります。

```sh
call() {  # call <人> <ツール> <引数>
  curl -s http://127.0.0.1:8080/mcp -H "X-Person: $1" -H 'Accept-Language: ja' \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$2\",\"arguments\":$3}}" |
    jq -r '.result.content[0].text'
}
FORM='{"amount":120000,"purpose":"新しいメンバーのノートパソコン"}'
call s1201 list_requests '{}'
call s1201 check_operation "{\"tool\":\"submit_purchase\",\"arguments\":{\"form\":$FORM}}"
call s1201 submit_purchase "{\"idempotency_key\":\"try-1\",\"form\":$FORM}"
call s1101 list_waiting '{}'
```

```
この人が今日出せる申請の種類は1種類です：purchase（購買の申請）。
何も保存していません。いま行うと、チケット（purchase）は in_progress になります。ステップは manager です。s1101 を待ちます。
受け付けました。チケット 01M89KPYFZKZDRAPZNJRYJQX34（purchase）は in_progress です。ステップは manager です。s1101 を待っています。
この人の答えを待っているチケットは、新しい順に次の1件です：01M89KPYFZKZDRAPZNJRYJQX34（purchase）。
```

二つ目の `check_operation` は、申請したらどうなるかを答えるだけで、何も保存しません。四つ目は、営業一課長として、自分を待っている申請を並べています。

## ツールの一覧

`<種類>` は、`.ringi` に書いた種類の ID です。

| ツール | すること |
|---|---|
| `list_requests` | その人が出せる申請の種類を並べる |
| `list_waiting`、`list_mine` | その人を待っている申請、その人の申請を、新しい順に並べる |
| `get_ticket` | 申請を一件読む |
| `submit_<種類>`、`resubmit_<種類>` | 申請する、再申請する |
| `edit_<種類>` | 承認者が入力する項目を書く |
| `worked_<種類>`、`ticket_worked_<種類>` | 途中まで入力したフォームの、合計などの計算をする |
| `approve`、`reject`、`send_back`、`retract`、`comment`、`withdraw`、`discard` | 承認、却下、差し戻し、承認の取り消し、コメント、取り下げ、申請をやめる |
| `get_organization`、`get_master_<一覧>` | 組織のデータ、選択肢の一覧を読む |
| `find_people`、`find_teams`、`find_master_<一覧>` | 人、チーム、選択肢を言葉で探す |
| `check_operation` | 申請を変えるツールを試し、どうなるかを答える。何も保存しない |

どの申請の種類も使わない操作には、ツールがありません。保留、承認者の追加、引き継ぎはページで行うことにしているので、これにもツールがありません。

サーバーはエージェントに、次の手順で使うように伝えます。

1. 申請の種類は `list_requests` で探す。
2. 変える前に `check_operation` で確かめる。
3. 申請を読み、そこにある `revision` を付けて操作する。読んだあとで申請が変わっていれば、サーバーは受け付けない。
4. 新しい操作には、新しい `idempotency_key` を付ける。同じ番号で送り直しても、実行は一度だけになる。

受け付けられない操作には、理由と、フォームのどの項目が原因かが返ります。

## 会社のログインと一緒に使う

`/mcp` は、ページと同じログインで人を見つけます。会社のログインを使うサーバーでは、エージェントのリクエストにも、ブラウザでログインしたときのクッキーが要ります。ログインは長くて 12 時間で切れるので、社員に使ってもらうのには向きません。

社員に使ってもらうときは、クッキーの代わりにベアラートークンを使います。`cmd/server` のサーバーでは、ログインのないリクエストは `/mcp` に届く前に止まるので、[自分のプログラムに組み込んだ](embedding.ja.md)サーバーに `/mcp` を置きます。トークンからその人を見つける関数を書き、`HTTP` の `Verify` に渡します。

```go
func verify(ctx context.Context, token string, r *http.Request) (mcp.Token, error) {
	p, expires, err := lookUp(ctx, token) // トークンを確かめて、だれかを返す関数を自分で書く
	if err != nil {
		return mcp.Token{}, fmt.Errorf("%w: %v", server.ErrNoPerson, err) // 401 になる
	}
	return mcp.Token{Person: p, Expires: expires}, nil
}
```

置き方と、一人分のツールを標準入出力（stdio）で出す方法は、[モジュール mcp](../mcp/README.ja.md) にあります。
