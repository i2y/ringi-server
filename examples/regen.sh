#!/bin/sh
# Writes go/ (the Go package) and requests/ (the contract: openapi.json, types/, vectors/…) again
# from project/, with the ritsu on PATH. go/flows/ is left out: its flows need the Temporal SDK,
# which a module of its own runs. --lang en: the code ritsu writes says its words in English (its
# comments, and the errors it answers), whatever language ritsu speaks to the one who runs it.
#
# It writes v2/go and v2/requests too: a package of the second version of the leave requests
# (v2/project), which takes effect on 2027-04-01, for an administrator to switch (the framework's
# DESIGN 13.2). It is made from a copy of the project with those two files in place of its request
# types, with a catalog ID of its own: a renderer keeps one catalog by its ID, and the server serves
# both packages (the framework's web/DESIGN.md 2.3).
set -eu
cd "$(dirname "$0")"
rm -rf go requests v2/go v2/requests
(cd project && ritsu gen . --root . --target go --target requests \
	--catalog https://forms.example.test/a2ui/catalog.json \
	--module github.com/i2y/ringi-server/examples/go --out .. --lang en)
rm -rf go/flows
here=$(pwd)
two=$(mktemp -d "${TMPDIR:-/tmp}/ringi-regen.XXXXXX")
trap 'rm -rf "$two"' EXIT
cp -R project/. "$two"
rm "$two"/*.ringi
cp v2/project/*.ringi "$two"
(cd "$two" && ritsu gen . --root . --target go --target requests \
	--catalog https://forms.example.test/a2ui/v2/catalog.json \
	--module github.com/i2y/ringi-server/examples/v2/go --out "$here/v2" --lang en)
rm -rf v2/go/flows
