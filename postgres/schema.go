package postgres

// steps raise the tables one version each: steps[0] makes version 1. A step only adds (tables,
// columns, indexes); one that must rewrite rows is a step of its own, written up in DESIGN.md. {s}
// is the schema.
var steps = []string{
	`
create table {s}.tickets (
  id text primary key,
  kind text not null,
  version integer not null,
  status text not null,
  number bigint not null,
  body json not null,
  updated timestamptz not null
);
create table {s}.moves (
  ticket text not null references {s}.tickets (id),
  number bigint not null,
  at timestamptz not null,
  person text not null,
  key text not null,
  event json not null,
  effects json not null,
  organization text not null,
  primary key (ticket, number)
);
create table {s}.waiting (
  ticket text not null references {s}.tickets (id),
  person text not null,
  primary key (ticket, person)
);
create index waiting_person on {s}.waiting (person);
create table {s}.timers (
  ticket text not null,
  timer text not null,
  stage text not null,
  at timestamptz not null,
  try_at timestamptz not null,
  lease_until timestamptz,
  primary key (ticket, timer, stage)
);
create index timers_due on {s}.timers (try_at);
create table {s}.todo (
  id text primary key,
  seq bigserial not null,
  ticket text not null,
  what text not null,
  body json not null,
  created timestamptz not null,
  try_at timestamptz not null,
  tries integer not null default 0,
  last_error text not null default '',
  lease_until timestamptz,
  done_at timestamptz
);
create index todo_due on {s}.todo (try_at) where done_at is null;
create index todo_ticket on {s}.todo (ticket, seq);
create table {s}.operations (
  person text not null,
  key text not null,
  digest text not null,
  outcome text not null,
  reply json not null,
  at timestamptz not null,
  primary key (person, key)
);
create table {s}.organizations (
  id text primary key,
  seq bigserial not null,
  data json not null,
  at timestamptz not null
);
`,
	// 2: the attachments (Store.Files).
	`
create table {s}.files (
  id text primary key,
  name text not null,
  content_type text not null,
  size bigint not null,
  by_person text not null,
  at timestamptz not null,
  data bytea not null
);
`,
	// 3: the people of each ticket (Move.People and Move.Own), filled with the requester's side of
	// the tickets there are: the approvers of the tickets made before it are the ones their tickets
	// name now.
	`
create table {s}.people (
  ticket text not null references {s}.tickets (id),
  person text not null,
  own boolean not null,
  since timestamptz not null,
  primary key (ticket, person)
);
create index people_mine on {s}.people (person, since desc, ticket collate "C" desc) where own;
insert into {s}.people (ticket, person, own, since)
select t.id, x.person, bool_or(x.own), min(coalesce(m.at, t.updated))
from {s}.tickets t
left join {s}.moves m on m.ticket = t.id and m.number = 1
cross join lateral (values (t.body->>'requester', true), (t.body->>'made_by', true), (t.body->>'sent_back_by', false)) as x(person, own)
where x.person is not null and x.person <> ''
group by t.id, x.person;
`,
	// 4: the sessions of signing in that were ended before their time (Store.Revoke).
	`
create table {s}.revoked (
  session text primary key,
  until timestamptz not null
);
create index revoked_until on {s}.revoked (until);
`,
	// 5: the uploads no ticket holds yet, which the server lets go of after a time (Store.KeepUpload,
	// Store.LetGoUploads), and the operations by their time, which it forgets after a time
	// (Store.ForgetOperations). An upload's state is open, held (a ticket holds it), gone (let go:
	// its file is being deleted) or deleted. Files kept before this step have no record as uploads,
	// so they are never let go.
	`
create table {s}.uploads (
  id text primary key,
  seq bigserial not null,
  by_person text not null,
  at timestamptz not null,
  state text not null default 'open'
);
create index uploads_open on {s}.uploads (by_person) where state = 'open';
create index uploads_old on {s}.uploads (at, seq) where state in ('open', 'gone');
create index operations_at on {s}.operations (at);
`,
	// 6: the operations begun whose reply is not kept yet, with the names the server gave them
	// (Store.Begin), and when each ticket began to wait for the people it waits for (Move.Since),
	// which orders the tickets that wait for a person (Store.Waiting). The times are filled by step 7.
	`
create table {s}.begun (
  person text not null,
  key text not null,
  digest text not null,
  ticket text not null,
  attempt text not null,
  at timestamptz not null,
  primary key (person, key)
);
create index begun_at on {s}.begun (at);
alter table {s}.tickets add column waiting_since timestamptz;
alter table {s}.waiting add column since timestamptz;
`,
	// 7 rewrites rows (DESIGN 4.3): each ticket that waits now is given the time its stage started
	// (its JSON's stages[].started), else the time of its last move, and the list of the tickets
	// that wait for a person is indexed by it.
	`
update {s}.tickets t set waiting_since = coalesce(
  (select to_timestamp((st->>'started')::bigint) from json_array_elements(t.body->'stages') st
   where t.status in ('in_progress', 'paused') and st->>'name' = t.body->>'stage' and st->>'started' is not null
   limit 1),
  t.updated)
where exists (select 1 from {s}.waiting w where w.ticket = t.id);
update {s}.waiting w set since = t.waiting_since from {s}.tickets t where t.id = w.ticket;
alter table {s}.waiting alter column since set not null;
drop index {s}.waiting_person;
create index waiting_list on {s}.waiting (person, since desc, ticket collate "C" desc);
`,
	// 8: the administration (DESIGN 13.8): the records of what administrators changed, the starts of
	// the versions they decided, the drafts of the organization's data they checked, and the kind,
	// the version and the way in of each move, for the record's lines. The moves kept before it are
	// given their tickets' kinds and versions by step 9; their way in stays "".
	`
create table {s}.records (
  id text primary key,
  at timestamptz not null,
  person text not null,
  via text not null,
  what text not null,
  kind text not null,
  version integer not null,
  before json not null,
  after json not null,
  reason text not null,
  source text not null,
  snapshot text not null,
  added boolean not null
);
create index records_order on {s}.records (at desc, id collate "C" desc);
create index records_kind on {s}.records (kind, version, at desc, id collate "C" desc);
create index records_imports on {s}.records (at) where what = 'organization' and added;
create index records_source on {s}.records (at desc, id collate "C" desc) where what = 'organization' and source = 'config';
create table {s}.starts (
  kind text not null,
  version integer not null,
  how text not null,
  at timestamptz,
  day text not null,
  number bigint not null,
  record text not null,
  primary key (kind, version)
);
create table {s}.drafts (
  person text not null,
  id text not null,
  source text not null,
  data json not null,
  at timestamptz not null,
  primary key (person, id)
);
create index drafts_old on {s}.drafts (at, person collate "C", id collate "C");
alter table {s}.moves add column kind text not null default '';
alter table {s}.moves add column version integer not null default 0;
alter table {s}.moves add column via text not null default '';
create index moves_order on {s}.moves (at desc, ticket collate "C" desc, number desc);
create index moves_kind on {s}.moves (kind, version, at desc, ticket collate "C" desc, number desc);
create index moves_person on {s}.moves (person, at desc, ticket collate "C" desc, number desc);
create index tickets_count on {s}.tickets (kind, version, status);
`,
	// 9 rewrites rows (DESIGN 4.3): each move kept before step 8 is given its ticket's kind and version.
	`
update {s}.moves m set kind = t.kind, version = t.version from {s}.tickets t where t.id = m.ticket and m.version = 0;
`,
}

