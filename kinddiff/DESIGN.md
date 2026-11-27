# Design: kinddiff

`kinddiff` compares two versions of one kind of request as they appear in two requests directories,
the directories `ritsu gen --target requests` writes. It reads `types/<kind>.json` and the operations
the kind owns in `openapi.json` from each directory, and answers a `Diff`: the kind, the old and new
versions, whether the kind is new, and the changes as sentences in English or Japanese.

Japanese: [DESIGN.ja.md](DESIGN.ja.md).

The package uses the standard library alone.

## 1. Why the server makes the diff

A bundle arrives while the server is running. Before an administrator approves it, the server has to
show what will change. The bundle could carry a text saying so, but that text would only be a claim
made by the bundle. A missing line in it would be indistinguishable from an unchanged part. The
server therefore makes the diff itself from the two machine-readable contracts it already trusts: the
generated description of the kind and the generated OpenAPI document.

This also makes the answer checkable. The same two directories always produce the same lines, and a
test can compare two real generated directories rather than a text written for the test.

## 2. What is compared

`Compare(old, new, kind)` reads `types/<kind>.json` from both directories. `old == nil` means that
the kind is new. A directory without the kind, a description that does not read as the expected JSON
object, and a description of another kind are refused. The description's `name` has to be the kind
being compared; a change of that name is therefore not a diff but a refusal.

Except `version`, every top-level key of the description is compared. Four parts are read in detail:

- `form`: fields added, removed and moved; labels; type; the bounds of the value; the length of a
  text or the number of rows; choices; requiredness; computed fields; the fields a computation
  reads; whether only approvers fill in a field; the calendar of open days; and the conditions
  that show or require a field. Fields inside rows are compared recursively and named by dotted
  ASCII paths such as `items.price`.
- `stages`: stages added, removed and moved; labels; the name as written; `comment_required`, the
  operations for which the stage requires a reason; the stage's deadline lines; and the tables a
  late stage goes up by.
- `approvers`: the cases in which a stage comes, the `ways` and the sentence of whom each way
  chooses, positions and team kinds of the organization, the organization `needs` (`people`,
  `teams` and `members_of`), `submitted_by`, `self_approval`, and the fields named by
  `approvers_edit`.
- `usable`: `effective_from`, `usable.from`, `usable.through`, the explanatory entries
  `usable.from_by` and `usable.through_by`, and `utc_offset_minutes`.

Every other key is still compared and reported in the `other` section. That includes an unknown key
inside a stage, a way, a position or a team kind. The package does not guess from a key's name that
it is an approver rule or a deadline: a future key that happens to be called `rule` is only an
unknown value until ritsu defines what the description carries. Simple values are shown as values.
Keys with a clearer meaning have a sentence of their own: `endings`, `events`, `masters` and
`flows`. A change deep inside a less structured key is reported as a change of that key rather than
silently ignored.

A new kind is treated as a comparison against an empty description: its fields, stages, ways, dates
and operations are all additions. This is verbose on purpose, since approving a new kind approves
its whole shape.

Labels are shown in the language asked for. A description's `label` is in the language its source
file was written in, and `labels` holds translations. If the requested language has no translation,
the base label is used. The ASCII machine name follows in brackets when it differs from the label,
because names are what the API and the files use.

A generated field writes the same keys for every type, with null or an empty value where a key does
not apply. The diff reads all of them: `written`, `label`, `labels`, `type`, `type_written`, `min`,
`max`, `count_min`, `count_max`, `choices`, `optional`, `computed`, `reads`, `worked_on_screen`,
`approvers_only`, `open_in`, `shown_when`, `required_when` and `fields`. The two ends of `min` and
`max` are presented as one range, and the two ends of `count_min` and `count_max` as one length or
row-count limit. The words depend on the type: a number, a date or an amount has a range of that
kind; a text has a length limit; rows have a row-count limit. An absent end is an absent end, so a
text bounded to 100 characters is never described as without bounds.

A change of `version` itself makes no line. The page already names the two versions it compares,
and repeating the numbers for every pair would hide the substantive diff. The versions still live in
`Diff.OldVersion` and `Diff.NewVersion` for the caller.

