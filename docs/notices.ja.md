# 通知の送り方を変える

承認の依頼、催促、結果などの通知を、メールやチャットに送ります。書くのは、`Notify` という関数一つです。`ritsu ringi init` が書くプロジェクトは、通知をログに出すだけです。英語版は [notices.md](notices.md) です。

メールで送る手順は、[3. 通知をメールで送る](mail.ja.md)にあります。

## 通知の種類

通知は十種類あります。「出した人」は、代理として申請を出した人です（申請者が自分で出した申請には、いません）。

| `Notice` | いつ | だれに |
|---|---|---|
| `waiting` | ステップが始まったとき。承認者を加えたとき、引き継いだとき | まだ答えていない承認者と、その代理 |
| `reminder` | 催促の時刻になったとき | まだ答えていない承認者と、その代理 |
| `went_up` | 期限を過ぎて、上の人に回ったとき | 新しい承認者と、その代理 |
| `returned` | 差し戻されたとき | 申請者と、出した人 |
| `returned` | 申請者が取り下げたとき | 待っていた承認者と、その代理 |
| `decided` | 承認、却下、期限切れで終わったとき。申請をやめたとき | 申請者と、出した人 |
| `circulation` | 回覧のステップに来たとき | 回覧の相手 |
| `paused` | 保留になったとき | 申請者と、出した人 |
| `resumed` | 保留が解けたとき | 申請者と、出した人、承認者 |
| `comment` | コメントが付いたとき | 申請者、出した人、いまの承認者、差し戻した人。書いた人を除く |
| `stopped` | 承認者がいなくて止まったとき | 宛先は空。だれに知らせるかは、自分で決める |

## 受け取るもの

```go
type Notice struct {
	Key     EffectKey   // この通知のキー。同じキーなら、同じ通知
	Kind    string      // 申請の種類
	Version int         // その版
	Ticket  string      // 申請の番号
	Notice  string      // 上の表のどれか
	Stage   string      // どのステップか。なければ空
	To      []Recipient // 知らせる人
	Owner   string      // 申請の種類を受け持つチームの ID
}

type Recipient struct {
	ID, Name, Email string
	Language        string // その人の言語。en か ja
}
```

申請の種類の題名は、`Server.Title(Kind, Version, Language)` が、その人の言語で返します。申請のページのアドレスは、`<サーバーのアドレス>/pages/tickets/<Ticket>` です。

## チャットに送る

URL に JSON を送ると投稿されるチャットなら、次のように書きます。

```go title="chat.go"
package shinsei

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	server "github.com/i2y/ringi-server"
)

// ChatNotifier は、通知をチャットに送る。
type ChatNotifier struct {
	Server *server.Server // 題名と、組織のデータを尋ねる。Started が入れる
	URL    string         // チャットの URL
	Pages  string         // ページの URL
}

// Notify は、通知を受け取る一人一人に送る。
func (n *ChatNotifier) Notify(ctx context.Context, x server.Notice) error {
	to, err := told(ctx, n.Server, x)
	if err != nil {
		return err
	}
	for _, r := range to {
		title, _ := n.Server.Title(x.Kind, x.Version, r.Language)
		body, err := json.Marshal(map[string]string{
			"to":   r.Email,
			"text": fmt.Sprintf("%s (%s): %s/tickets/%s", title, x.Notice, n.Pages, x.Ticket),
		})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", x.Key.String()+"."+r.ID)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err // サーバーが、あとでもう一度呼ぶ
		}
		res.Body.Close()
		if res.StatusCode >= 300 {
			return fmt.Errorf("the chat answered %s", res.Status)
		}
	}
	return nil
}

// told は、知らせる人。止まった申請の通知には宛先がないので、受け持つチームの長に知らせる。
func told(ctx context.Context, srv *server.Server, x server.Notice) ([]server.Recipient, error) {
	if x.Notice != "stopped" {
		return x.To, nil
	}
	teams, err := srv.Teams(ctx)
	if err != nil {
		return nil, err
	}
	people, err := srv.People(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range teams {
		for _, p := range people {
			if t.ID == x.Owner && p.ID == t.Head {
				return []server.Recipient{{ID: p.ID, Name: p.Name, Email: p.Email, Language: p.Language}}, nil
			}
		}
	}
	return nil, fmt.Errorf("the team %s has no head to tell", x.Owner)
}
```

`shinsei.go` で、`os` を読み込み、`Parts` の `Notifier` を替え、`Started` でサーバーを渡します。

```go title="shinsei.go" hl_lines="5"
import (
	"embed"
	"io/fs"
	"log/slog"
	"os"

	server "github.com/i2y/ringi-server"
```

```go title="shinsei.go" hl_lines="5"
	return server.Config{
		Packages:     []*server.Package{p},
		Gate:         &Gate{Log: log},
		Organization: Organization(),
		Notifier:     &ChatNotifier{URL: os.Getenv("CHAT_URL"), Pages: "https://ringi.example.com:8443/pages"},
	}, nil
```

```go title="shinsei.go" hl_lines="2"
func Started(cfg server.Config, srv *server.Server) {
	if n, ok := cfg.Notifier.(*ChatNotifier); ok {
		n.Server = srv
	}
}
```

## 止まった申請を知らせる

承認者になれる人がいなくて申請が止まると、サーバーは `stopped` の通知を作ります。宛先は空なので、`x.To` だけを見る Notifier では、だれにも知らされません。

上の `told` は、`Owner`（申請の種類を受け持つチーム）の長に知らせます。管理の役割を持つ人に知らせたくても、役割はログインのサービスの側にあるので、サーバーには分かりません。総務のメーリングリストのような決まった宛先に送るのも、よい方法です。

知らせを受けた人は、申請のページで承認者を加えます（[答えられない承認者を替える](use/admin.ja.md#答えられない承認者を替える)）。

## 失敗したとき、二度呼ばれたとき

`Notify` がエラーを返すと、サーバーは 2 秒後、4 秒後、8 秒後と間隔を延ばしながら（最長 10 分）、成功するまで呼び直します。そのあいだも、申請は先へ進みます。

送ったのに結果が分からなかったときも、同じ `Key` でもう一度呼ばれます。同じ通知が一人に二通届くことへの備え方は、三つあります。

| 送る先 | 備え方 |
|---|---|
| キーを見て、二通目を捨てられる | `Key` と人の ID をつないだものを、キーとして渡す。上の `Idempotency-Key` や、メールの `Message-ID` |
| 捨てられない | 送った `Key` と人の ID をデータベースの表に残し、表にあれば送らない |
| まれに二通届いてもよい | 何もしない |

`Notify` は、いくつもの通知について同時に呼ばれます。`ctx` が終わったら、送るのをやめて戻るように書きます。標準ライブラリの `smtp.SendMail` は `ctx` を受け取らないので、メールサーバーの応答が遅いときは、そのあいだ待つことになります。
