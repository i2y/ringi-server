# Why something is refused

When an operation does not fit the rules, the server changes nothing, and says why. A page shows a sentence. An answer
of the API (409) has the kind of reason in `refused`. There are 12 kinds. Japanese: [refusals.ja.md](refusals.ja.md).

On the pages, "Details for the people who keep this kind of request" below the sentence says which rule it did not
fit, and where. A book's reason (`book`) is not folded there: it is written for the person, and shown right under the
sentence.

| `refused` | What the page says | When |
|---|---|---|
| `state` | The request is not in a state that takes this now. | An operation on a request that has ended. Approving one that is back with its requester. Past the time it may be withdrawn |
| `clock` | The request is waiting for a deadline to be dealt with. Try again in a moment. | An operation after a deadline came, and before the server has dealt with it |
| `stale` | The request changed after this page was shown. Here it is as it is now: check it, and try again. | After the page was opened, someone else approved it, an approver filled in a field, and so on |
| `person` | You cannot do this on this request. | Neither an approver nor a proxy of one. They have answered already. They tried to approve their own request. The gate does not permit it |
| `event` | This kind of request does not take this. | An operation the request type does not have. Sending back to a stage it cannot go back to. Adding the same approver twice |
| `limit` | This has been done as many times, or for as many days, as the request allows. | The times or the days of sending back, withdrawing or putting on hold are used up |
| `form` | Some values were not accepted. Check the fields marked below. | A value out of its range, a required field left empty, a value that is not one of the choices. The approver of the next stage was not chosen |
| `comment` | The comment was not accepted. Check the comment. | Left empty in an operation that needs one. Longer than 2,000 characters |
| `submit` | You are not one of the people who may make this request. | A request by someone who is not in `submitted by` of the request type |
| `data` | The request cannot be dealt with on this date with the data the company has set up. Ask the people who keep this kind of request. | There is no organization's data in effect that day. A day outside the time the request type can be used. The deadline cannot be counted in the calendar |
| `book` | A book (of a budget or of days of leave, say) did not take this. | Too little is left of a budget or of days of leave. It is not decided which department's book it goes in |
| `unread` | The request could not be dealt with. Ask the people who keep this kind of request. | The request type was changed without raising its version, and a request in progress cannot be read |

The answer of `form` says in `at` which field of the form it is. In a row of items, it is like `items[1].price`, with
the rows counted from 0. The page marks that field.

`text` and `text_ja` of the answer of `book` are the reason the book gave (such as "the department's budget is
short"). The page shows it under its own sentence.

## Operations of administrators

| `refused` | When |
|---|---|
| `stale` | Another administrator switched it first. Other data was imported after this was checked. A bundle was decided, or what it changes differs, after you read it |
| `owner` | A switch that moves the request type to another team, which you do not administer |
| `locks_out` | With this data, you could not import any more. To import it all the same, add `locks_out: true` |
| `uploader` | An approval or a decline of a bundle you uploaded: another administrator decides it |
| `api_changes` | An approval of a bundle that changes the API, without `api_changes: true` |
| `taken` | A hiding of an approved bundle whose versions new requests may still take: set them to never first |

## Answers without a reason of these kinds

| Status | What the page says | When |
|---|---|---|
| 401 | Not signed in | No one is signed in. The session ended |
| 403 | The form could not be read | It was sent from a page of another site |
| 404 | Not found | There is no request of that ID, or the person may not see it |
| 400 | What is wrong, and where | The organization's data or a bundle does not pass its checks (`problems`) |
| 413 | Too large to send | An attachment, a bundle or the body is above the limit |
| 429 | How many seconds to wait, or that there can be no more comments | A comment soon after another. The limit of comments was reached. The server checks another bundle |
| 500 | The server failed | The server failed. The log says why ([Watch it](troubleshooting.md#look-at-the-log)) |
