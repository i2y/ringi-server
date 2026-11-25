#!/usr/bin/env python3
"""Draws the figures of the documents, each in English and in Japanese: python3 docs/images/figures.py

The words of both languages are side by side here, so that a figure changed in one is changed in the
other. The figures have their own ground, and follow the reader's light or dark colours."""

import html
from pathlib import Path

HERE = Path(__file__).resolve().parent
STYLE = """<style>
text { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "Hiragino Sans", "Noto Sans JP", "Yu Gothic", sans-serif; font-size: 13px; fill: #334155; }
.h { font-weight: 700; font-size: 15px; fill: #0f172a; }
.s { font-weight: 700; font-size: 13.5px; fill: #0f172a; }
.n { font-weight: 700; font-size: 14px; fill: #ffffff; }
.k { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; fill: #1d4ed8; }
.l { font-size: 12.5px; fill: #475569; }
.ground { fill: #f8fafc; }
.box { fill: #ffffff; stroke: #cbd5e1; stroke-width: 1.2; }
.main { fill: #eff6ff; stroke: #1d4ed8; stroke-width: 1.8; }
.good { fill: #f0fdf4; stroke: #15803d; stroke-width: 1.4; }
.end { fill: #f1f5f9; stroke: #94a3b8; stroke-width: 1.2; }
.dot { fill: #1d4ed8; }
.arrow { stroke: #64748b; stroke-width: 1.6; fill: none; }
.back { stroke: #64748b; stroke-width: 1.4; fill: none; stroke-dasharray: 5 4; }
.head { fill: #64748b; }
@media (prefers-color-scheme: dark) {
text { fill: #cbd5e1; } .h, .s { fill: #f1f5f9; } .n { fill: #0f172a; } .k { fill: #93c5fd; } .l { fill: #cbd5e1; }
.ground { fill: #0f172a; } .box { fill: #1e293b; stroke: #475569; } .main { fill: #172554; stroke: #93c5fd; }
.good { fill: #052e16; stroke: #4ade80; } .end { fill: #1e293b; stroke: #64748b; } .dot { fill: #93c5fd; }
.arrow, .back { stroke: #94a3b8; } .head { fill: #94a3b8; }
}
</style>
<defs><marker id="head" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto"><path class="head" d="M0 0L8 4L0 8Z"/></marker></defs>"""


class Figure:
    def __init__(self, lang: str, width: int, height: int, title: str, desc: str) -> None:
        e = html.escape
        self.out = [
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}" role="img" aria-labelledby="t d" lang="{lang}">',
            f'<title id="t">{e(title)}</title><desc id="d">{e(desc)}</desc>',
            STYLE,
            f'<rect class="ground" width="{width}" height="{height}" rx="14"/>',
        ]

    def box(self, x: int, y: int, w: int, h: int, cls: str = "box") -> None:
        self.out.append(f'<rect class="{cls}" x="{x}" y="{y}" width="{w}" height="{h}" rx="10"/>')

    def text(self, x: float, y: float, s: str, cls: str = "t", anchor: str = "start") -> None:
        self.out.append(f'<text class="{cls}" x="{x}" y="{y}" text-anchor="{anchor}">{html.escape(s)}</text>')

    def arrow(self, *points: tuple[float, float], cls: str = "arrow") -> None:
        d = "".join(f"{'ML'[i > 0]}{x} {y}" for i, (x, y) in enumerate(points))
        self.out.append(f'<path class="{cls}" d="{d}" marker-end="url(#head)"/>')

    def titled(self, x: int, y: int, w: int, h: int, title: str, lines: list[str], cls: str = "box", lead: int = 20) -> None:
        self.box(x, y, w, h, cls)
        self.text(x + 16, y + 27, title, "h")
        for i, l in enumerate(lines):
            self.text(x + 16, y + 52 + i * lead, l)

    def middle(self, x: int, y: int, w: int, h: int, title: str, under: str = "", cls: str = "box") -> None:
        self.box(x, y, w, h, cls)
        self.text(x + w / 2, y + h / 2 + (5 if not under else -4), title, "s", "middle")
        if under:
            self.text(x + w / 2, y + h / 2 + 16, under, "k", "middle")

    def number(self, x: int, y: int, n: int) -> None:
        self.out.append(f'<circle class="dot" cx="{x}" cy="{y}" r="13"/>')
        self.text(x, y + 5, str(n), "n", "middle")

    def save(self, name: str) -> None:
        (HERE / name).write_text("\n".join([*self.out, "</svg>"]) + "\n")


