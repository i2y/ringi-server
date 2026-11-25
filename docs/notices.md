# Change how notices are sent

You can send the notices, such as that a request waits, a reminder, or what became of a request, by mail or to
a chat. What you write is one function, `Notify`. The project `ritsu ringi init` writes only writes them to the
log. Japanese: [notices.ja.md](notices.ja.md).

[3. Send the notices by mail](mail.md) sends them by mail, step by step.

## The kinds of notice

There are ten kinds. "The one who made it" is the proxy who made a request for someone (there is none when the
requester made it).

| `Notice` | When | To whom |
|---|---|---|
| `waiting` | A stage started. An approver was added, or a place handed over | The approvers who have not answered, and their proxies |
| `reminder` | It is time to remind | The approvers who have not answered, and their proxies |
| `went_up` | Past the deadline, it went to the person above | The new approver, and their proxies |
| `returned` | It was sent back | The requester, and the one who made it |
| `returned` | The requester withdrew it | The approvers it waited for, and their proxies |
| `decided` | It ended approved, rejected or expired, or was discarded | The requester, and the one who made it |
| `circulation` | It came to a stage of circulation | Those it is circulated to |
| `paused` | It was put on hold | The requester, and the one who made it |
| `resumed` | It was taken off hold | The requester, the one who made it, and the approvers |
| `comment` | Someone commented | The requester, the one who made it, the approvers now, and the one who sent it back, except the one who wrote it |
| `stopped` | It stopped, with no approver | To no one. You decide whom to tell |

## What it is given

```go
type Notice struct {
	Key     EffectKey   // the key of this notice: the same key is the same notice
	Kind    string      // the request type
	Version int         // its version
	Ticket  string      // the ID of the request
	Notice  string      // one of the table above
	Stage   string      // the stage, or ""
	To      []Recipient // whom to tell
	Owner   string      // the ID of the team that keeps the request type
}

type Recipient struct {
	ID, Name, Email string
	Language        string // their language: en or ja
}
```

`Server.Title(Kind, Version, Language)` answers the title of the request type in a person's language. The page
of a request is at `<the server's address>/pages/tickets/<Ticket>`.

## Send them to a chat

For a chat that posts the JSON sent to a URL, write it like this.

```go title="chat.go"
package approvals

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	server "github.com/i2y/ringi-server"
)

// ChatNotifier sends the notices to a chat.
type ChatNotifier struct {
	Server *server.Server // asked for the titles and the organization's data; Started sets it
	URL    string         // the URL of the chat
	Pages  string         // the URL of the pages
}

// Notify sends a notice to each person it is for.
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
			return err // the server calls again later
		}
		res.Body.Close()
		if res.StatusCode >= 300 {
			return fmt.Errorf("the chat answered %s", res.Status)
		}
	}
	return nil
}

// told is whom to tell. The notice of a request that stopped is for no one: the head of the team that
// keeps the request type is told.
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

In `approvals.go`, import `os`, change the `Notifier` of `Parts`, and give it the server in `Started`.

```go title="approvals.go" hl_lines="5"
import (
	"embed"
	"io/fs"
	"log/slog"
	"os"

	server "github.com/i2y/ringi-server"
```

```go title="approvals.go" hl_lines="5"
	return server.Config{
		Packages:     []*server.Package{p},
		Gate:         &Gate{Log: log},
		Organization: Organization(),
		Notifier:     &ChatNotifier{URL: os.Getenv("CHAT_URL"), Pages: "https://ringi.example.com:8443/pages"},
	}, nil
```

```go title="approvals.go" hl_lines="2"
func Started(cfg server.Config, srv *server.Server) {
	if n, ok := cfg.Notifier.(*ChatNotifier); ok {
		n.Server = srv
	}
}
```

## Tell someone that a request stopped

When a request stops because no one can be its approver, the server makes a notice `stopped`. It is for no one,
so a notifier that looks only at `x.To` tells no one.

`told` above tells the head of `Owner` (the team that keeps the request type). You may want to tell the people
with a role that administers, but the roles are at the sign-in service, and the server does not know who has
them. Sending it to a fixed address, such as a mailing list of general affairs, is a good way too.

The person who is told adds an approver on the page of the request ([Replacing an approver who cannot
answer](use/admin.md#replacing-an-approver-who-cannot-answer)).

## When it fails, and when it is called twice

When `Notify` returns an error, the server calls it again after 2 seconds, 4 seconds, 8 seconds and so on, up to
10 minutes apart, until it succeeds. The request goes on in the meantime.

It is called again with the same `Key` too when it sent something but could not tell whether it arrived. There
are three ways to be ready for the same notice reaching a person twice.

| What it is sent to | How to be ready |
|---|---|
| Something that sees the key, and can throw the second away | Give it the `Key` and the person's ID, put together, as a key: the `Idempotency-Key` above, or the `Message-ID` of a mail |
| Something that cannot | Keep each `Key` and person's ID that was sent in a table of a database, and do not send those in it |
| Two now and then do no harm | Do nothing |

`Notify` is called for several notices at once. Write it so that it stops and returns when `ctx` ends.
`smtp.SendMail` of the standard library takes no `ctx`, so it waits as long as a slow mail server takes.
