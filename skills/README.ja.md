# ringi-server の Agent Skill

このフォルダーには、[Agent Skill](https://agentskills.io) が一つ、[ringi-server](ringi-server/SKILL.md) があります。このフレームワークの上に作るプロジェクト（`ritsu ringi init` が書くプロジェクトや、モジュールを組み込んだ Go のサーバー）のためのものです。Agent Skills を読むエージェント（Claude Code など）は、作業がスキルの説明に合うとその `SKILL.md` を読み、[reference.md](ringi-server/reference.md)（フラグ、設定、URL、API の応答）は要るときにだけ読みます。プロジェクトで ringi-server を*使う*ためのスキルで、このリポジトリで作業するためのものではありません。英語版は [README.md](README.md) です。

| スキル | 使う場面 |
|---|---|
| [ringi-server](ringi-server/SKILL.md) | 申請のプロジェクトのサーバー。申請の種類や版を足す、組織のデータ、ゲート、ログイン、通知とメール、カレンダー、承認のあとの仕事（Temporal）、帳簿、エージェントのツール、ページの見た目、署名したバンドル、PostgreSQL での運用、テスト、API が断る理由 |

申請の種類そのもの（`.ringi` のファイル）を書くのは、[ritsu](https://github.com/i2y/ritsu/tree/main/skills) のスキル `ringi` です。そういうプロジェクトで働くエージェントは、二つを一緒に使います。`project/` のファイルに何を書くかは `ringi`、そのファイルをどこに置き、サーバーがどう読み、変えたあと何を走らせるかは `ringi-server` が受け持ちます。スキルは `ritsu` と `go` を動かすので、どちらも PATH に要ります。版は [版の表](../README.ja.md#版) の組にそろえます。

## 入れ方

### Claude Code：プラグイン

フレームワークのサイトが、Claude Code のプラグインのマーケットプレイス `ringi-server` を公開しています。マーケットプレイスには同じ名前のプラグイン `ringi-server` が一つあり、そこにスキルが入っています。

```text
/plugin marketplace add https://i2y.github.io/ringi-server/marketplace.json
/plugin install ringi-server@ringi-server
```

マーケットプレイスはこの一つのファイルで、プラグインはこのリポジトリの `skills/` のフォルダーです。Claude Code はこのフォルダーだけを取ってくるので、リポジトリ全体はダウンロードしません。Claude Code は、スキルにプラグインの名前を付けて `ringi-server:ringi-server` と呼びます。プラグインのバージョンはフレームワークのバージョンと同じなので、ringi-server の新しいリリースが、プラグインの新しいバージョンになります。

### 手で：フォルダーをコピーする

リポジトリのクローンから、エージェントがスキルを読む場所にフォルダーをコピーします。

```sh
git clone --depth 1 https://github.com/i2y/ringi-server.git
cp -r ringi-server/skills/ringi-server <your-project>/.claude/skills/   # 一つのプロジェクトで使い、一緒にコミットする
cp -r ringi-server/skills/ringi-server ~/.claude/skills/                # このマシンのすべてのプロジェクトで使う
```

ほかのエージェントには、そのエージェントがスキルを読むディレクトリに、フォルダー `ringi-server` をコピーします。

### 言語のスキル

スキル `ringi` は ritsu に入っていて、`ritsu` のバイナリと同じ版です。

```sh
ritsu skills install ringi
```

プロジェクトに `.claude/skills/ringi/` を書きます（`--user` なら `~/.claude/skills/`、`--dir` ならほかのディレクトリ）。Claude Code では、プラグイン `ritsu` にも入っています。

```text
/plugin marketplace add https://i2y.github.io/ritsu/marketplace.json
/plugin install ritsu@ritsu
```

## どう保っているか

スキルは手で書いています。リポジトリの根にある `skill_test.go` が、スキルとコードの食い違いを確かめます。`SKILL.md` のフロントマターとライセンス、リンクがフォルダーの中か、サイトにあるページを指すこと、挙げたフラグがサーバーか `ringi-bundle` のものであること、挙げた Go の名前がフレームワークにあること、マーケットプレイスが [版の表](../README.ja.md#版) のいちばん新しい版を書いていること、です。
