# 3. Send the notices by mail

You tell an approver by mail that a request waits for them. So far, the project only writes its notices to
the log. You write a part that sends mail, and give it to the server. This is the only place in the
tutorial where you write Go. It takes about fifteen minutes. Japanese: [mail.ja.md](mail.ja.md).

## What the server does, and what the part does

The server decides whom to tell, when, and about which request. How a notice is sent differs from company
to company, so that is a part of your project. Whenever there is something to tell, the server calls the
part's `Notify`.

This part is called the notifier. The one the project came with is `LogNotifier` in `notify.go`, which
writes each notice it is given to the log.

## Prepare a mail server to try it with

To try it on your own computer, make a mail server that only prints the mail it gets, in a new file
`cmd/mailbox/main.go`. You need not read what is in it. Skip this section if you have a mail server you
can use.

```go title="cmd/mailbox/main.go"
// mailbox is a mail server that prints the mail it gets, to try things with.
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

// receive takes the mail of one connection.
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

Open another terminal, and start it in the project's directory.

```sh
go run ./cmd/mailbox
```

## Write a notifier that sends mail

It goes in a new file, `mail.go`.

```go title="mail.go"
package approvals

import (
	"context"
	"fmt"
	"mime"
	"net/smtp"

	server "github.com/i2y/ringi-server"
)

// MailNotifier sends the notices by mail.
type MailNotifier struct {
	Server *server.Server // asked for the names of the request types; Started sets it
	Addr   string         // the SMTP server that sends the mail (host:port)
	From   string         // the sender
	Pages  string         // the URL of the pages
}

// subjects are the subjects of the notices.
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

// Notify sends a mail to each person a notice is for, in their language.
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
			return err // the server calls again later
		}
	}
	return nil
}
```

The `server.Notice` that `Notify` is given has these fields.

| Field | What it is |
|---|---|
| `Notice` | What it tells: `waiting` that a request waits, `reminder` that it still does, `returned` that it is back with its requester, `decided` what became of it. There are six more ([the kinds of notice](notices.md#the-kinds-of-notice)) |
| `Kind`, `Version` | The request type, and its version |
| `Ticket` | The ID of the request. Its page is `/pages/tickets/` and the ID |
| `To` | Whom to tell. Their `ID`, `Name`, `Email` and `Language` are from the company's data |
| `Key` | The number of the notice, the same when it is tried again |

## Give it to the server

Add three lines to `run` in `cmd/server/main.go`. With the environment variable `SMTP_ADDR`, the notices
go by mail. Without it, they go to the log as before.

```go title="cmd/server/main.go" hl_lines="13 14 15"
func run(flags serve.Flags, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stores, err := serve.Open(ctx, flags, log)
	if err != nil {
		return err
	}
	defer stores.Close()
	cfg, err := approvals.Parts(log)
	if err != nil {
		return err
	}
	if addr := os.Getenv("SMTP_ADDR"); addr != "" {
		cfg.Notifier = &approvals.MailNotifier{Addr: addr, From: "ringi@example.com", Pages: "http://127.0.0.1:8080/pages"}
	}
	return stores.Serve(ctx, cfg, func(srv *server.Server) { approvals.Started(cfg, srv) })
}
```

`Started` in `approvals.go` is called once the server is made, and gives the notifier the server. Change
it to give it to a `MailNotifier` too.

```go title="approvals.go" hl_lines="2 3 4 5 6 7"
func Started(cfg server.Config, srv *server.Server) {
	switch n := cfg.Notifier.(type) {
	case *LogNotifier:
		n.Server = srv
	case *MailNotifier:
		n.Server = srv
	}
}
```

## Get a mail

Start the server with the mail server's address.

```sh
export SMTP_ADDR=127.0.0.1:1025
go run ./cmd/server -dev-login
```

Make a purchase request of 120,000 yen as "Sales staff 1 (s1201)", and the terminal of `mailbox` shows the
mail to the manager of Sales Section 1.

```
From: ringi@example.com
To: s1101@example.com
Subject: Purchase request: Please approve
Message-ID: <01M89JKTDYFM2C2C9SJBH2MDAC/1/2.s1101@ringi.example.com>
Content-Type: text/plain; charset=utf-8

http://127.0.0.1:8080/pages/tickets/01M89JKTDYFM2C2C9SJBH2MDAC?lang=en
----
```

## When the mail server is down

Stop `mailbox` with `Ctrl+C`, and approve the request as "Manager of Sales Section 1 (s1101)". The approval
is done as usual, and the request goes on to the head's stage. The server's log says that the mail could
not be sent.

```
level=WARN msg="a thing to do failed; it is tried again" id=01M89JKTDYFM2C2C9SJBH2MDAC/1/8 what=notify tries=1 in=2s err="dial tcp 127.0.0.1:1025: connect: connection refused"
level=WARN msg="a thing to do failed; it is tried again" id=01M89JKTDYFM2C2C9SJBH2MDAC/1/8 what=notify tries=2 in=4s err="dial tcp 127.0.0.1:1025: connect: connection refused"
```

The server remembers the notice, and tries again after 2 seconds, 4 seconds, 8 seconds and so on, up to 10
minutes apart. Start `mailbox` again, and after a while the mail to the Head of Sales arrives.

As it tries again, the same notice can arrive twice. When a notice is for two people and the second mail
fails, the next try sends the first one again too. The `Message-ID` is made from the number of the notice,
so that whoever gets the mail can tell that it is the same. [Change how notices are sent](notices.md) says
how to send each only once.

## Sending through your own mail server

As it stands, this example will not do for a server your staff use. Change these:

| What to change | Why |
|---|---|
| The second argument of `smtp.SendMail`, `nil` | A mail server that asks who sends takes the value of `smtp.PlainAuth`. Read the password from an environment variable |
| `ringi@example.com` in `From` and `Message-ID` | It is to be an address of yours |
| `Pages` | It is to be the address of the pages your staff open. The link in the mail is made of it |
| The notices with no subject | A notice that is not in `subjects` has a name like `comment` as its subject. Give each of the [ten kinds](notices.md#the-kinds-of-notice) its own |
| The notice `stopped` | It says that a request stopped with no approver, and is for no one. Decide whom to tell, and send it to them ([Tell someone that a request stopped](notices.md#tell-someone-that-a-request-stopped)) |

## Next

The tutorial ends here. You have made a purchase request, changed its rules, and sent mail.

- The words, and how it works: [How it works, and its words](concepts.md)
- A second request type: [Add and change request types](types.md)
- Your own company in place of the sample one: [Prepare the organization's data](organization.md)
- Your own sign-in, and a server your staff can use: [Connect the sign-in](sign-in.md), [Prepare it and
  start it](operations.md)