## 3. How approvers are chosen and how deadlines are read

A stage's `approvers` is the ordered list of the cases in which the stage comes. The diff reports a
case added, removed or moved in its route, and compares the case's `when`, `who`, `ways`, `kind` and
`route`. Cases have no machine identifier of their own, so unchanged cases are matched first; of the
cases left over, similar ones are read as edits and the rest as removals and additions. This keeps a
small edit from being reported as the loss of one unrelated case and the addition of another, while
still letting a real replacement appear as a removal and an addition. The names in `ways` are
compared as a set: their order does not change whom the case can choose.

A stage's `deadlines` are matched by their `kind` (`due`, `remind`, `late` or `empty`), with the
written order breaking ties if a kind appears twice. The diff reports a line added or removed and
changes of `days`, `calendar` and `written`. The `written` value is the machine-readable source form
of what a late or empty stage does; the line also shows the page sentence before and after the
change. Deadline lines stay in the `stages` section because they are part of how the stage runs. No
new `Line.Section` is introduced.

The root `late_tables` are matched by `name`. A table added or removed is reported as such. Inside a
table, heads and rows are first matched by the entries that did not change, so removing one row does
not make every row after it look edited. The diff reports changed heads and rows, entries added or
removed, and a change of row or head order. An English and a Japanese form of the same logical row
produce one line, not two.

For these rules and deadlines, the substance of a line is the `en` or `ja` sentence already written
in the description. The server does not paraphrase rule logic: a generated explanation could be a
different rule while sounding plausible. Framing such as “the case is added” is necessarily the
server's, but the condition, the description of who answers, the way's `how`, the deadline sentence
and the table text are quoted in the language asked for. The machine fields `ways`, `kind`, `route`,
`days`, `calendar` and `written` are still compared as machine fields, because the sentence alone is
not a safe representation of them.

None of these values says which actual people will approve. That depends on the organization's data
and is decided when the stage starts. The diff compares the rule and deadline shape, not the people
an organization version would resolve on a particular day.

A description written by an older ritsu has none of these keys. When an existing kind's old and new
descriptions differ in whether they expose one of these families, the exposed content is not a
behavior addition or removal: the old behavior existed, but the old description did not say it. The
diff therefore says once that the change cannot be shown. The stage cases and the ways' `how` share
one such explanation in `approvers`, because both are parts of how approvers are chosen; when the
cases are exposed, the cases of the exposing side are then listed as what that description says now.
Deadline and late-table explanations are in `stages`. Their unchanged content is not enumerated:
this is a compatibility notice, not a change report, and a long list of lines that did not change
would hide the real diff. A genuinely new kind is not treated this way: there is no old behavior, so
its shape is reported as additions.

## 4. What counts as an API change

The diff does not compare OpenAPI text. It compares the shape a client can observe. For each
operation belonging to the kind it keeps only:

- method and path;
- parameters, with their input location, requiredness, style and schema or content;
- the request body's requiredness, media types, schemas and encodings;
- response statuses, headers, media types, schemas and encodings.

Local `$ref` references are resolved before this selection. Cyclic references are left as their
`$ref` marker so expansion is finite. Header and media type names are compared in lower case.
Descriptions, titles, examples, deprecation and read/write annotations, vendor extensions,
operation tags, summaries and the `x-ritsu-request` marker are documentation or metadata, not the
shape of the JSON a client sends or receives, and are left out. Object key order is ignored. Arrays
of scalar values such as `required` and `enum` are compared as sets because their order has no
meaning in JSON Schema.

An operation belongs to the kind when `x-ritsu-request.name` is the kind. Older generated documents
may lack that marker, so operations whose paths start `/requests/<kind>/`, or whose tag is the kind,
are also recognised. Shared operations such as ticket and organization operations are not reported
as changes of the kind. A shared schema can still affect the kind: if the kind's operation refers to
it, its expansion is part of that operation's shape.

