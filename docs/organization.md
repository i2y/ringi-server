# Prepare the organization's data

Who approves a request is found in the organization's data: three CSV files with the people, the teams and
who belongs where. When people change places, you add a new version. The files of the request types need no
change. Japanese: [organization.ja.md](organization.ja.md).

## Where the files are

```
project/
├── shared/
│   └── company.ringi      the positions and the kinds of team
└── org/
    ├── 2026-04-01/        the organization from 1 April 2026
    │   ├── teams.csv
    │   ├── people.csv
    │   └── members.csv
    ├── 2026-12-01/        the organization from 1 December 2026
    │   └── …
    └── proxies.csv        the proxies (you may leave it out)
```

The name of a directory is the day that organization starts. One directory is one version of the data.
Approvers are found in the version in effect on the day a stage starts.

## The three files

### Teams (teams.csv)

The company, its departments, their sections and so on.

```
id,name,name_ja,kind,parent,head
company,Example Co.,サンプル株式会社,company,,p0001
sales,Sales Department,営業部,department,company,s1001
sales_1,Sales Section 1,営業一課,section,sales,s1101
```

| Column | What to write |
|---|---|
| `id` | The ID of the team |
| `name`, `name_ja` | Its name, and its name in Japanese. `name_ja` may be left out |
| `kind` | The kind of team: one of `team kinds` in `company.ringi` |
| `parent` | The ID of the team above. Empty for the one at the top |
| `head` | The ID of its head. Empty while there is none |

### People (people.csv)

```
id,name,name_ja,email
s1001,Head of Sales,営業部長,s1001@example.com
s1101,Manager of Sales Section 1,営業一課長,s1101@example.com
s1201,Sales staff 1,営業担当 1,s1201@example.com
```

| Column | What to write |
|---|---|
| `id` | The ID of the person. Once given, it does not change |
| `name`, `name_ja` | Their name, and their name in Japanese. `name_ja` may be left out |
| `email` | Their mail address. A person who signs in is found by it, and notices are sent to it |
| `language` | The language of their notices and pages, `en` or `ja`. The whole column may be left out |

### Memberships (members.csv)

Who belongs to which team.

```
person,team,position,primary
s1001,sales,head,yes
s1101,sales_1,manager,yes
s1201,sales_1,staff,yes
```

| Column | What to write |
|---|---|
| `person`, `team` | The ID of the person, and the ID of the team |
| `position` | Their position: one of `positions` in `company.ringi` |
| `primary` | `yes` for their main team. `no` or empty for a team they are in besides |

