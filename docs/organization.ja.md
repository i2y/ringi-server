# 組織のデータを用意する

だれが承認するかは、組織のデータから決まります。人、チーム、所属を書いた三つの CSV のファイルです。異動があれば、新しい版を足します。申請の種類のファイルを書き直す必要はありません。英語版は [organization.md](organization.md) です。

## ファイルの置き場所

```
project/
├── shared/
│   └── company.ringi      役職とチームの種類
└── org/
    ├── 2026-04-01/        2026 年 4 月 1 日からの組織
    │   ├── teams.csv
    │   ├── people.csv
    │   └── members.csv
    ├── 2026-12-01/        2026 年 12 月 1 日からの組織
    │   └── …
    └── proxies.csv        代理（なくてもよい）
```

ディレクトリの名前が、その組織が始まる日です。一つのディレクトリが、組織のデータの一つの版です。承認者は、ステップが始まる日に有効な版から決まります。

## 三つのファイル

### チーム（teams.csv）

会社、部、課などです。

```
id,name,name_ja,kind,parent,head
company,Example Co.,サンプル株式会社,company,,p0001
sales,Sales Department,営業部,department,company,s1001
sales_1,Sales Section 1,営業一課,section,sales,s1101
```

| 列 | 書くもの |
|---|---|
| `id` | チームの ID |
| `name`、`name_ja` | 名前と、日本語の名前。`name_ja` は省ける |
| `kind` | チームの種類。`company.ringi` の `team kinds` のどれか |
| `parent` | 上のチームの ID。いちばん上は空 |
| `head` | 長の ID。いないあいだは空 |

### 人（people.csv）

```
id,name,name_ja,email
s1001,Head of Sales,営業部長,s1001@example.com
s1101,Manager of Sales Section 1,営業一課長,s1101@example.com
s1201,Sales staff 1,営業担当 1,s1201@example.com
```

| 列 | 書くもの |
|---|---|
| `id` | 人の ID。一度決めたら変えない |
| `name`、`name_ja` | 名前と、日本語の名前。`name_ja` は省ける |
| `email` | メールアドレス。ログインした人を探すのと、通知を送るのに使う |
| `language` | 通知とページの言語。`en` か `ja`。列ごと省ける |

### 所属（members.csv）

だれがどのチームに属するかです。

```
person,team,position,primary
s1001,sales,head,yes
s1101,sales_1,manager,yes
s1201,sales_1,staff,yes
```

| 列 | 書くもの |
|---|---|
| `person`、`team` | 人の ID とチームの ID |
| `position` | 役職。`company.ringi` の `positions` のどれか |
| `primary` | 主な所属なら `yes`。兼務の行は `no` か空 |

