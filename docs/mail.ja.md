# 3. 通知をメールで送る

承認を待っていることを、承認する人にメールで知らせます。いまのプロジェクトは、通知をログに書くだけです。メールを送る部品を書いて、サーバーに渡します。Go のコードを書くのは、チュートリアルの中でここだけです。15 分ほどで終わります。英語版は [mail.md](mail.md) です。

## サーバーと部品の分担

だれに、いつ、どの申請について知らせるかは、サーバーが決めます。送り方は会社ごとに違うので、プロジェクトが部品として用意します。サーバーは、知らせることがあるたびに部品の `Notify` を呼びます。

この部品を Notifier と呼びます。プロジェクトに最初から入っている Notifier は `notify.go` の `LogNotifier` で、受け取った通知をログに書きます。

## 試すためのメールサーバーを用意する

手元で試せるように、届いたメールを表示するだけのメールサーバーを作ります。新しいファイル `cmd/mailbox/main.go` に、次のとおり書きます。中身は読まなくてかまいません。使えるメールサーバーがすでにあれば、この節は飛ばせます。

```go title="cmd/mailbox/main.go"
// mailbox は、届いたメールを表示するだけのメールサーバー。手元で試すときに使う。
package main

import (
	"bufio"
	"fmt"
	"log"
	"mime"
	"net"
	"strings"
)

func main() {
	l, err := net.Listen("tcp", "127.0.0.1:1025")
	if err != nil {
		log.Fatal(err)
	}
	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go receive(c)
	}
}

// receive は、一つの接続でメールを受け取る。
func receive(c net.Conn) {
	defer c.Close()
	answer := func(s string) { fmt.Fprintf(c, "%s\r\n", s) }
	answer("220 mailbox")
	in := bufio.NewScanner(c)
	for reading := false; in.Scan(); {
		line := in.Text()
		switch {
		case reading && line == ".":
			reading = false
			fmt.Println("----")
			answer("250 ok")
		case reading:
			if s, err := new(mime.WordDecoder).DecodeHeader(line); err == nil {
				line = s
			}
			fmt.Println(line)
		case strings.HasPrefix(line, "DATA"):
			reading = true
			answer("354 send the mail")
		case strings.HasPrefix(line, "QUIT"):
			answer("221 bye")
			return
		default:
			answer("250 ok")
		}
	}
}
```

別の端末を開き、プロジェクトのディレクトリで起動しておきます。

```sh
go run ./cmd/mailbox
```

## メールを送る Notifier を書く

新しいファイル `mail.go` に書きます。

```go title="mail.go"
package shinsei

import (
	"context"
	"fmt"
	"mime"
	"net/smtp"

	server "github.com/i2y/ringi-server"
)

// MailNotifier は、通知をメールで送る。
type MailNotifier struct {
	Server *server.Server // 申請の種類の名前を尋ねる。Started が入れる
	Addr   string         // メールを送る SMTP サーバー（ホスト:ポート）
	From   string         // 差出人
	Pages  string         // ページの URL
}

// subjects は、通知ごとの件名。
var subjects = map[string]map[string]string{
	"ja": {
		"waiting":  "承認をお願いします",
		"reminder": "承認を待っています",
		"returned": "申請が申請者に戻りました",
		"decided":  "申請の結果が出ました",
	},
	"en": {
		"waiting":  "Please approve",
		"reminder": "Waiting for your approval",
		"returned": "The request is back with its requester",
		"decided":  "Your request was decided",
	},
}

// Notify は、通知を受け取る一人一人に、その人の言語でメールを送る。
func (n *MailNotifier) Notify(_ context.Context, x server.Notice) error {
	for _, r := range x.To {
		if r.Email == "" {
			continue
		}
		title, _ := n.Server.Title(x.Kind, x.Version, r.Language)
		subject := subjects[r.Language][x.Notice]
		if subject == "" {
			subject = x.Notice
		}
		mail := "From: " + n.From + "\n" +
			"To: " + r.Email + "\n" +
			"Subject: " + mime.BEncoding.Encode("utf-8", title+": "+subject) + "\n" +
			"Message-ID: <" + x.Key.String() + "." + r.ID + "@ringi.example.com>\n" +
			"Content-Type: text/plain; charset=utf-8\n" +
			"\n" +
			fmt.Sprintf("%s/tickets/%s?lang=%s\n", n.Pages, x.Ticket, r.Language)
		if err := smtp.SendMail(n.Addr, nil, n.From, []string{r.Email}, []byte(mail)); err != nil {
			return err // サーバーが、あとでもう一度呼ぶ
		}
	}
	return nil
}
```

`Notify` が受け取る `server.Notice` には、次のものが入っています。

