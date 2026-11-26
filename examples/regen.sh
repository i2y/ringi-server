#!/bin/sh
# Writes go/ (the Go package) and requests/ (the contract: openapi.json, types/, vectors/…, and the
# kinds as Wasm, kinds.wasm with wasm.json) again from project/, with the ritsu on PATH. go/flows/ is
# left out: its flows need the Temporal SDK, which a module of its own runs. --lang en: the code ritsu
# writes says its words in English (its comments, and the errors it answers), whatever language
# ritsu speaks to the one who runs it. --extra wasm builds the Wasm with cargo, which needs the target
# wasm32-unknown-unknown (rustup target add wasm32-unknown-unknown); wasmkind runs it (the
# framework's DESIGN 2.9).
#
# It writes v2/go and v2/requests too: a package of the second version of the leave requests
# (v2/project), which takes effect on 2027-04-01, for an administrator to switch (the framework's
# DESIGN 13.2). It is made from a copy of the project with those two files in place of its request
# types, with a catalog ID of its own: a renderer keeps one catalog by its ID, and the server serves
# both packages (the framework's web/DESIGN.md 2.3).
#
# Last, it writes bundles/leave-v3.zip: a bundle of the third version of the leave requests
# (v3/project), which asks how to reach the requester while they are away and takes effect on
# 2028-04-01, for the server to read as it starts (-bundles bundles) or for an administrator to upload
# (the framework's DESIGN 15). --extra bundle puts the requests directory and the files the kinds are
# made from into one zip, the same bytes from the same input; --diff-from puts in it what
# `ritsu ringi diff` says against a bundle of the second version, which is made beside it and not kept.
# The bundle has a catalog ID of its own too.
set -eu
cd "$(dirname "$0")"
rm -rf go requests v2/go v2/requests bundles/leave-v3.zip
(cd project && ritsu gen . --root . --target go --target requests --extra wasm \
	--catalog https://forms.example.test/a2ui/catalog.json \
	--module github.com/i2y/ringi-server/examples/go --out .. --lang en)
rm -rf go/flows
here=$(pwd)
two=$(mktemp -d "${TMPDIR:-/tmp}/ringi-regen.XXXXXX")
trap 'rm -rf "$two"' EXIT
cp -R project/. "$two"
rm "$two"/*.ringi
cp v2/project/*.ringi "$two"
(cd "$two" && ritsu gen . --root . --target go --target requests --extra wasm \
	--catalog https://forms.example.test/a2ui/v2/catalog.json \
	--module github.com/i2y/ringi-server/examples/v2/go --out "$here/v2" --lang en)
rm -rf v2/go/flows
base=$(mktemp -d "${TMPDIR:-/tmp}/ringi-regen.XXXXXX")
three=$(mktemp -d "${TMPDIR:-/tmp}/ringi-regen.XXXXXX")
trap 'rm -rf "$two" "$base" "$three"' EXIT
(cd "$two" && ritsu gen . --root . --target requests --extra wasm --extra bundle \
	--catalog https://forms.example.test/a2ui/v2/catalog.json --out "$base" --lang en)
rm "$two"/*.ringi
cp v3/project/*.ringi "$two"
(cd "$two" && ritsu gen . --root . --target requests --extra wasm --extra bundle \
	--diff-from "$base/requests.zip" \
	--catalog https://forms.example.test/a2ui/v3/catalog.json --out "$three" --lang en)
mkdir -p bundles
cp "$three/requests.zip" bundles/leave-v3.zip