どの人にも、主な所属が一つ必要です。上長と部長は、主な所属からたどって決まります（[承認者はどう決まるか](concepts.ja.md#承認者はどう決まるか)）。

## 役職とチームの種類

役職とチームの種類は、`project/shared/company.ringi` に書きます。

```ringi title="project/shared/company.ringi"
organization company v1 "会社"
description "組織の形。役職を低いほうから、チームの種類を広いほうから並べ、版ごとのデータを置くフォルダーを書く"

positions
  staff     "社員"
  manager   "課長"
  head      "部長"
  president "社長"

team kinds
  company    "会社"
  department "部"
  section    "課"

data "../org"
```

| 書く場所 | 内容 |
|---|---|
| `positions` | 役職。低いほうから並べる。`nearest head above requester` と書くと、部長かそれより上の役職の長を探す |
| `team kinds` | チームの種類。広いほうから並べる。`requester.department` は、申請者のチームから上へたどって最初に見つかる、種類が `department` のチーム |
| `data` | 組織のデータのディレクトリ |

本部がある会社なら、`team kinds` に `division "本部"` を足します。すると、申請の種類に `head of requester.division` と書けるようになります。

## 見本の会社を自分の会社に替える

`ritsu ringi init` が作るプロジェクトには、見本の会社のデータが入っています。自分の会社に替えるときは、次の順に書き換えます。まだだれも使っていないプロジェクトなら、申請の種類の版は上げなくてかまいません。

1. `project/shared/company.ringi` の役職とチームの種類を、自分の会社のものにします。
2. `project/org/2026-04-01/` の三つの CSV を、自分の会社のものにします。ディレクトリの名前は、その組織が始まった日にします。シナリオの日付より前の日にしてください。
3. `.ringi` のファイルの `owner` を、その申請を受け持つチームの ID にします。`owner` に書いたチームがあるかどうかは、検査では確かめられません。
4. `.ringi` のシナリオと、試験（`pages_test.go`、`server_test.go`）に出てくる人の ID とメールアドレスを、自分の会社の人のものにします。
5. 検査し、コードを作り直し、試験します。

    ```sh
    ritsu check project
    go generate ./...
    go test ./...
    ```

替え忘れた人がいれば、検査で分かります。次は、シナリオに出てくる `s1202` がデータにいないときのエラーです。

```
…
エラー[ringi E603]: project/purchase.ringi:63:22: チケットは、この行の `submit` を受け付けません: s1202 は、2026-04-01 からの組織にいません
    63 | | 2026-11-16 10:00 | submit  | s1202 |                        | 上長     | in progress |
  = チケットはまだありません。
…
```

## 異動を反映する

例として、12 月 1 日から、営業一課長が s1101 から s1202 に替わることにします。s1101 は、営業部の所属になります。

1. いまの版をコピーして、新しい版のディレクトリを作ります。名前は、始まる日です。

    ```sh
    cp -R project/org/2026-04-01 project/org/2026-12-01
    ```

2. 新しい版の `teams.csv` で営業一課の長を、`members.csv` で二人の所属と役職を直します。

    ```csv title="project/org/2026-12-01/teams.csv" hl_lines="3"
    company,Example Co.,サンプル株式会社,company,,p0001
    sales,Sales Department,営業部,department,company,s1001
    sales_1,Sales Section 1,営業一課,section,sales,s1202
    ```

    ```csv title="project/org/2026-12-01/members.csv" hl_lines="2 4"
    s1001,sales,head,yes
    s1101,sales,staff,yes
    s1201,sales_1,staff,yes
    s1202,sales_1,manager,yes
    ```

3. 検査します。

    ```sh
    ritsu check project
    ```

    誤りがあれば、どのファイルの何行目かと、直し方が出ます。たとえば、`members.csv` の `s1202` の行を書き忘れると、次のエラーが出ます。

    ```
    …
    エラー[ringi E501]: project/org/2026-12-01/people.csv:6:1: 人 `s1202` に、主の所属がありません
         6 | s1202,Sales staff 2,営業担当 2,s1202@example.com
      = `project/org/2026-12-01/members.csv` の `s1202` の行の一つの `primary` を `yes` にしてください（人は、主の所属のチームから上長と部長をたどります）。
    …
    ```

4. 承認者が変わる人を読みます。

    ```sh
    ritsu ringi diff --versions 2026-04-01 2026-12-01 project/purchase.ringi
    ```

    ```
      申請の種類 `購買`（`purchase.ringi`）を、2026-04-01 に効いている組織（2026-04-01 からの版）と、2026-12-01 に効いている組織（2026-12-01 からの版）で読み比べます。
    承認者：
      二つの版にいる 9 人の申請者のうち、2 人の承認者が変わります。
        s1201（営業担当 1）、上長：上長：s1101（営業一課長） → 上長：s1202（営業担当 2）
        s1202（営業担当 2）、上長：上長：s1101（営業一課長） → 上長：s1001（営業部長）
    …
    ```

5. 動いているサーバーに渡します。渡し方は、次の節にあります。

進行中の申請では、すでに始まったステップの承認者は変わりません。そのあとに始まるステップから、新しい版で承認者が決まります。

## 動いているサーバーに渡す

渡し方は二つあります。どちらも、組織のデータ（すべての版と代理、[一覧](lists.ja.md)）をまとめて入れ替えます。前のデータに戻すときは、前のデータをもう一度渡します。

| 渡し方 | 向いているとき |
|---|---|
| 管理のページから取り込む | 人事や総務の担当者が反映する。サーバーを止めない |
| サーバーを作り直す | 開発者が、申請の種類の変更などと一緒に反映する |

### 管理のページから取り込む

zip にまとめ、[管理のページ](use/admin.ja.md#異動のデータを取り込む)で取り込みます。

```sh
(cd project && rm -f ../org.zip && zip -qr ../org.zip org -x '*/.*')
```

同じデータを `project/org` にも入れたままにして、Git に残しておきます。次にサーバーを作り直したとき、組み込まれたデータが変わっていれば、それが取り込まれるからです。`project/org` に入れ忘れると、管理のページから取り込んだ版が、そのときに消えます。

### サーバーを作り直す

組織のデータはサーバーに組み込まれ、起動のときに取り込まれます。次のコマンドを実行してから、サーバーを作り直して入れ替えます。

```sh
go generate ./...
go test ./...
```

`go generate` を忘れると、試験用のデータが古いままになり、`go test` が失敗します。

## 代理を決める

休みのあいだ、だれの代わりをだれが務めるかを、`project/org/proxies.csv` に書きます。

```csv title="project/org/proxies.csv"
person,proxy,kind,from,to,requests
s1001,s1101,approve,2026-12-21,2026-12-25,purchase
```

| 列 | 書くもの |
|---|---|
| `person`、`proxy` | 本人の ID と、代理をする人の ID |
| `kind` | `approve` なら承認の代理、`submit` なら申請の代理 |
| `from`、`to` | 最初の日と最後の日。`to` が空なら、終わりなし |
| `requests` | 代理をする申請の種類。空白で区切って並べる。空なら、すべて |

代理は、異動と同じ方法でサーバーに渡します。社員が自分で代理を決めるページはありません。代理は操作した日に決まるので、すでに待っている申請も、その期間は代理の人が承認できます。

## 異動した人、退職した人が待たせている申請

始まったステップの承認者は、組織のデータを変えても変わりません。

| だれのところで待っているか | すること |
|---|---|
| 異動した人 | 何もしなくてよい。本人がそのまま答えられる |
| 退職した人、答えられない人 | 管理する人が、[承認者を加えるか、引き継ぐ](use/admin.ja.md#答えられない承認者を替える) |

## 人事システムから作る

人事システムがあれば、そこから三つの CSV のファイルを書き出すのが簡単です。書き出したあとの検査と取り込みは、上と同じです。列の一覧は、プロジェクトの `requests/organization/columns.md` にもあります。

サーバーに人事システムを直接読ませることもできます。`server.OrgSource` を実装して、`Config` の `Organization` に渡します（[部品とパッケージ](parts.ja.md)）。

## 社員のデータを Git に入れないとき

`project/org` には見本の会社を残し、本物のデータは、サーバーを動かすコンピューターのディレクトリから読ませます。シナリオと試験は、見本の会社で動きます。`shinsei.go` を次のように書き換えます。

```go title="shinsei.go" hl_lines="5"
import (
	"embed"
	"io/fs"
	"log/slog"
	"os"

	server "github.com/i2y/ringi-server"
```

```go title="shinsei.go" hl_lines="2 3 4"
func Organization() server.OrgSource {
	if dir := os.Getenv("ORG_DIR"); dir != "" {
		return orgfiles.New(os.DirFS(dir))
	}
	dir, err := fs.Sub(project, "project")
```

`ORG_DIR` のディレクトリには、`org/` を置きます。一覧を使っていれば、`masters/` も置きます。サーバーは、起動のときと、管理のページの「起動のときに読むデータを確かめる」で、このディレクトリを読みます。CSV の誤りは、そのときに確かめられます。

本物のデータで承認者のいない人がいないかは、`ritsu check` で確かめます。手元でプロジェクトをコピーし、`project/org` を本物のデータに替えて実行してください。シナリオの人がいないというエラーは、見なくてかまいません。