OVERVIEW = {
    "en": dict(
        title="How ringi-server fits together",
        desc="You write the request types, the organization and the permissions. ritsu checks them and makes Go code. Your server runs that code with ringi-server, for people, programs and agents, and keeps everything in PostgreSQL.",
        write=("1. You write", ["request types (.ringi)", "the organization's data (CSV)", "permissions (.gate), holidays (.cal)"]),
        gen="check, generate",
        made=("2. ritsu makes", ["Go code that decides how", "each request moves on", "the API and the screens"]),
        serve=("3. You run the server", ["a Go program on ringi-server,", "with your own mail, sign-in", "and whatever else you connect"]),
        left=[("People", "pages in a browser"), ("Programs", "the HTTP API"), ("Agents", "tools (MCP)")],
        server=("The server (ringi-server)", ["knows who sends each request", "checks that they may do it", "moves the request on, as the code says", "keeps every request and its history", "watches deadlines, sends notices"]),
        right=["PostgreSQL", "Sign-in (OpenID Connect)", "Notices: mail, chat", "Books: budget, days of leave", "Work after approval: Temporal"],
    ),
    "ja": dict(
        title="ringi-server の全体",
        desc="申請の種類、組織のデータ、権限をファイルに書く。ritsu がそれを検査して Go のコードを作る。ringi-server を使ったサーバーがそのコードを動かし、人とプログラムとエージェントが使う。申請はすべて PostgreSQL に残る。",
        write=("1. 書く", ["申請の種類（.ringi）", "組織のデータ（CSV）", "権限（.gate）、休日（.cal）"]),
        gen="検査して生成",
        made=("2. ritsu が作る", ["申請をどう進めるかを決める", "Go のコード", "API と画面の定義"]),
        serve=("3. サーバーを動かす", ["ringi-server を使う Go のプログラム。", "メールやログインなど、つなぐものは", "自分で渡す"]),
        left=[("人", "ブラウザのページ"), ("プログラム", "HTTP の API"), ("エージェント", "ツール（MCP）")],
        server=("サーバー（ringi-server）", ["だれの操作かを確かめる", "その操作をしてよいかを確かめる", "作られたコードのとおりに申請を進める", "申請と履歴を保存する", "期限を見張り、通知を送る"]),
        right=["PostgreSQL", "ログインのプロバイダー", "通知：メール、チャット", "帳簿：予算、休暇の残り", "承認のあとの処理：Temporal"],
    ),
}


def overview(lang: str, d: dict) -> None:
    f = Figure(lang, 900, 432, d["title"], d["desc"])
    f.titled(24, 24, 236, 112, *d["write"])
    f.arrow((260, 80), (366, 80))
    f.text(313, 70, d["gen"], "l", "middle")
    f.titled(368, 24, 212, 112, *d["made"])
    f.titled(608, 24, 268, 112, *d["serve"])
    f.arrow((580, 80), (606, 80))
    f.arrow((742, 136), (742, 166), (460, 166), (460, 196))
    for i, (a, b) in enumerate(d["left"]):
        y = 204 + i * 68
        f.box(24, y, 190, 54)
        f.text(40, y + 23, a, "s")
        f.text(40, y + 42, b)
        f.arrow((214, y + 27), (290, y + 27))
    f.titled(292, 198, 336, 206, *d["server"], cls="main", lead=32)
    for i, a in enumerate(d["right"]):
        y = 198 + i * 42
        f.box(660, y, 216, 34)
        f.text(672, y + 22, a)
        f.arrow((628, y + 17), (658, y + 17))
    f.save(f"overview.{lang}.svg")


