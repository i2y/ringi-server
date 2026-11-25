# Set the business days

A deadline such as "within 3 business days" is counted in your company's business days. They are in
`project/shared/office.cal`, written in koyomi, one of ritsu's languages. In the file `ritsu ringi init`
writes, public holidays are business days. Add your public holidays and your company's own days off before
your staff use it. Japanese: [calendar.ja.md](calendar.ja.md).

## What you start with

```cal title="project/shared/office.cal"
calendar office v1
description "The business days of the office: Saturdays, Sundays and the New Year break are closed, in the time of Tokyo"
offset +09:00

closed weekly sat, sun
closed every 12-29..01-03 "New Year break"
```

| What it says | What it means |
|---|---|
| `offset +09:00` | The time difference dates and times are counted in. "The end of the day" of a deadline, and the `09:00` of a reminder, are in it |
| `closed weekly sat, sun` | Closed every Saturday and Sunday |
| `closed every 12-29..01-03 "New Year break"` | Closed every year from 29 December to 3 January |

## Add your company's days off

A day off of every year is `closed every`, and one of a single time is `closed` and its date.

```cal title="project/shared/office.cal" hl_lines="3 4"
closed weekly sat, sun
closed every 12-29..01-03 "New Year break"
closed every 06-10 "Foundation day"
closed 2027-04-30 "Extra day off"
```

## Read a list of public holidays

A list of public holidays that is published as a CSV file can be read too. The example is the one the Cabinet
Office of Japan publishes. You download the file into the project, and read it from there.

1. Add the three lines of `source`, and the line `closed national_holidays`.

    ```cal title="project/shared/office.cal" hl_lines="4 5 6 7 9"
    description "The business days of the office: Saturdays, Sundays and the New Year break are closed, in the time of Tokyo"
    offset +09:00

    source national_holidays = file "data/syukujitsu.csv" url "https://www8.cao.go.jp/chosei/shukujitsu/syukujitsu.csv"
      format csv shift_jis
      covers listed years

    closed weekly sat, sun
    closed national_holidays
    closed every 12-29..01-03 "New Year break"
    ```

    | What it says | What it means |
    |---|---|
    | `file` | Where the downloaded file goes, from the directory of `office.cal` |
    | `url` | Where it is downloaded from |
    | `format csv shift_jis` | A CSV file with "date,name" on each line, in Shift_JIS |
    | `covers listed years` | Of a year that is in the file, every day off is in it. Of a year that is not, it is not known whether a day is a business day |

2. Download it, and write its hash.

    ```sh
    ritsu koyomi source fetch project/shared/office.cal
    ritsu koyomi source pin project/shared/office.cal
    ```

    `fetch` keeps the file in `project/shared/data/`. `pin` adds the hash of the file (`sha256:`) to the line
    of `source`. If what is in the file changes without your knowing, `ritsu check` says so as an error.

3. Check it, and make the code again.

    ```sh
    ritsu check project
    go generate ./...
    go test ./...
    ```

    ```
    …
      used through 2027-12-21: the data of the calendar `office` (`project/shared/office.cal`) ends on 2027-12-31, and the days to resubmit a ticket that goes back to its requester later cannot be counted in it (`resubmit within 5 business days`)
    …
    ```

With holidays up to 2027, purchase requests can be made through 21 December 2027. If the list is not brought
up to date by then, no request can be made.

A CSV file of your company's own days off is read in the same way. You may leave out `url`.

## Add the holidays once a year

When the holidays of the next year are published, bring the list up to date like this.

1. Download it again, and write its hash again.

    ```sh
    ritsu koyomi source fetch project/shared/office.cal
    ritsu koyomi source pin project/shared/office.cal
    ```

2. Check it, make the code again, and test it.

    ```sh
    ritsu check project
    go generate ./...
    go test ./...
    ```

3. Make the server again and put it in place ([Put in a server with changed request
   types](maintenance.md#put-in-a-server-with-changed-request-types)).

You do not raise the version of the request types. The deadlines of the stages that wait stay as they are,
and those of the stages that start from now on are counted in the new business days.

If you keep [the code of an earlier version](types.md#change-the-rules-of-a-type-in-use) with requests in
progress, bring the calendar of that version (`v1/project/shared/office.cal` and its data) up to date too: the
deadlines of those requests are counted in it.