`APIChanged()` is true when a line in the `api` section exists: an operation is added or removed,
its method or path changes, a parameter is added or removed or changes shape, a request body or
response gains or loses a media type or status, or a schema constraint changes. This mark matters
because clients, screens and generated callers may depend on the wire shape even when the visible
approval process is unchanged; the approval flow can require explicit acknowledgement of it.

## 5. Order and language

`Lines(lang)` answers a copy of the lines. `lang == "ja"` chooses Japanese; every other value
chooses English. The order is stable: `form`, `stages`, `approvers`, `usable`, `api`, then `other`.
Inside a section, lines follow the order of the description or the order operations appear in
`openapi.json`. Object key order does not affect equality, but it does affect which change is
presented first, matching the order an administrator sees in the generated document.

Each `Line` carries its section. API lines also have `API` true, so a caller can both group by
section and require an explicit acknowledgement of API shape changes.

Japanese lines place names by the framework's own rule in `internal/jatext`: an ASCII name, key or
ID has one space between it and adjoining kana or kanji, and none next to a full-width mark.
Numbers, dates and amounts are written into the Japanese text itself, so they stay next to their
counters and units with no space. Sentences taken from the description (`when`, `who`, `how` and
the late tables) are quoted as they are; this spacing is only between the server's framing and the
pieces it quotes, never a rewrite inside those sentences.

## 6. Real output

`ExampleCompare` in `example_test.go` compares the real generated `leave` version 1 and version 2
copies in `kinddiff/testdata`. They were written by `ritsu gen --target requests` of ritsu 0.56.0,
and contain the approver, deadline, way and late-table metadata. `kinddiff/testdata/old` holds
version 1 as a ritsu before that metadata wrote it, for the comparison of such an old description
with a new one. Go's example test checks this exact output:

```text
form api=false: The number range of the field "Business days" (days) changes from 1 through 20 to 1 through 25.
usable api=false: This version now takes effect from 2027-04-01.
usable api=false: The first day the kind can be used changes from 2026-04-01 to 2027-04-01.
usable api=false: The data deciding the first day the kind can be used changes from "the first version of the organization `shared/company.ringi` (data `../org`)" to "`effective from`".
api api=true: In the request body of `submit_leave`, `maximum` of `form.days` changes from `20` to `25`.
api api=true: In the request body of `resubmit_leave`, `maximum` of `form.days` changes from `20` to `25`.
form api=false: 項目「営業日の日数」（days）の数値の範囲が1以上20以下から1以上25以下に変わります。
usable api=false: この版は2027-04-01から効きます。
usable api=false: この申請の種類を出せる最初の日が2026-04-01から2027-04-01に変わります。
usable api=false: 申請を出せる最初の日を決めるデータが「the first version of the organization `shared/company.ringi` (data `../org`)」から「`effective from`」に変わります。
api api=true: `submit_leave` に送る本文では、`form.days` の `maximum` が`20`から`25`に変わります。
api api=true: `resubmit_leave` に送る本文では、`form.days` の `maximum` が`20`から`25`に変わります。
```

The output has no approver or deadline lines because the two real versions write the same values for
that metadata. The detailed tests edit copies of these real descriptions for each kind of change.
The nonempty late-table fixture is the real generated `expense` table; its edits are test fixtures,
not output of another ritsu version.

## 7. What is left out

The diff does not read screens, MCP tools, vectors, masters' data, or organization data. They can
affect what a deployment shows or does, but they are not part of the kind's own description or its
wire API. The bundle approval will need its own view for them if they become part of the bundle's
contract.

Unknown keys are not interpreted. Interpreting a key this package does not understand could invent a
meaning the generator did not intend; the key is instead shown in `other` with its before and after
values. The same applies to details inside `masters` and `flows` beyond their named entries.

A change of the description's `name` cannot be shown as a change because the file is addressed as
`types/<kind>.json` and its `name` is the proof that it describes that kind. The comparison refuses
it as a different kind.

No golden files are used for the sentences. The repository uses goldens where large structured
screen output would be hard to read in assertions. Here the sentences are the product itself, so the
tests assert the important sentence fragments in both languages, and `ExampleCompare` pins one real
end-to-end output exactly.
