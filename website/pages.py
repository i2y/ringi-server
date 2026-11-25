#!/usr/bin/env python3
"""Writes the pages of the site, pages/en and pages/ja, from the documents of the repository.

Each document is a page, in English and in Japanese: README.md is the home page, docs/api.md the page
api, web/README.md the page web, DESIGN.md the page design and web/DESIGN.md the page design/web. A
link to another document leads to its page, and a link to any other file of the repository to the file
on GitHub. The sentence that names the document in the other language is left out, as every page has a
switch for it, and so is the one of the README that names the site.

The documents are read as GitHub reads them, too. What the site's Markdown would show in another way
stops this, naming the line, and so does a break in a sentence of Japanese, which shows as a space.
"""

import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parent
GITHUB = "https://github.com/i2y/ringi-server"
SUFFIX = {"en": ".md", "ja": ".ja.md"}

# What a page leaves out: the sentences that name the document in the other language, and the site.
LEFT_OUT = {
    "en": re.compile(r"\s*\bJapanese:\s+\[[^\]]+\]\([^)\s]+\.ja\.md\)\.|The documents are on \[the site\]\([^)]+\) too\.\n"),
    "ja": re.compile(r"英語版?(?:は |：)\[[^\]]+\]\([^)\s]+\.md\)(?: です)?。|文書は\[サイト\]\([^)]+\)でも読めます。\n"),
}
# A code span, or where a link, a picture or a tag leads.
TARGET = re.compile(r"(?P<code>(`+)(?!`).+?(?<!`)\2(?!`))|(?P<link>\]\((?P<to>[^)\s]+)\))|(?P<src>\bsrc=\"(?P<file>[^\"]+)\")", re.S)
FENCE = re.compile(r"(?ms)^```.*?^```[ ]*$")
ITEM = re.compile(r" {0,3}(?P<mark>[-*+]|\d+[.)]) ")


def documents() -> list[str]:
    """The English documents, each of which has a Japanese one beside it."""
    files = subprocess.run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "*.md"],
        cwd=REPO, check=True, capture_output=True, text=True,
    ).stdout.split()
    files = [f for f in files if not f.startswith("examples/") and "/testdata/" not in f and (REPO / f).exists()]
    english = [f for f in files if not f.endswith(".ja.md")]
    for f in english:
        if f.removesuffix(".md") + ".ja.md" not in files:
            sys.exit(f"{f} has no Japanese document beside it")
    if len(files) != 2 * len(english):
        sys.exit("a Japanese document has no English one beside it")
    return english


def page_of(document: str) -> str:
    """The page of an English document, as a path in pages/en and pages/ja."""
    directory, name = os.path.split(document)
    match name:
        case "README.md":
            return f"{directory}.md" if directory else "index.md"
        case "DESIGN.md":
            return f"design/{directory}.md" if directory else "design/index.md"
        case _:
            return f"{'' if directory == 'docs' else directory + '/'}{name.lower()}".lstrip("/")


def problems(text: str, language: str) -> list[tuple[int, str]]:
    """The lines that GitHub reads as one thing and the site's Markdown as another, and, in Japanese,
    the ones that break a sentence, as a break shows as a space."""
    out: list[tuple[int, str]] = []
    state = ""  # what the line before is: "paragraph", "item", "after" (an empty line after an item), "fence"
    kind = ""  # of the items of the list
    for n, line in enumerate(text.split("\n"), 1):
        item = ITEM.match(line)
        if line.startswith("```"):
            state = "" if state == "fence" else "fence"
        elif state == "fence":
            continue
        elif not line.strip():
            state = "after" if state in ("item", "after") else ""
        elif state == "paragraph":
            if item and item["mark"] in ("-", "*", "+", "1.", "1)"):
                out.append((n, "a list needs an empty line before it"))
            elif line.startswith("|"):
                out.append((n, "a table needs an empty line before it"))
            elif language == "ja":
                out.append((n, "a paragraph of Japanese is one line"))
        elif state == "item":
            if item and (line.startswith(" ") or item["mark"][-1] != kind):
                out.append((n, "a line of an item must not start like an item, unless it is one, indented by 4"))
            elif not item and language == "ja":
                out.append((n, "an item of Japanese is one line"))
        elif item:
            state, kind = "item", item["mark"][-1]
        elif state == "after" and line.startswith(" ") and not line.startswith("    "):
            out.append((n, "a second paragraph of an item is indented by 4"))
        else:
            state = "" if line.startswith(("#", "|")) else "paragraph"
    return out


def write(document: str, language: str, pages: dict[str, str]) -> None:
    source = document.removesuffix(".md") + SUFFIX[language]
    page = pages[document]
    text = (REPO / source).read_text()
    for n, why in problems(text, language):
        sys.exit(f"{source}:{n}: {why}")
    root = os.path.relpath(".", os.path.dirname(page) or ".")
    out = HERE / "pages" / language / page

    def target(to: str) -> str:
        path, sharp, anchor = to.partition("#")
        if "://" in to or not path:
            return to
        path = os.path.normpath(os.path.join(os.path.dirname(source), path))
        if not (REPO / path).exists():
            sys.exit(f"{source} links to {to}, which is not there")
        for suffix in (".ja.md", ".md"):
            if path.endswith(suffix) and path.removesuffix(suffix) + ".md" in pages:
                if suffix != SUFFIX[language]:
                    sys.exit(f"{source} links to {path}, which is in the other language")
                at = pages[path.removesuffix(suffix) + ".md"]
                return os.path.relpath(at, os.path.dirname(page) or ".") + sharp + anchor
        if path.startswith("docs/images/"):
            picture = HERE / "pages" / language / path.removeprefix("docs/")
            picture.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(REPO / path, picture)
            return os.path.normpath(os.path.join(root, path.removeprefix("docs/")))
        kind = "tree" if (REPO / path).is_dir() else "blob"
        return f"{GITHUB}/{kind}/main/{path}{sharp}{anchor}"

    def replace(m: re.Match[str]) -> str:
        if m["link"]:
            return f"]({target(m['to'])})"
        if m["src"]:
            # a tag's path is read as it is, from the page's URL: one directory deeper than the file
            to = target(m["file"])
            return f'src="{to if page.endswith("index.md") else os.path.join("..", to)}"'
        return m[0]

    parts: list[str] = []
    at = 0
    for block in [*FENCE.finditer(text), None]:
        prose = text[at : block.start() if block else len(text)]
        prose = LEFT_OUT[language].sub("", prose)
        prose = TARGET.sub(replace, prose)
        # a table's cell has `a \| b` in code; the site's Markdown takes the bar as it is there
        prose = "\n".join(
            re.sub(r"`[^`]*`", lambda m: m[0].replace("\\|", "|"), line) if line.startswith("|") else line
            for line in prose.split("\n")
        )
        parts.append(prose)
        if block:
            parts.append(block[0])
            at = block.end()
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(re.sub(r"\n{3,}", "\n\n", "".join(parts)))


def main() -> None:
    shutil.rmtree(HERE / "pages", ignore_errors=True)
    pages = {d: page_of(d) for d in documents()}
    for document in pages:
        for language in SUFFIX:
            write(document, language, pages)
    print(f"pages/en and pages/ja: {len(pages)} pages each")


if __name__ == "__main__":
    main()
