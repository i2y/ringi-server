# The Agent Skill of ringi-server

This folder holds one [Agent Skill](https://agentskills.io), [ringi-server](ringi-server/SKILL.md), for the projects
built on this framework: the project `ritsu ringi init` writes, or a Go server that embeds the module. An agent that
reads Agent Skills (Claude Code is one) reads its `SKILL.md` when a task matches the skill's description, and
[reference.md](ringi-server/reference.md) (the flags, the settings, the URLs and the answers of the API) only when it
needs it. The skill is about *using* ringi-server in a project, not about working on this repository. Japanese:
[README.ja.md](README.ja.md).

| Skill | Use it for |
|---|---|
| [ringi-server](ringi-server/SKILL.md) | the server of a project of requests: a new kind of request or a new version of one, the organization's data, the gate, sign-in, notices and mail, the calendar, work after an approval on Temporal, books, agents' tools, the look of the pages, signed bundles, running it on PostgreSQL, testing it, and what its API refuses |

Writing the request types themselves, the `.ringi` files, is the skill `ringi` of
[ritsu](https://github.com/i2y/ritsu/tree/main/skills). An agent working on such a project uses the two together:
`ringi` for what a file of `project/` says, and `ringi-server` for where it sits, how the server reads it and what
to run after changing it. The skill runs `ritsu` and `go`, so both have to be on PATH, at a pair of versions of
[the table of versions](../README.md#versions).

## Install

### Claude Code: the plugin

The framework's site publishes a Claude Code plugin marketplace, `ringi-server`, with one plugin, also
`ringi-server`, which holds the skill:

```text
/plugin marketplace add https://i2y.github.io/ringi-server/marketplace.json
/plugin install ringi-server@ringi-server
```

The marketplace is that one file, and the plugin is the folder `skills/` of this repository, which Claude Code
checks out alone rather than the whole repository. Claude Code names the skill after the plugin:
`ringi-server:ringi-server`. The plugin takes the version of the framework, so a new release of ringi-server is a
new version of the plugin.

### By hand: copy the folder

From a clone of the repository, copy the folder where the agent reads skills:

```sh
git clone --depth 1 https://github.com/i2y/ringi-server.git
cp -r ringi-server/skills/ringi-server <your-project>/.claude/skills/   # one project, committed with it
cp -r ringi-server/skills/ringi-server ~/.claude/skills/                # every project on this machine
```

For another agent, copy the folder `ringi-server` into the directory it reads skills from.

### The skill of the language

The skill `ringi` comes with ritsu, at the version of the `ritsu` binary:

```sh
ritsu skills install ringi
```

It writes `.claude/skills/ringi/` in the project (`--user` for `~/.claude/skills/`, `--dir` for another
directory). In Claude Code, the plugin `ritsu` has it too:

```text
/plugin marketplace add https://i2y.github.io/ritsu/marketplace.json
/plugin install ritsu@ritsu
```

## How it is kept

The skill is written by hand. `skill_test.go` at the root of the repository holds it to the code: the frontmatter
and the license of `SKILL.md`, that its links stay in the folder or are pages the site has, that each flag it names
is one the server or `ringi-bundle` takes, that each Go name it names is in the framework, and that the
marketplace says the newest version of [the table of versions](../README.md#versions).