Everyone needs one main team. Their manager and the head of their department are found from it ([How an
approver is found](concepts.md#how-an-approver-is-found)).

## The positions and the kinds of team

The positions and the kinds of team are in `project/shared/company.ringi`.

```ringi title="project/shared/company.ringi"
organization company v1 "Company"
description "The shape of the organization: the positions from the lowest, the kinds of teams from the widest, and the folder that holds the data of each version"

positions
  staff     "Staff"
  manager   "Manager"
  head      "Head of department"
  president "President"

team kinds
  company    "Company"
  department "Department"
  section    "Section"

data "../org"
```

| Where | What it says |
|---|---|
| `positions` | The positions, from the lowest. `nearest head above requester` looks for a head whose position is `head` or higher |
| `team kinds` | The kinds of team, from the widest. `requester.department` is the first team of the kind `department`, on the way up from the requester's team |
| `data` | The directory of the organization's data |

A company with divisions adds `division "Division"` to `team kinds`. Then a request type can say `head of
requester.division`.

## Put your own company in place of the sample one

A project `ritsu ringi init` makes has the data of a sample company. To put your own in its place, change the
files in this order. If no one uses the project yet, you need not raise the version of the request types.

1. Make the positions and the kinds of team in `project/shared/company.ringi` your own.
2. Make the three CSV files in `project/org/2026-04-01/` your own. Name the directory by the day that
   organization started. Make it a day before the dates of the scenarios.
3. In each `.ringi` file, make `owner` the ID of the team that keeps that kind of request. If a version
   of the organization has no team of that ID, the check (`ritsu check`) says so, as E511.
4. Make the IDs and the mail addresses in the scenarios of the `.ringi` files and in the tests
   (`pages_test.go`, `server_test.go`) those of people of your company.
5. Check it, make the code again, and test it.

    ```sh
    ritsu check project
    go generate ./...
    go test ./...
    ```

The check finds a person you forgot. This is the error when `s1202` of a scenario is not in the data.

```
…
error[ringi E603]: project/purchase.ringi:63:22: The ticket does not take the `submit` of this row: s1202 is not in the organization from 2026-04-01
    63 | | 2026-11-16 10:00 | submit  | s1202 |                               | manager  | in progress |
  = There is no ticket yet.
…
```

## When people change places

As an example, s1202 takes the place of s1101 as the manager of Sales Section 1 from 1 December. s1101 goes
to the Sales Department.

1. Copy the version there is now into a directory for the new one, named by the day it starts.

    ```sh
    cp -R project/org/2026-04-01 project/org/2026-12-01
    ```

2. In the new version, change the head of Sales Section 1 in `teams.csv`, and the teams and the positions of
   the two in `members.csv`.

    ```csv title="project/org/2026-12-01/teams.csv" hl_lines="3"
    company,Example Co.,サンプル株式会社,company,,p0001
    sales,Sales Department,営業部,department,company,s1001
    sales_1,Sales Section 1,営業一課,section,sales,s1202
    ```

    ```csv title="project/org/2026-12-01/members.csv" hl_lines="2 4"
    s1001,sales,head,yes
    s1101,sales,staff,yes
    s1201,sales_1,staff,yes
    s1202,sales_1,manager,yes
    ```

3. Check it.

    ```sh
    ritsu check project
    ```

    If there is a mistake, it says which line of which file, and how to put it right. Had you forgotten the
    line of `s1202` in `members.csv`, for example, this error would come out.

    ```
    …
    error[ringi E501]: project/org/2026-12-01/people.csv:6:1: The person `s1202` has no primary team
         6 | s1202,Sales staff 2,営業担当 2,s1202@example.com
      = Mark one of the rows of `s1202` in `project/org/2026-12-01/members.csv` `yes` in `primary`: a requester's manager and heads are found from their primary team.
    …
    ```

4. Read whose approvers change.

    ```sh
    ritsu ringi diff --versions 2026-04-01 2026-12-01 project/purchase.ringi
    ```

    ```
      The request `purchase` (`purchase.ringi`), read in the organization in effect on 2026-04-01 (the version from 2026-04-01) and on 2026-12-01 (the version from 2026-12-01).
    Approvers:
      The approvers change for 2 of the 9 requesters both versions have.
        s1201 (Sales staff 1), manager: manager: s1101 (Manager of Sales Section 1) → manager: s1202 (Sales staff 2)
        s1202 (Sales staff 2), manager: manager: s1101 (Manager of Sales Section 1) → manager: s1001 (Head of Sales)
    …
    ```

5. Give it to the server that runs. The next section says how.

In a request in progress, the approvers of a stage that has started stay as they are. From the next stage on,
they are found in the new version.

## Give it to a server that runs

There are two ways. Either replaces all of the organization's data at once: every version, the proxies, and
the [lists](lists.md). To go back to the data before, give that data again.

| How | When |
|---|---|
| Import it on the pages for administrators | Someone in personnel or general affairs puts it in, without stopping the server |
| Make the server again | A developer puts it in, along with changes to the request types and the like |

### Import it on the pages for administrators

Put it into a zip file, and import it on [the pages for
administrators](use/admin.md#importing-new-data-of-the-organization).

```sh
(cd project && rm -f ../org.zip && zip -qr ../org.zip org -x '*/.*')
```

Keep the same data in `project/org` too, and in Git. The next time the server is made again, the data built in
is imported if it has changed. If it was not put in `project/org`, the version imported on the page is gone
then.

### Make the server again

The organization's data is built into the server, and imported when it starts. Run these commands, then make
the server again and put it in place.

```sh
go generate ./...
go test ./...
```

Without `go generate`, the data for the tests is out of date, and `go test` fails.

## Set proxies

Who stands in for whom, and when, goes in `project/org/proxies.csv`.

```csv title="project/org/proxies.csv"
person,proxy,kind,from,to,requests
s1001,s1101,approve,2026-12-21,2026-12-25,purchase
```

| Column | What to write |
|---|---|
| `person`, `proxy` | The ID of the person, and the ID of the one who stands in for them |
| `kind` | `approve` to approve for them, `submit` to make requests for them |
| `from`, `to` | The first day and the last. With `to` empty, there is no end |
| `requests` | The request types it is for, with spaces between them. Empty for all |

Give it to the server in the same way as when people change places. There is no page on which staff set their
own proxies. Proxies are read on the day of the operation, so during that time the proxy can approve what
waits already, too.

## Requests that wait for someone who moved or left

The approvers of a stage that has started do not change when the organization's data does.

| Whom it waits for | What to do |
|---|---|
| Someone who moved | Nothing. They can answer it as before |
| Someone who left, or cannot answer | An administrator [adds an approver or hands the place over](use/admin.md#replacing-an-approver-who-cannot-answer) |

## Make it from your personnel system

If you have a personnel system, the easiest way is to have it write the three CSV files. Checking and
importing them is as above. `requests/organization/columns.md` of the project lists the columns too.

You can also have the server read the personnel system itself. Implement `server.OrgSource`, and give it as
`Organization` of `Config` ([Parts and packages](parts.md)).

## When the data of your staff is not to be in Git

Keep the sample company in `project/org`, and have the server read the real data from a directory of the
computer it runs on. The scenarios and the tests run on the sample company. Change `approvals.go` like this.

```go title="approvals.go" hl_lines="5"
import (
	"embed"
	"io/fs"
	"log/slog"
	"os"

	server "github.com/i2y/ringi-server"
```

```go title="approvals.go" hl_lines="2 3 4"
func Organization() server.OrgSource {
	if dir := os.Getenv("ORG_DIR"); dir != "" {
		return orgfiles.New(os.DirFS(dir))
	}
	dir, err := fs.Sub(project, "project")
```

Put `org/` in the directory of `ORG_DIR`, and `masters/` too if you use lists. The server reads the directory
when it starts, and when someone presses "Check the data the server reads at start" on the pages for
administrators. Mistakes in the CSV files are found then.

To see that no one is without an approver in the real data, use `ritsu check`: copy the project on your own
computer, put the real data in place of `project/org`, and run it. You may ignore the errors that say that a
person of a scenario is not there.
