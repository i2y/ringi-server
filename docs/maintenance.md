# Backups and raising versions

How to keep the data of a running server safe, and how to bring it up to date. Japanese:
[maintenance.ja.md](maintenance.ja.md).

## Back it up

All of this is in the database.

- The requests and their history, and the attachments
- The timers of the deadlines, and the notices and the work not yet sent
- The organization's data, and the record of the administration
- The books of the budgets and the like, when they are in the same database

Back up the whole database at once, so that nothing disagrees.

```sh
pg_dump --format=custom --file=ringi.dump ringi
```

Keep the key of the cookies (`RINGI_SESSION_KEYS`) and the Git repository of the project too, apart from it.

## Restore it

1. Stop every process of the server.
2. Make an empty database, give the right, and restore into it.

    ```sh
    createdb ringi_restored
    psql -d ringi_restored -c 'grant create on database ringi_restored to ringi_server'
    pg_restore --dbname=ringi_restored --exit-on-error ringi.dump
    ```

3. Start the server with the new database as `-db`.

Restoring puts everything back to the time of the backup. Know this before you start the server.

| What | What becomes of it |
|---|---|
| Requests and approvals after the backup | They are gone, and have to be made again |
| Deadlines that passed after the backup | They are dealt with as soon as it starts. A request approved in the meantime waits again after the restore, so it may expire |
| Notices and work not yet sent at the time of the backup | The server sends them when it starts. Those that were sent after the backup are sent once more, with the same key. A receiver that remembers the keys does nothing twice |
| A payment or the like that started after the backup | It is not undone. When that request is approved again, it may start once more |

Before you start the server, this SQL says how many deadlines and reminders it will deal with at once.

```sql
select count(*) from ringi.timers where at < now();
```

## Raising versions

### ringi-server and ritsu

Raise the two together. [The table of versions](../README.md#versions) says which go together.

1. In the project, raise ritsu and ringi-server, and make the code again. Put the version you raise to in place of
   `v0.1.0`.

    ```sh
    brew upgrade ritsu
    go get github.com/i2y/ringi-server@v0.1.0
    go generate ./...
    go mod tidy
    go test ./...
    ```

    If you use the modules `mcp` and `temporal`, raise them to the same version.

2. Stop every process of the old server.
3. Back up the database.
4. Start the new server. When it starts, it brings the tables of the database up to date, in one transaction.

An old server does not start on tables that were brought up to date. To go back to the version before, restore the
backup.

### Put in a server with changed request types

When you changed the request types, the calendar, the permissions or the Go code, make the server again and put it in
place. For a change that is not to touch the requests in progress, [raise the version of the request
type](types.md#change-the-rules-of-a-type-in-use) first.

1. Check it, make the code again, test it and build it.

    ```sh
    ritsu check project
    go generate ./...
    go test ./...
    go build -o server ./cmd/server
    ```

2. Stop every process of the old server.
3. Start the new server.

The deadlines that pass while it is stopped are dealt with when it starts.

If old and new processes run together, an old one cannot open a request of a new type or version (it is the server's
error, 500). The same happens when you go back to the old executable file after a request of a new version was made.

### Take out the code of an old version

The code of an old version cannot be taken out while there is even one request of it, as opening a request that has
ended uses the code of its version too. There is no operation that deletes a request, so the code of a version that
was ever used stays for good.

"Tickets (open / all)" on the page for administrators says how many requests there are of which version. In the
database, this SQL does.

```sql
select kind, version, count(*) as all_tickets,
       count(*) filter (where status in ('in_progress', 'paused', 'with_requester')) as in_progress
from ringi.tickets group by kind, version order by kind, version;
```

## The organization's data

The data of people who moved or joined is imported into a running server, on the pages for administrators ([Importing
new data of the organization](use/admin.md#importing-new-data-of-the-organization)). You need not stop the server.

Add the same to `project/org/` of the project. When the server starts, it treats the data built into it like this.

| The data built in | When it starts |
|---|---|
| The same as what was built in before | It is not imported. The data imported on the pages for administrators stays in use |
| Not the same as what was built in before | It is imported, in place of the data imported on the pages for administrators |

If you forget to put what was imported on the pages in `project/org/`, it is gone the next time you change
`project/org/`.

## What is kept, and for how long

The server deletes what is past its time itself, when it starts and every hour.

| What | How long |
|---|---|
| The requests and their history, the attachments a request holds, the organization's data, the record of the administration, and the record of the notices and the work that are done | For good |
| An attachment no request holds | 7 days |
| The number of an operation and its answer | 30 days. Sent again with the same number in that time, it gets the same answer |
| Data of the organization that was checked and not imported | a day |
| The record of a session that was signed out of | Until that session would have ended |

There is no operation that deletes a request and its history. The SQL in [Look into the
database](troubleshooting.md#look-into-the-database) says how large the tables have become.