STATES = {
    "en": dict(
        title="The states of a request",
        desc="A submitted request is in progress. It can be put on hold and taken off hold. An approver's send-back or the requester's withdrawal returns it to the requester, who submits it again or discards it. It ends approved, rejected, expired or withdrawn.",
        submit="submit",
        progress=("In progress", "in_progress"),
        paused=("On hold", "paused"),
        requester=("With the requester", "with_requester"),
        approved=("Approved", "approved"),
        rejected=("Rejected", "rejected"),
        expired=("Expired", "expired"),
        withdrawn=("Withdrawn", "withdrawn"),
        pause="put on, off hold",
        back=["sent back", "or withdrawn"],
        again="submit again",
        last="approved at the last stage",
        reject="rejected",
        late="the deadline passes",
        discard="discarded",
    ),
    "ja": dict(
        title="申請の状態",
        desc="出した申請は進行中になる。保留にして、また進めることもできる。承認者が差し戻すか、申請者が取り下げると、申請者に戻る。申請者は再申請するか、申請をやめる。最後は、承認、却下、期限切れ、取り下げのどれかで終わる。",
        submit="申請する",
        progress=("進行中", "in_progress"),
        paused=("保留中", "paused"),
        requester=("申請者に戻っています", "with_requester"),
        approved=("承認されました", "approved"),
        rejected=("却下されました", "rejected"),
        expired=("期限が切れました", "expired"),
        withdrawn=("取り下げられました", "withdrawn"),
        pause="保留／解除",
        back=["差し戻し、", "取り下げ"],
        again="再申請",
        last="最後のステップで承認",
        reject="却下",
        late="期限を過ぎた",
        discard="申請をやめる",
    ),
}


def states(lang: str, d: dict) -> None:
    f = Figure(lang, 900, 356, d["title"], d["desc"])
    f.arrow((28, 74), (196, 74))
    f.text(112, 64, d["submit"], "l", "middle")
    f.middle(198, 40, 220, 68, *d["progress"], cls="main")
    f.middle(28, 250, 180, 64, *d["paused"])
    f.middle(290, 250, 220, 64, *d["requester"])
    for i, (key, cls) in enumerate((("approved", "good"), ("rejected", "end"), ("expired", "end"), ("withdrawn", "end"))):
        f.middle(676, (24, 98, 172, 256)[i], 196, 56, *d[key], cls=cls)
    # in progress and paused
    f.arrow((232, 108), (232, 180), (118, 180), (118, 248))
    f.arrow((146, 250), (146, 206), (258, 206), (258, 110), cls="back")
    f.text(40, 226 - 60, d["pause"], "l")
    # in progress and with the requester
    f.arrow((330, 108), (330, 248))
    for i, l in enumerate(d["back"]):
        f.text(340, 138 + i * 17, l, "l")
    f.arrow((470, 250), (470, 90), (420, 90), cls="back")
    f.text(462, 226, d["again"], "l", "end")
    # the ends
    f.arrow((418, 52), (674, 52))
    f.text(548, 43, d["last"], "l", "middle")
    f.arrow((418, 70), (560, 70), (560, 126), (674, 126))
    f.text(570, 117, d["reject"], "l")
    f.arrow((418, 84), (540, 84), (540, 194), (674, 194))
    f.text(550, 185, d["late"], "l")
    f.arrow((510, 270), (598, 270), (598, 214), (674, 214), cls="arrow")
    f.arrow((510, 292), (674, 292))
    f.text(548, 310, d["discard"], "l")
    f.save(f"states.{lang}.svg")


PRESSED = {
    "en": dict(
        title="What happens when a button is pressed",
        desc="The server finds who pressed the button, asks the gate whether they may, asks the generated code what happens, and saves everything in one transaction. It then answers, and sends the notices after that, again and again until each is done.",
        person=("Manager of Sales 1", "presses Approve"),
        answer=("The page shows", "the next stage"),
        server="The server",
        rows=[
            ("Who is it?", "the person who is signed in", "Sign-in", "your company's provider"),
            ("May they?", "approve this request, now", "The gate", "permissions, in .gate"),
            ("What happens?", "to the next stage, with whom", "The generated code", "from the .ringi file"),
            ("Save it, all at once", "the request, its history, what to do", "PostgreSQL", "one transaction"),
            ("Afterwards", "notices and the rest, until done", "Mail, chat, flows…", "the parts you connect"),
        ],
    ),
    "ja": dict(
        title="ボタンを押すと起きること",
        desc="サーバーは、だれが押したかを確かめ、してよいかを gate に尋ね、どうなるかを作られたコードに尋ね、すべてを一つのトランザクションで保存して応答する。通知はそのあとで送り、済むまでやり直す。",
        person=("営業一課長", "「承認する」を押す"),
        answer=("ページに次の", "ステップが出る"),
        server="サーバー",
        rows=[
            ("だれか", "ログインしている人", "ログイン", "会社のプロバイダー"),
            ("してよいか", "この申請を、いま承認してよい人か", "gate", "権限。.gate のファイル"),
            ("どうなるか", "次はどのステップで、だれが答えるか", "作られたコード", ".ringi のファイルから"),
            ("まとめて保存する", "申請、履歴、このあとすること", "PostgreSQL", "一つのトランザクション"),
            ("そのあとにする", "通知などを、済むまでやり直す", "メール、チャット、flow", "自分でつなぐ部品"),
        ],
    ),
}


