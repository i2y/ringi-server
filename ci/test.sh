#!/usr/bin/env bash
# Runs `go test` with the arguments in the module of the current directory, and fails on a test that
# was skipped as on one that failed: where this runs (.github/workflows/ci.yml) everything the tests
# need is installed, and a test that did not run must not look as if it passed.
#
#   ci/test.sh -race ./...                    # in the root
#   ../ci/test.sh ./... -skip '^TestExamples$'   # a test that is left out on purpose is named
set -u
log=$(mktemp)
trap 'rm -f "$log"' EXIT
go test -count=1 -v "$@" >"$log" 2>&1
status=$?

if [ "$status" -ne 0 ]; then
	cat "$log"
	echo "ci/test.sh: go test failed ($status)" >&2
	exit "$status"
fi
grep -E '^(ok|\?) ' "$log"
passed=$(grep -c -E '^ *--- PASS:' "$log")
echo "passed: $passed, subtests counted"
if [ "$passed" -eq 0 ]; then
	echo "ci/test.sh: no test ran" >&2
	exit 1
fi
if grep -E '^ *--- SKIP:|SKIP: ' "$log"; then
	echo "ci/test.sh: the tests above were skipped; nothing is skipped here" >&2
	exit 1
fi