| フィールド | 内容 |
|---|---|
| `Notice` | 何の知らせか。`waiting` は承認待ち、`reminder` は催促、`returned` は申請者に戻ったこと、`decided` は結果。ほかに六つある（[通知の種類](notices.ja.md#通知の種類)） |
| `Kind`、`Version` | 申請の種類と、その版 |
| `Ticket` | 申請の番号。ページの URL は `/pages/tickets/` に番号を続けたもの |
| `To` | 知らせる人。`ID`、`Name`、`Email`、`Language` は、会社のデータのもの |
| `Key` | 通知の番号。やり直しのときも同じ番号になる |

## サーバーに渡す

`cmd/server/main.go` の `run` に三行足します。環境変数 `SMTP_ADDR` があればメールで送り、なければこれまでどおりログに書きます。

```go title="cmd/server/main.go" hl_lines="13 14 15"
func run(flags serve.Flags, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stores, err := serve.Open(ctx, flags, log)
	if err != nil {
		return err
	}
	defer stores.Close()
	cfg, err := shinsei.Parts(log)
	if err != nil {
		return err
	}
	if addr := os.Getenv("SMTP_ADDR"); addr != "" {
		cfg.Notifier = &shinsei.MailNotifier{Addr: addr, From: "ringi@example.com", Pages: "http://127.0.0.1:8080/pages"}
	}
	return stores.Serve(ctx, cfg, func(srv *server.Server) { shinsei.Started(cfg, srv) })
}
```

`shinsei.go` の `Started` は、サーバーができたあとに呼ばれ、Notifier にサーバーを渡します。`MailNotifier` にも渡すように書き換えます。

```go title="shinsei.go" hl_lines="2 3 4 5 6 7"
func Started(cfg server.Config, srv *server.Server) {
	switch n := cfg.Notifier.(type) {
	case *LogNotifier:
		n.Server = srv
	case *MailNotifier:
		n.Server = srv
	}
}
```

## メールを受け取る

メールサーバーのアドレスを渡して、サーバーを起動します。

```sh
export SMTP_ADDR=127.0.0.1:1025
go run ./cmd/server -dev-login
```

「営業担当 1（s1201）」として 120,000 円の購買を申請すると、`mailbox` の端末に、営業一課長へのメールが出ます。

```
From: ringi@example.com
To: s1101@example.com
Subject: 購買の申請: 承認をお願いします
Message-ID: <01M89JKTDYFM2C2C9SJBH2MDAC/1/2.s1101@ringi.example.com>
Content-Type: text/plain; charset=utf-8

http://127.0.0.1:8080/pages/tickets/01M89JKTDYFM2C2C9SJBH2MDAC?lang=ja
----
```

## メールサーバーが止まっているとき

`mailbox` を `Ctrl+C` で止めてから、「営業一課長（s1101）」として承認してみます。承認はふだんどおり済み、申請は部長のステップに進みます。サーバーのログには、メールを送れなかったことが出ます。

```
level=WARN msg="a thing to do failed; it is tried again" id=01M89JKTDYFM2C2C9SJBH2MDAC/1/8 what=notify tries=1 in=2s err="dial tcp 127.0.0.1:1025: connect: connection refused"
level=WARN msg="a thing to do failed; it is tried again" id=01M89JKTDYFM2C2C9SJBH2MDAC/1/8 what=notify tries=2 in=4s err="dial tcp 127.0.0.1:1025: connect: connection refused"
```

サーバーは、送れなかった通知を覚えています。2 秒後、4 秒後、8 秒後と間隔を延ばしながら（最長 10 分）、届くまでやり直します。`mailbox` をもう一度起動すると、しばらくして、営業部長へのメールが届きます。

やり直しがあるので、同じ通知が二度届くこともあります。たとえば、二人に知らせる通知で二人目に失敗すると、次は一人目にも送ります。`Message-ID` を通知の番号から作ったのは、同じメールかどうかを受け取る側で見分けられるようにするためです。一度だけ送る方法は、[通知の送り方を変える](notices.ja.md)にあります。

## 会社のメールサーバーで送るとき

この例のままでは、社員に使ってもらうサーバーにはなりません。次のところを替えます。

| 替えるところ | 理由 |
|---|---|
| `smtp.SendMail` の二番目の引数 `nil` | メールサーバーに認証が要るなら、`smtp.PlainAuth` の値を渡す。パスワードは環境変数から読む |
| `From` と `Message-ID` の `ringi@example.com` | 会社のアドレスにする |
| `Pages` | 社員が開くページのアドレスにする。本文のリンクになる |
| 件名のない通知 | `subjects` にない通知は、件名が `comment` のような名前になる。[十種類](notices.ja.md#通知の種類)の件名をそろえる |
| `stopped` の通知 | 承認者がいなくて止まった、という知らせで、宛先が空。だれに知らせるかを決めて送る（[止まった申請を知らせる](notices.ja.md#止まった申請を知らせる)） |

## 次へ

チュートリアルはここまでです。購買の申請を作り、決まりを変え、メールを送るところまで動きました。

- 言葉としくみを整理する：[しくみと言葉](concepts.ja.md)
- 二つ目の申請の種類を作る：[申請の種類を足す、変える](types.ja.md)
- 見本の会社を、自分の会社に替える：[組織のデータを用意する](organization.ja.md)
- 会社のログインを使い、社員に開放する：[ログインをつなぐ](sign-in.ja.md)、[用意して起動する](operations.ja.md)
