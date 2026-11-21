#!/bin/sh
# Writes the files the tests hold the screens to (testdata/golden/) again, from what this module
# makes now. Read the difference before keeping it:
#
#   testdata/bless.sh && git diff -- testdata/golden
set -eu
cd "$(dirname "$0")/.."
SCREENS_BLESS=1 go test -count=1 -run '^TestGolden$' .
