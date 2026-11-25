# Watch it, and when something is wrong

How to watch a running server, and how to find out what is wrong. Japanese:
[troubleshooting.ja.md](troubleshooting.ja.md).

## When someone on the staff asks

| What they say | Why, and what to do |
|---|---|
| They cannot sign in | Most often, their mail address is not in the organization's data. The line `oidc: a sign-in refused` of the log says why ([What a person who signs in sees](sign-in.md#what-a-person-who-signs-in-sees)) |
| They have to sign in again | A person stays signed in until 12 hours after signing in, or after 2 hours unused. When it ends sooner, see whether the processes have different keys of the cookies |
| A request type is not there | One of these: they may not make it (`submitted by`); its version has not started, or was stopped; they are not in the organization's data of that day |
| It went to the wrong person | Look at the organization's data of that day. `ritsu ringi simulate <file> --requester <ID> --on <day>` says whom it goes to (it uses the data in `project/org/`, so see first that it is the same as what was imported) |
| There is no button to approve | They are neither an approver of the stage it is at nor a proxy of one. "Route" on the page of the request says whom it waits for. It can also be the rule that no one approves their own request |
| The page of a request says "Not found" | They may not see that request ([Who can see a request](concepts.md#who-can-see-a-request)) |
| No notice comes | [When a notice does not arrive](#when-a-notice-does-not-arrive) |
| It is past its deadline, and does not end | See whether the server is running, and whether the log has the line `a timer was refused` |
| It says "The request could not be dealt with." | This happens when a request type was changed without raising its version, with requests of it in progress. Put the names of the stages back, or [raise the version](types.md#change-the-rules-of-a-type-in-use) and give the server both |
| It says "The server failed" | Look at the line `an operation failed` of the log. When the code of an old version was taken out, its `err` has `no package has the kind …` |

[Why something is refused](refusals.md) says what the other words on the pages mean.

## When a notice does not arrive

Look at the log, in this order.

1. There is a line `msg=notice`. No part that sends has been given, so it is only written to the log. Give the
   server a notifier that sends ([Send the notices by mail](mail.md)).
2. A line `msg="a thing to do failed; it is tried again"` has `what=notify`. Sending it fails. Once you put right
   what `err` says, it arrives the next time it is tried. If the address is wrong, put the organization's data right
   and import it: it is sent to the address in the data of the time it is tried.
3. There is neither. It has been sent. Look at the address in the `people.csv` that was imported, and at the spam
   folder.

## See whether it is running

`GET /a2ui/functions.js` answers 200 with no one signed in while the process runs. A proxy and what watches the server
use this. It does not ask the database, so watch the database on its own.

## Look at the log

The log is text on the standard error, a line for each thing. Watch these lines.

| Line | What it means |
|---|---|
| `msg="a thing to do failed; it is tried again"` | Something to do after an answer failed. `what` says what it is (the next table), `tries` how many times it was tried, and `err` why. It is tried again after 2 seconds, 4 seconds, 8 seconds and so on (up to 10 minutes apart), until it succeeds |
| `msg="a timer failed; it is tried again when its lease ends"` | A deadline or a reminder could not be dealt with. It is tried again after 30 seconds |
| `msg="a timer was refused; it is tried again later"` | A deadline or a reminder did not fit the rules of the request, most often because of the organization's data of that day |
| `msg="an operation failed"` | Someone was answered 500. `operation`, `ticket` and `person` say which operation, and `err` why |
| `msg="oidc: a sign-in refused"` | Someone signed in to your organization, and could not be told to be one person of the organization's data. `reason` says why ([What a person who signs in sees](sign-in.md#what-a-person-who-signs-in-sees)) |
| `msg="oidc: a sign-in failed"` | A failure between the server and the sign-in service. `step` says where |
| `msg="reading the starts of the versions"` | The database did not answer |
| `msg="the server stopped"` | It did not start, or it stopped. `err` says why |

These are the values of `what`. The `what` of the SQL below is the same.

| `what` | What it does |
|---|---|
| `notify` | Sends a notice |
| `launch` | Starts the work after an approval |
| `ask_agent`, `ask_note` | Has an agent check a request, and write a summary |
| `release` | Gives back an amount of a budget or the like that was held. Only when the books are in another database |
| `audit` | Sends the record of an operation to another place |

## Look into the database

This SQL says how many notices and pieces of work are not done yet, and which keep failing.

```sql
select what, count(*) filter (where done_at is null) as open, count(*) as made from ringi.todo group by what order by what;

select ticket, what, tries, last_error, try_at from ringi.todo where done_at is null and tries > 3 order by created;
```

The deadlines and reminders that are past and not dealt with:

```sql
select count(*) as late, min(at) as oldest from ringi.timers where at < now() - interval '5 minutes';
```

The requests that wait for someone to answer them, or to submit them again. Put the ID of the person in place of
`s1101`.

```sql
select w.ticket, t.kind, w.since from ringi.waiting w join ringi.tickets t on t.id = w.ticket
where w.person = 's1101' order by w.since;
```

How large the tables are:

```sql
select relname as "table", n_live_tup as rows, pg_size_pretty(pg_total_relation_size(relid)) as size
from pg_stat_user_tables where schemaname = 'ringi' order by pg_total_relation_size(relid) desc;
```

Only read the tables. To change something, do it through the server. The server compares what is in one table with
what is in another, so changing them yourself can make it stop working.

## When it does not start

The last line of the log says why. These are the common ones.

| The log | Why, and what to do |
|---|---|
| `signing in through the provider takes …, and these are missing: …` | Settings of your organization's sign-in are missing. Give those it lists |
| `… checks no one, so it listens only on a loopback address …` | There are no settings of your organization's sign-in, so it does not run on an address other computers reach. Give the settings, or make `-addr` `127.0.0.1:…` |
| `the environment has RINGI_SESSION_KEYS, a setting of signing in through the provider, and … checks no one` | Give the flags `-oidc-…`, or take that environment variable out |
| `-session-keys: oidc: key 1 has 33 bytes, not 32` | Make the key again with `head -c 32 /dev/urandom \| base64` |
| `the -oidc-… settings: oidc: the provider's discovery document: …` | The issuer is not written as the sign-in service writes it, or the server does not reach the sign-in service |
| `-db: postgres: the database cannot be reached: …` | Look at the connection string, the password and the network |
| `-db: postgres: the tables in the schema ringi: ERROR: permission denied for database …` | The user of the database may not make a schema. `grant create on database` gives the right |
| `-db: postgres: the tables in the schema ringi: they are at version 11, newer than this code's 10: …` | A server of a newer version brought the tables up to date. Run that version, or restore the backup made before it |
| `the package keeps contract …, and this framework reads …: …` | ritsu and ringi-server do not go together. Use a pair of [the table of versions](../README.md#versions), and run `go generate ./...` |
| The name and a line of a file of `org/` | The organization's data does not pass its checks. `ritsu check project` says the same before you start it |
