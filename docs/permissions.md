# Decide who may do what

At every operation, such as seeing a request, approving it or administering, the server asks the permissions
whether that person may do it. They are called the **gate**, and are in `project/shared/company.gate`, a file
in sekisho, one of ritsu's languages. Japanese: [permissions.ja.md](permissions.ja.md).

Who the approvers are is not in the gate. It comes from the route (the `.ringi` file) and the organization's
data ([How an approver is found](concepts.md#how-an-approver-is-found)).

## What you start with

The gate `ritsu ringi init` writes says this.

| What | Who may do it |
|---|---|
| Make a request | Anyone on the staff. `submitted by` of the `.ringi` file narrows it further |
| Approve, reject, send back, put on hold | The approvers the route found for that stage, and their proxies |
| Approve, reject or send back one's own request | No one, except in a request whose `.ringi` file allows it with `self approval` |
| Withdraw, discard | The requester's side |
| Comment | The requester's side, the approvers of the stage it is at (while it is with the requester, the one who sent it back), and the people of the department that keeps it |
| See a request | The requester's side, the approvers, those it was circulated to, and the people of the department that keeps it |
| Add an approver, hand a place over | A `workflow_admin` of the department that keeps it |
| Switch the version of a request type | A `workflow_admin` of the department that keeps it, and a `system_admin` |
| Read the record of requests | A `workflow_admin` of the department that keeps them, a `system_admin`, and an `auditor` |
| Import the organization's data | A `system_admin` |
| Read the record of the organization's data | A `system_admin` and an `auditor` |

"The requester's side" is the requester, and those who may make requests for them that day. For a request a
proxy made, it has the one who made it too.

"The people of the department that keeps it" are those of the team in `owner` of the `.ringi` file, and of the
teams below it (of its sections, when a department keeps it).

`workflow_admin` and the other roles come from [the sign-in](sign-in.md#4-pass-the-roles). People with a role
that administers can do what they could as staff, too.

## How to read the gate

One rule from `company.gate`:

```gate title="project/shared/company.gate"
permit the_owning_department_sees
  description "The department that owns a request type sees the tickets of it"
  principal is Staff
  action see
  when resource.owner is principal.department
```

| Word | What it means |
|---|---|
| `principal` | Who acts. `principal is Staff` is anyone who signed in, and `principal in auditor` anyone with the role `auditor` |
| `action` | The name of the operation. `see` is to see a request ([the list](#what-the-server-asks)) |
| `resource` | What it is done to: a request, a request type and so on. `resource.owner` is the team that keeps it |
| `when`, `unless` | When the rule holds, and when it does not |
| `permit` | A rule that permits. What no rule permits is not permitted |
| `forbid` | A rule that forbids, even what a `permit` permits |
| `expect deny`, `expect allow` | What is never to be permitted, and what is to be. `ritsu check` sees that the rules are so |

ritsu checks this file and makes Go code from it (`go/authz/company`). Whenever the server asks, `gate.go`
gives the values of the person and the request to that code, and answers what it says.

## Change the rules

As an example, let the people with the role `auditor` see every request. Add a rule after the last `permit` of
`company.gate`.

```gate title="project/shared/company.gate" hl_lines="5 6 7 8 9"
permit auditors_read_every_record
  description "An auditor reads the record of every request type and of the organization"
  principal in auditor
  action read_audit, read_organization_audit

permit auditors_see_every_ticket
  description "An auditor sees every ticket"
  principal in auditor
  action see
```

The check finds an error.

```sh
ritsu check project
```

```
…
error[sekisho E304]: project/shared/company.gate:265:1: The expectation `other_departments_do_not_see` does not hold: 8 of the 15 combinations it picks are allowed
   265 | expect deny other_departments_do_not_see
  = For example: Staff holding auditor, Ticket, resource.owner is not principal.department; allowed by `auditors_see_every_ticket`.
  = Fix the policies, or the expectation if it says more than is meant.
…
```

That is because the same file expects that people of other departments do not see a request (`expect deny`).
This change is a change of what is expected, so change the expectation.

```gate title="project/shared/company.gate" hl_lines="2 5"
expect deny other_departments_do_not_see
  description "One of another department who is no auditor does not see a ticket they are not on"
  principal in staff
  action see
  unless principal in auditor
  unless resource.owner is principal.department
```

Once the check passes, make the code and test it.

```sh
ritsu check project
go generate ./...
go test ./...
```

With the rules and what is expected of them written apart, you notice when a new rule lets in people you did
not mean. [The pages of sekisho](https://i2y.github.io/ritsu/sekisho/) say how to write the rules.

## Add a role or an attribute

To add a role, write a `role` in `company.gate`, list it in `roles` of `principal Staff`, and write the rules
that use it. The Go code does not change.

To use a new value of a person or a request in a rule, such as where they work, add it to `attributes` in
`company.gate`, and return its value in `gateStore` of `gate.go`.

## What the server asks

| Operation | The `action` it asks about |
|---|---|
| Make a request, submit it again | `submit` |
| Withdraw, discard | `withdraw` |
| Approve, save the changes | `approve` |
| Reject | `reject` |
| Send back | `send_back` |
| Put on hold, take off hold | `pause` |
| Take back an approval | `retract` |
| Add an approver | `add_approver` |
| Hand a place over | `hand_over` |
| Comment | `comment` |
| See a request one is neither the requester nor an approver of | `see` |
| Switch the version of a request type | `change_request_type` |
| Read the record of requests | `read_audit` |
| Import the organization's data | `import_organization` |
| Read the record of the organization's data | `read_organization_audit` |

The buttons of the pages come up after the same questions: a button that may not be pressed is not there.

Even when the gate permits it, a request may not take an operation, such as approving one that has ended. Then
the answer is [why it is refused](refusals.md).
