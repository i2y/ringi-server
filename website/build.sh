#!/usr/bin/env bash
# Builds the site into build/: the English pages, and the Japanese ones in build/ja. It needs Zensical
# in .venv:
#
#   python3 -m venv website/.venv
#   website/.venv/bin/pip install --require-hashes -r website/requirements.txt
#   website/build.sh
#
# pages.py writes the pages from the documents of the repository, and finish.py checks what was built.
# The English build cleans build/, so it goes first, and the plugin marketplace of the skill
# (marketplace.json, https://i2y.github.io/ringi-server/marketplace.json) is put at the root of
# build/ after both.
set -eu
cd "$(dirname "$0")"
.venv/bin/python pages.py
# The page cache is keyed on the Markdown, not on what renders it, so a change to a lexer would leave
# every cached page as it was.
rm -rf .cache
export PYTHONPATH="$PWD/tools${PYTHONPATH:+:$PYTHONPATH}" # the lexers that colour ritsu's languages
.venv/bin/zensical build --clean --strict
.venv/bin/zensical build --clean --strict -f zensical.ja.toml
cp marketplace.json build/marketplace.json
.venv/bin/python finish.py
