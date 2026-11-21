#!/bin/sh
# Writes the description and the screens of the request `steps` again (types/steps.json, and the
# form, card and list of a2ui/steps/ in English and in Japanese): a form of rates in steps of a part
# of a percent and of money in cents and in sen, which no example has. It is ritsu's machine test
# crates/ringi/tests/machine/steps.ringi, made beside ritsu's example requests (whose organization
# and calendar it uses) by the `ritsu` on PATH.
#
# Usage: testdata/steps/regen.sh <ritsu checkout>/crates/ringi/examples <ritsu checkout>/crates/ringi/tests/machine/steps.ringi
#
# --lang en keeps what ritsu writes in English, whatever language it speaks to the one who runs it.
# What ritsu wrote stays in a directory of its own under $TMPDIR, which the script names.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
examples=${1:?usage: regen.sh <the examples> <steps.ringi>}
steps=${2:?usage: regen.sh <the examples> <steps.ringi>}

work=$(mktemp -d "${TMPDIR:-/tmp}/ringi-regen.XXXXXX")
cp -R "$examples" "$work/project"
chmod -R u+w "$work/project" # a copy of read-only examples, so that the directory can be removed
sed 's|"../../examples/shared/|"shared/|' "$steps" >"$work/project/steps.ringi"
if ! (cd "$work/project" && ritsu gen . --root . --target requests \
	--catalog https://forms.example.test/a2ui/catalog.json --out "$work/out" --lang en) >"$work/gen.log" 2>&1; then
	cat "$work/gen.log" >&2
	exit 1
fi
mkdir -p "$here/types" "$here/a2ui/steps"
cp "$work/out/requests/types/steps.json" "$here/types/"
for s in form card list; do
	for l in en ja; do
		cp "$work/out/requests/a2ui/steps/$s.$l.json" "$here/a2ui/steps/"
	done
done
echo "written; what ritsu wrote is in $work"