def pressed(lang: str, d: dict) -> None:
    f = Figure(lang, 900, 440, d["title"], d["desc"])
    f.box(24, 72, 170, 64)
    f.text(109, 99, d["person"][0], "s", "middle")
    f.text(109, 119, d["person"][1], "l", "middle")
    f.arrow((194, 104), (244, 104))
    f.box(246, 24, 372, 392, "main")
    f.text(264, 51, d["server"], "h")
    for i, (title, under, part, where) in enumerate(d["rows"]):
        y = 68 + i * 70
        f.number(278, y + 28, i + 1)
        f.text(304, y + 24, title, "s")
        f.text(304, y + 44, under)
        if i < 4:
            f.arrow((278, y + 43), (278, y + 82))
        f.box(676, y, 200, 56)
        f.text(692, y + 24, part, "s")
        f.text(692, y + 43, where, "l")
        f.arrow((618, y + 28), (674, y + 28))
    f.box(24, 282, 170, 64)
    f.text(109, 309, d["answer"][0], "s", "middle")
    f.text(109, 329, d["answer"][1], "s", "middle")
    f.arrow((246, 314), (196, 314))
    f.save(f"pressed.{lang}.svg")


DEPLOY = {
    "en": dict(
        title="A server for people",
        desc="Browsers reach a proxy that serves https, which passes each request to one of the server's processes. The processes share one PostgreSQL database, sign people in through the company's provider, and send notices and the rest through what you connect.",
        browser=("People's browsers", "https"),
        proxy=("A proxy", "serves https"),
        processes=("The server", "one process, or several"),
        db=("PostgreSQL", "requests, history, attachments,", "timers, records"),
        right=[("Sign-in provider", "OpenID Connect"), ("Mail, chat", "your notifier"), ("Temporal and others", "only if you use them")],
    ),
    "ja": dict(
        title="社員に使ってもらうサーバー",
        desc="社員のブラウザは、https を受けるプロキシに届き、プロキシはサーバーのプロセスの一つにリクエストを渡す。プロセスは一つの PostgreSQL のデータベースを共有し、会社のプロバイダーでログインさせ、通知などはつないだ先へ送る。",
        browser=("社員のブラウザ", "https"),
        proxy=("プロキシ", "https を受ける"),
        processes=("サーバー", "プロセスは一つでも複数でもよい"),
        db=("PostgreSQL", "申請、履歴、添付ファイル、", "タイマー、記録"),
        right=[("ログインのプロバイダー", "OpenID Connect"), ("メール、チャット", "プロジェクトの Notifier"), ("Temporal など", "使うときだけ")],
    ),
}


def deploy(lang: str, d: dict) -> None:
    f = Figure(lang, 900, 300, d["title"], d["desc"])

    def two(x: int, y: int, w: int, h: int, a: str, b: str, cls: str = "box") -> None:
        f.box(x, y, w, h, cls)
        f.text(x + w / 2, y + h / 2 - 4, a, "s", "middle")
        f.text(x + w / 2, y + h / 2 + 16, b, "l", "middle")

    two(24, 88, 140, 64, *d["browser"])
    f.arrow((164, 120), (206, 120))
    two(208, 88, 130, 64, *d["proxy"])
    f.arrow((338, 120), (380, 120))
    f.box(394, 66, 220, 92, "main")
    f.box(382, 78, 220, 92, "main")
    f.text(492, 118, d["processes"][0], "h", "middle")
    f.text(492, 140, d["processes"][1], "l", "middle")
    f.arrow((492, 170), (492, 208))
    f.box(372, 210, 240, 72)
    f.text(492, 234, d["db"][0], "s", "middle")
    f.text(492, 253, d["db"][1], "l", "middle")
    f.text(492, 270, d["db"][2], "l", "middle")
    for i, (a, b) in enumerate(d["right"]):
        y = 24 + i * 72
        two(668, y, 208, 58, a, b)
        f.arrow((614, 112), (640, 112), (640, y + 29), (666, y + 29))
    f.save(f"deploy.{lang}.svg")


for lang in ("en", "ja"):
    overview(lang, OVERVIEW[lang])
    states(lang, STATES[lang])
    pressed(lang, PRESSED[lang])
    deploy(lang, DEPLOY[lang])
