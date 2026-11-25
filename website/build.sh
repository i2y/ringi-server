#!/usr/bin/env bash
# Builds the site into build/: the English pages, and the Japanese ones in build/ja. It needs Zensical
# in .venv:
#
#   python3 -m venv website/.venv
#   website/.venv/bin/pip install --require-hashes -r website/requirements.txt
#   website/build.sh
#
# pages.py writes the pages from the documents of the repository, and finish.py checks what was built.
# The English build cleans build/, so it goes first.
set -eu
cd "$(dirname "$0")"
.venv/bin/python pages.py
.venv/bin/zensical build --clean --strict
.venv/bin/zensical build --clean --strict -f zensical.ja.toml
.venv/bin/python finish.py
