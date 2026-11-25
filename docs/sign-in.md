# Connect the sign-in

A server your staff use takes your organization's sign-in. With a sign-in service of OpenID Connect, flags are
all it takes. Japanese: [sign-in.ja.md](sign-in.ja.md).

## Three ways to sign in

| How the server is started | Whose request it is | What it is for |
|---|---|---|
| `-dev-login` | The person chosen on a page | To try the pages on your own computer |
| The flags `-oidc-…` | The person who signed in to your organization's service | For your staff |
| Neither | The person in the header `X-Person` | To try the API on your own computer |

`-dev-login` and `X-Person` check no one. So they work only on an address of your own computer, and a request
that a proxy forwarded is not taken.

When a proxy in front of the server signs people in, or when programs are to come with tokens, you put the
server together yourself ([Embed it in a program of your own](embedding.md)).

## 1. Register it with the sign-in service

Have those who run the sign-in service make a client (the registration of this server) like this.

| What | Value |
|---|---|
| Type | A web application with a secret (the authorization code flow) |
| Redirect URI | The address your staff open, with `/login/callback` after it, and with the port if there is one (such as `https://ringi.example.com:8443/login/callback`) |
| Scopes | `openid email profile` |
| Claims in the ID token | `email`, `email_verified`, and the list of the person's roles |
| Signature of the ID token | RS256, PS256 or ES256 |
| Authentication of the client | `client_secret_basic` or `client_secret_post` |

You get three things: the URL of the issuer, the client's ID, and the client's secret.

## 2. Put the mail addresses in the organization's data

The server finds the person who signed in by their mail address, in the organization's data. In the column
`email` of `people.csv`, put the address the sign-in service has verified. Upper and lower case are the same.

## 3. Give it the settings

Make the key that encrypts the cookies of the people who sign in. You make it once, keep what comes out, and
give the server the same one each time it starts. If you lose it, everyone signs in again.

```sh
head -c 32 /dev/urandom | base64
```

The client's secret and the key go in environment variables, and the rest in flags.

| What | Environment variable or flag |
|---|---|
| The client's secret | `RINGI_OIDC_CLIENT_SECRET` |
| The key of the cookies | `RINGI_SESSION_KEYS` |
| The URL of the issuer | `-oidc-issuer https://id.example.com` |
| The client's ID | `-oidc-client-id ringi` |
| The redirect URI | `-oidc-redirect-url https://ringi.example.com:8443/login/callback` |
| The name of the claim with the roles | `-oidc-roles-claim roles` |

[Prepare it and start it](operations.md#3-start-it) has the whole command.

Give the issuer exactly as it is in the document at `<issuer>/.well-known/openid-configuration`. If only the
`/` at its end differs, the server does not start.

## 4. Pass the roles

The roles that administer requests (`workflow_admin`, `system_admin`, `auditor`) are given to people at the
sign-in service. [What decides who may do what](concepts.md#what-decides-who-may-do-what) says what they let a
person do. Give the name of the claim that has them with `-oidc-roles-claim`. Its value is a string, or a list
of strings.

- People with a role can do what they could as staff, too. A person with no role is an ordinary member of the
  staff (`staff`).
- The server uses only the roles that are in the permissions (`project/shared/company.gate`). Other values are
  left out, and the log says so once.
- A role that is given or taken away counts from the next time that person signs in.
- With a service that passes groups by their IDs, the ID is the name of the role. Write the permissions with
  those values.
- The roles are in the cookie. If all of a person's groups are passed, they do not fit in it, and the person
  cannot sign in. Pass only the roles of this server.

## 5. See that it works

1. Open the start page in a browser: it goes to your organization's sign-in, and comes back when that is done.
2. Your name is at the top of the page.
3. The start page of a person with a role has the link "Administration".

If you could not sign in, look at the reason on the page, and at the log.

## What a person who signs in sees

When someone who is not signed in opens a page, it goes to the sign-in service, and back to that page when
that is done. A person stays signed in until 12 hours after signing in, or after 2 hours unused. "Sign out" is
at the top of every page.

These people cannot sign in. The page says why, and so does the log.

| Why | `reason` in the log |
|---|---|
| The sign-in service has not verified their mail address | `no_verified_email` |
| No one in the organization's data has that address | `not_in_organization` |
| Two people or more have that address | `ambiguous_email` |

Next, [Prepare it and start it](operations.md) starts the server with the rest of what it needs.
