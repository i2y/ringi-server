#!/bin/sh
# Writes the generated files the tests read again, from ritsu's example requests:
#
#   requests/  openapi.json, types/, a2ui/ and organization/find.jsonl of `ritsu gen --target
#              requests` (the vectors of the finds, which no kind's vectors hold)
#   vectors/   its vectors, one gzip file for each kind, and in vectors/worked/ those of what each
#              kind's form works out (the kind's Worked entry), one gzip file for each kind
#   gen/       the Go packages of `ritsu gen --target go` the vectors are run with (the list of the
#              kinds, ticketkinds, and what it imports; nothing that needs a module from outside)
#
# Usage: testdata/regen.sh <a ritsu checkout>/crates/ringi/examples
#
# `ritsu` must be on PATH; --lang en keeps what it writes in English, whatever language it speaks to
# the one who runs it. The files are written over the ones here; a file here that the new
# generation does not write is named, and the script fails, so that it can be removed by hand. What
# ritsu wrote stays in a directory of its own under $TMPDIR, which the script names.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
examples=${1:?usage: regen.sh <a ritsu checkout>/crates/ringi/examples}
module=github.com/i2y/ringi-server/screens/testdata/gen
catalog=https://forms.example.test/a2ui/catalog.json

work=$(mktemp -d "${TMPDIR:-/tmp}/ringi-regen.XXXXXX")
mkdir "$work/ex"
cp -R "$examples" "$work/ex/examples"
chmod -R u+w "$work/ex" # a copy of read-only examples, so that the directory can be removed
if ! (cd "$work/ex" && ritsu gen examples --root . --target go --target requests \
	--catalog "$catalog" --module "$module" --out "$work/out" --lang en) >"$work/gen.log" 2>&1; then
	cat "$work/gen.log" >&2
	exit 1
fi

# what the tests read, in a tree of its own beside the one here
new="$work/new"
mkdir -p "$new/requests/types" "$new/requests/organization" "$new/vectors/worked" "$new/gen"
cp "$work/out/requests/openapi.json" "$new/requests/"
cp "$work/out/requests/types/"*.json "$new/requests/types/"
cp "$work/out/requests/organization/find.jsonl" "$new/requests/organization/"
cp -R "$work/out/requests/a2ui" "$new/requests/a2ui"
for v in "$work/out/requests/vectors/"*.jsonl; do
	gzip -n -c "$v" >"$new/vectors/$(basename "$v").gz"
done
for v in "$work/out/requests/vectors/worked/"*.jsonl; do
	gzip -n -c "$v" >"$new/vectors/worked/$(basename "$v").gz"
done
for p in ticketkinds glue dates rules units; do
	cp -R "$work/out/go/$p" "$new/gen/$p"
done

# the files here the new generation does not write
stale=$(cd "$here" && find requests vectors gen -type f 2>/dev/null | LC_ALL=C sort | while read -r f; do
	[ -e "$new/$f" ] || echo "$f"
done)

for d in requests vectors gen; do
	mkdir -p "$here/$d"
	cp -R "$new/$d/." "$here/$d/"
done

echo "written from $examples; what ritsu wrote is in $work"
if [ -n "$stale" ]; then
	echo "these files are not written by the new generation; remove them:" >&2
	echo "$stale" >&2
	exit 1
fi
