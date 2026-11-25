# Prepare it and start it

How to prepare a server for your staff, and start it. What comes after is in [Backups and raising
versions](maintenance.md), and in [Watch it, and when something is wrong](troubleshooting.md). Japanese:
[operations.ja.md](operations.ja.md).

![The browsers of your staff use the server through a proxy that takes https. There may be one process of the server or several. The requests, their history, the attachments, the timers and the record are in PostgreSQL. The sign-in is a provider of OpenID Connect, and the project's Notifier sends the notices by mail or to a chat. Temporal is connected only when it is used](images/deploy.en.svg)

Five things differ from trying it on your own computer.

| | On your own computer | For your staff |
|---|---|---|
| The sign-in | `-dev-login` (checks no one) | Your organization's (OpenID Connect) |
| Where the requests are kept | In memory, lost when it stops | PostgreSQL |
| Notices | Written to the log | Sent by mail or to a chat |
| The network | Inside your own computer | https |
| The organization's data | A sample company | Your own people and teams |

## 1. Prepare

| What | How |
|---|---|
| A PostgreSQL database | The server makes its tables itself, in the schema `ringi`. Give the user of the database the right to make a schema (`grant create on database ringi to ringi_server`). A pooler between the two has to be in session mode. The tests run on PostgreSQL 18 |
| Your organization's sign-in | Register a client, and have it pass the roles ([Connect the sign-in](sign-in.md)) |
| The organization's data | The CSV files of your company. `email` of `people.csv` is the address the sign-in service has verified ([Prepare the organization's data](organization.md)) |
| The business days | A calendar with your company's days off and the public holidays ([Set the business days](calendar.md)) |
| Notices | A notifier that sends mail or the like. Put your own address of the pages, sender and authentication of the mail server in the code of the example ([Sending through your own mail server](mail.md#sending-through-your-own-mail-server)) |
| https | A certificate for the server, or a proxy in front of it that takes https |
| The same time everywhere | Deadlines and the length of a sign-in are read from the clock of the server's computer. Keep it right |

## 2. Build it

The server is one executable file. The request types, the pages and the organization's data are built in, so it
needs no other file. When the computer it runs on has another system than the one you build on, give `GOOS` and
`GOARCH` (such as `GOOS=linux GOARCH=amd64`).

```sh
go test ./...
go build -o server ./cmd/server
```

## 3. Start it

Secrets go in environment variables, and the other settings in flags. `SMTP_ADDR` is for sending mail as in [the
tutorial](mail.md).

```sh
export RINGI_OIDC_CLIENT_SECRET='the secret from the sign-in service'
export RINGI_SESSION_KEYS='the key you keep'
export PGPASSWORD='the password of the database'
export SMTP_ADDR='mail.example.com:587'
./server -addr :8443 -tls-cert server.crt -tls-key server.key \
  -db 'postgres://ringi_server@db.example.com/ringi' \
  -oidc-issuer https://id.example.com -oidc-client-id ringi \
  -oidc-redirect-url https://ringi.example.com:8443/login/callback \
  -oidc-roles-claim roles
```

How to make the key is in [Connect the sign-in](sign-in.md#3-give-it-the-settings), and the list of the flags in
[Flags and settings](reference.md).

When it starts, the server makes the tables in the database, and imports the organization's data built into it. If a
setting is wrong, it writes why to the log and stops before it takes a request ([When it does not
start](troubleshooting.md#when-it-does-not-start)).

## 4. See that it works

There is no operation that deletes a request, so one made to try it stays. Give `-db` another, empty database to try
it, and put the real one in its place after that.

1. `https://<host>/a2ui/functions.js` answers 200 with no one signed in.
2. Opening the start page in a browser goes to your organization's sign-in, and comes back.
3. When you make a request, its approver gets a notice, and the link in it opens the request. Approve it to its end.
4. The start page of a person with a role that administers has the link "Administration".
5. After the server is started again, the request is still there.

## Start it by itself

Have it start when the computer does, and when it stopped. With systemd, put this file in place.

```ini title="/etc/systemd/system/ringi.service"
[Unit]
Description=ringi-server
Wants=network-online.target
After=network-online.target

[Service]
User=ringi
EnvironmentFile=/etc/ringi/env
ExecStart=/opt/ringi/server -addr :8443 -tls-cert /etc/ringi/server.crt -tls-key /etc/ringi/server.key \
  -db postgres://ringi_server@db.example.com/ringi \
  -oidc-issuer https://id.example.com -oidc-client-id ringi \
  -oidc-redirect-url https://ringi.example.com:8443/login/callback \
  -oidc-roles-claim roles
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Write the secrets in `/etc/ringi/env`, a `NAME=value` on each line, so that only the administrator can read it
(`chmod 600`).

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now ringi
journalctl -u ringi -f
```

In a container, too, all it needs is the executable file, and the environment variables and flags above.

## Behind a proxy

When a proxy takes https, start the server on an address only the proxy reaches, without `-tls-cert` and
`-tls-key`. `-oidc-redirect-url` is the address your staff use.

Set up the proxy like this.

- Pass the `Host` header on as it came.
- Send people who came by http to https. The cookie of the sign-in is sent over https only.
- Let a body of 64 MiB through (the import of the organization's data), and wait 30 seconds for an answer.

Never put a proxy in front of a server with `-dev-login` or the header `X-Person`. They check no one.

## Several processes

The same executable file can be started any number of times, with the same database and the same key of the
cookies. Any process answers any request. Nothing has to connect them to each other.

- A deadline or a notice is taken by one process at a time. When that process stops, another takes over a deadline
  after 30 seconds, and a notice or the start of some work after 60 seconds.
- Then a notice or the like may be sent once more. The same one has the same key, so write the part so that it does
  not do it a second time ([Parts and packages](parts.md#the-same-thing-given-twice)).
- Signing out holds in every process at once. A switch of the version of a request type, and an import of the
  organization's data, reach every process within 30 seconds.

## Change the secrets and the certificate

| What | How |
|---|---|
| The key of the cookies (everyone stays signed in) | 1. Make it `old key,new key`, and start every process again. 2. Make it `new key,old key`, and start every process again. 3. After 12 hours, make it the new key alone, and start them again. With one process, you need not do 1 |
| A key of the cookies that leaked | Make it the new key alone, and start every process again. Everyone signs in again |
| The client's secret | Change it at the sign-in service and in `RINGI_OIDC_CLIENT_SECRET`, and start the server again. People who are signed in stay so |
| The certificate | Change the files, and start the server again. It reads them when it starts |

## Stop it

`Ctrl+C` (`SIGINT`) or `SIGTERM` stops it. It waits for the requests being answered, for 10 seconds at the most. The
deadlines that pass while it is stopped are dealt with as soon as it is started again.

## Next

- To keep the data safe, and raise the versions: [Backups and raising versions](maintenance.md)
- What to watch, and what to do about an error: [Watch it, and when something is wrong](troubleshooting.md)
