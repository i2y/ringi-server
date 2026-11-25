#!/usr/bin/env python3
"""What the build of the site ends with.

- The nav of each language lists every page, and both list the same pages in the same order.
- The switch of languages at the head of a page leads to the same page in the other language.
- Every link, picture and anchor of a page is there, in the site or, for a link to a file of the
  repository on GitHub, in the repository.
"""

import html
import os
import re
import sys
import tomllib
import urllib.parse
from html.parser import HTMLParser
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parent
BUILD = HERE / "build"
BASE = "/ringi-server/"
LANGUAGES = {"en": ("zensical.toml", ""), "ja": ("zensical.ja.toml", "ja/")}
GITHUB = re.compile(r"https://github\.com/i2y/ringi-server/(?:blob|tree)/main/([^#]*)")


def nav(config: str) -> list[str]:
    def pages(entries: list) -> list[str]:
        out: list[str] = []
        for entry in entries:
            for to in entry.values():
                out += pages(to) if isinstance(to, list) else [to] if to.endswith(".md") else []
        return out

    return pages(tomllib.loads((HERE / config).read_text())["project"]["nav"])


def check_nav() -> list[str]:
    problems = []
    navs = {language: nav(config) for language, (config, _) in LANGUAGES.items()}
    for language, listed in navs.items():
        written = {str(p.relative_to(HERE / "pages" / language)) for p in (HERE / "pages" / language).rglob("*.md")}
        config = LANGUAGES[language][0]
        problems += [f"{config}: the nav does not list {p}" for p in sorted(written - set(listed))]
        problems += [f"{config}: the nav lists {p}, which no document is written as" for p in sorted(set(listed) - written)]
    if navs["en"] != navs["ja"]:
        problems.append("the navs of the two languages list the pages in different orders")
    return problems


def html_of(language: str) -> dict[str, Path]:
    """The built pages of a language, by their path from the root of the language's site."""
    root = BUILD / LANGUAGES[language][1]
    return {
        str(p.relative_to(root)): p
        for p in sorted(root.rglob("*.html"))
        if language != "en" or not p.is_relative_to(BUILD / "ja")
    }


def link_languages() -> None:
    """Makes the switch of each page lead to the page in the other language, not to its home page."""
    for path in html_of("en"):
        if not path.endswith("index.html"):
            continue
        at = path.removesuffix("index.html")
        for language in LANGUAGES:
            page = BUILD / LANGUAGES[language][1] / path
            text = page.read_text()
            for other, (_, prefix) in LANGUAGES.items():
                text, n = re.subn(rf'href="{BASE}{prefix}" hreflang="{other}"', f'href="{BASE}{prefix}{at}" hreflang="{other}"', text)
                if n == 0:
                    sys.exit(f"{page.relative_to(BUILD)} has no switch to {other}")
            page.write_text(text)


class Page(HTMLParser):
    def __init__(self, path: Path) -> None:
        super().__init__()
        self.ids: set[str] = set()
        self.links: list[str] = []
        self.feed(path.read_text())

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        for name, value in attrs:
            if name in ("id", "name") and value:
                self.ids.add(value)
            if name in ("href", "src") and value is not None:
                self.links.append(value)


def check_links() -> tuple[list[str], int]:
    problems, checked = [], 0
    pages = {p: Page(p) for p in sorted(BUILD.rglob("*.html"))}
    for path, page in pages.items():
        where = path.relative_to(BUILD)
        for link in page.links:
            if m := GITHUB.match(link):
                checked += 1
                if not (REPO / urllib.parse.unquote(m[1])).exists():
                    problems.append(f"{where}: {link} is not in the repository")
                continue
            to = urllib.parse.urlsplit(html.unescape(link))
            if to.scheme or to.netloc:
                continue
            checked += 1
            at = urllib.parse.unquote(to.path)
            if at.startswith(BASE):
                target = BUILD / at.removeprefix(BASE)
            elif at.startswith("/"):
                problems.append(f"{where}: {link} is outside the site")
                continue
            else:
                target = Path(os.path.normpath(path.parent / at)) if at else path
            if target.is_dir():
                target /= "index.html"
            if not target.exists():
                problems.append(f"{where}: {link} is not there")
            elif (anchor := urllib.parse.unquote(to.fragment)) and target in pages and anchor not in pages[target].ids:
                problems.append(f"{where}: {link} has no such heading")
    return problems, checked


def main() -> None:
    problems = check_nav()
    if set(html_of("en")) != set(html_of("ja")):
        problems.append("the two languages have different pages")
    if problems:
        sys.exit("\n".join(problems))
    link_languages()
    problems, checked = check_links()
    if problems:
        sys.exit("\n".join(problems))
    print(f"build/ and build/ja: {len(html_of('en'))} pages each, {checked} links lead somewhere")


if __name__ == "__main__":
    main()