// peopleOf is the people of the ticket t, sorted as Go sorts strings.
const peopleOf = `array(select q.person from {s}.people q where q.ticket = t.id order by q.person collate "C")`

var queries = map[string]string{
	"empty":   `truncate {s}.waiting, {s}.people, {s}.moves, {s}.tickets, {s}.timers, {s}.todo, {s}.operations, {s}.begun, {s}.organizations, {s}.files, {s}.revoked, {s}.uploads, {s}.records, {s}.starts, {s}.drafts`,
	"ticket":  `select t.id, t.kind, t.version, t.status, t.number, t.body::text, t.updated, ` + peopleOf + `, t.waiting_since from {s}.tickets t where t.id = $1`,
	"op":      `insert into {s}.operations (person, key, digest, outcome, reply, at) values ($1, $2, $3, $4, $5::json, $6) on conflict do nothing`,
	"unbegin": `delete from {s}.begun where person = $1 and key = $2`,
	"begin": `insert into {s}.begun (person, key, digest, ticket, attempt, at) values ($1, $2, $3, $4, $5, $6)
	          on conflict (person, key) do nothing`,
	"begun": `select digest, ticket, attempt, at from {s}.begun where person = $1 and key = $2`,
	"renew": `insert into {s}.begun (person, key, digest, ticket, attempt, at) values ($1, $2, $3, $4, $5, $6)
	          on conflict (person, key) do update set digest = excluded.digest, ticket = excluded.ticket, attempt = excluded.attempt, at = excluded.at`,
	"new":    `insert into {s}.tickets (id, kind, version, status, number, body, updated, waiting_since) values ($1, $2, $3, $4, 1, $5::json, $6, $7) on conflict (id) do nothing`,
	"update": `update {s}.tickets set body = $2::json, status = $3, number = number + 1, updated = $4, waiting_since = $6 where id = $1 and number = $5`,
	// A move is kept with its ticket's kind and version, which the ticket keeps from its first move.
	"move": `insert into {s}.moves (ticket, number, at, person, key, event, effects, organization, kind, version, via)
	         select $1, $2, $3, $4, $5, $6::json, $7::json, $8, t.kind, t.version, $9 from {s}.tickets t where t.id = $1`,
	"unwait": `delete from {s}.waiting where ticket = $1`,
	"wait":   `insert into {s}.waiting (ticket, person, since) select $1, p, $3 from unnest($2::text[]) as p`,
	"rang":   `delete from {s}.timers where ticket = $1 and timer = $2 and stage = $3 and at = $4`,
	"cancel": `delete from {s}.timers where ticket = $1 and timer = $2 and stage = $3`,
	"set": `insert into {s}.timers (ticket, timer, stage, at, try_at) values ($1, $2, $3, $4, $5)
	        on conflict (ticket, timer, stage) do update set at = excluded.at, try_at = excluded.try_at, lease_until = null`,
	"todo":  `insert into {s}.todo (id, ticket, what, body, created, try_at) values ($1, $2, $3, $4::json, $5, $6) on conflict (id) do nothing`,
	"moves": `select ticket, number, at, person, key, event::text, effects::text, organization, kind, version, via from {s}.moves where ticket = $1 order by number`,
	"kept":  `select digest, outcome, reply::text, at from {s}.operations where person = $1 and key = $2`,
	// Each person's page is read by the index waiting_list, in its order, up to the limit; the
	// pages of the people are then merged (a ticket that waits for two of them once).
	"waiting": `select t.id, t.kind, t.version, t.status, t.number, t.body::text, t.updated, ` + peopleOf + `, t.waiting_since
	            from (select distinct on (x.ticket) x.ticket, x.since
	                  from unnest($1::text[]) as p(person)
	                  cross join lateral (
	                    select w.ticket, w.since from {s}.waiting w
	                    where w.person = p.person
	                      and ($3 = '' or (w.since, w.ticket collate "C") <
	                           (select b.since, b.ticket collate "C" from {s}.waiting b where b.ticket = $3 and b.person = any($1) limit 1))
	                      and ($2 = '' or exists (select 1 from {s}.tickets k where k.id = w.ticket and k.kind = $2))
	                    order by w.since desc, w.ticket collate "C" desc
	                    limit $4) x
	                  order by x.ticket) w
	            join {s}.tickets t on t.id = w.ticket
	            order by w.since desc, w.ticket collate "C" desc
	            limit $4`,
	// The person's rows of waiting are read by the index waiting_list (or the whole table, when they
	// are many of its rows) and counted by their tickets' kinds: a row comes back for each kind, and
	// no ticket's JSON.
	"count_waiting": `select t.kind, count(*) from {s}.waiting w join {s}.tickets t on t.id = w.ticket
	                  where w.person = $1 group by t.kind`,
	"people": `insert into {s}.people (ticket, person, own, since)
	           select $1, p, coalesce(p = any($3::text[]), false), $4 from unnest($2::text[]) as p
	           on conflict (ticket, person) do update set own = true, since = excluded.since
	           where excluded.own and not {s}.people.own`,
	"mine": `select t.id, t.kind, t.version, t.status, t.number, t.body::text, t.updated, ` + peopleOf + `, t.waiting_since
	         from {s}.people p join {s}.tickets t on t.id = p.ticket
	         where p.person = $1 and p.own and ($2 = '' or t.kind = $2)
	           and ($3 = '' or (p.since, p.ticket collate "C") <
	                (select b.since, b.ticket collate "C" from {s}.people b where b.person = $1 and b.ticket = $3 and b.own))
	         order by p.since desc, p.ticket collate "C" desc
	         limit $4`,
	"due_timers": `update {s}.timers t set lease_until = $2
	               from (select ticket, timer, stage from {s}.timers
	                     where try_at <= $1 and (lease_until is null or lease_until <= $1)
	                     order by try_at, ticket, timer, stage limit $3 for update skip locked) d
	               where t.ticket = d.ticket and t.timer = d.timer and t.stage = d.stage
	               returning t.ticket, t.timer, t.stage, t.at, t.try_at`,
	"timers":  `select ticket, timer, stage, at, try_at from {s}.timers where ticket = $1`,
	"put_off": `update {s}.timers set try_at = $4, lease_until = null where ticket = $1 and timer = $2 and stage = $3`,
	"due_todo": `update {s}.todo t set lease_until = $2
	             from (select id from {s}.todo
	                   where done_at is null and try_at <= $1 and (lease_until is null or lease_until <= $1)
	                   order by seq limit $3 for update skip locked) d
	             where t.id = d.id
	             returning t.seq, t.id, t.ticket, t.what, t.body::text, t.created, t.try_at, t.tries, t.last_error, t.done_at`,
	"todo_item":  `select seq, id, ticket, what, body::text, created, try_at, tries, last_error, done_at from {s}.todo where id = $1`,
	"todo_list":  `select seq, id, ticket, what, body::text, created, try_at, tries, last_error, done_at from {s}.todo where ticket = $1 order by seq`,
	"done":       `update {s}.todo set done_at = $2, lease_until = null where id = $1`,
	"retry":      `update {s}.todo set tries = tries + 1, try_at = $2, last_error = $3, lease_until = null where id = $1`,
	"save_org":   `insert into {s}.organizations (id, data, at) values ($1, $2::json, $3) on conflict (id) do update set seq = default, at = excluded.at`,
	"latest_org": `select id, data::text, at from {s}.organizations order by seq desc limit 1`,
	"org":        `select id, data::text, at from {s}.organizations where id = $1`,

	// The sessions that were ended (revoked.go).
	"revoke": `with gone as (delete from {s}.revoked where until < now() and session <> $1)
	           insert into {s}.revoked (session, until) values ($1, $2)
	           on conflict (session) do update set until = greatest({s}.revoked.until, excluded.until)`,
	"revoked": `select exists (select 1 from {s}.revoked where session = $1)`,

	// The attachments (files.go).
	"file_put":    `insert into {s}.files (id, name, content_type, size, by_person, at, data) values ($1, $2, $3, $4, $5, $6, $7)`,
	"file_stat":   `select name, content_type, size, by_person, at from {s}.files where id = $1`,
	"file_open":   `select name, content_type, size, by_person, at, data from {s}.files where id = $1`,
	"file_delete": `delete from {s}.files where id = $1`,

	// The uploads (uploads.go) and the operations past their time.
	"upload_keep":   `insert into {s}.uploads (id, by_person, at) values ($1, $2, $3) on conflict (id) do nothing`,
	"uploads_open":  `select count(*) from {s}.uploads where by_person = $1 and state = 'open'`,
	"uploads_hold":  `update {s}.uploads set state = 'held' where id = any($1) and state = 'open'`,
	"uploads_gone":  `select id from {s}.uploads where id = any($1) and state in ('gone', 'deleted') order by id limit 1`,
	"uploads_letgo": `select id, by_person, at from {s}.uploads where state = 'gone' order by at, seq limit $1`,
	"uploads_go": `update {s}.uploads u set state = 'gone'
	               from (select id from {s}.uploads where state = 'open' and at < $1
	                     order by at, seq limit $2 for update skip locked) o
	               where u.id = o.id
	               returning u.id, u.by_person, u.at`,
	"uploads_deleted": `update {s}.uploads set state = 'deleted' where id = any($1) and state = 'gone'`,
	// The operations past their time are forgotten the oldest first, and those of one time by person,
	// then by key, as Go compares strings (byte by byte: collate "C"), as the memory store forgets
	// them, whatever the database's collation.
	"ops_forget": `delete from {s}.operations where (person, key) in
	               (select person, key from {s}.operations where at < $1
	                order by at, person collate "C", key collate "C" limit $2)`,
	"begun_forget": `delete from {s}.begun where (person, key) in
	                 (select person, key from {s}.begun where at < $1
	                  order by at, person collate "C", key collate "C" limit $2)`,
}
