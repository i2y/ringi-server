# フォームの選択肢を一覧から読む

取引先や勘定科目のように、ときどき変わる選択肢は、申請の種類のファイルには書かず、一覧のファイルに置きます。この一覧をマスタと呼びます。例として、購買の申請に「取引先」を足します。[2. 申請の種類を書き換える](change.ja.md)まで済ませたプロジェクトで説明します。社員が使っている申請に足すときは、先に[版を上げます](types.ja.md#使われている申請の決まりを変える)。英語版は [lists.md](lists.md) です。

## 一覧を作る

一覧の形を `project/shared/masters.ringi` に、行を CSV のファイルに書きます。

```ringi title="project/shared/masters.ringi"
masters lists v1
description "フォームの選択肢の一覧"
use organization org from "company.ringi"

master vendors "取引先"
  key id
  fields
    id   : text "取引先の ID"  count <=12
    name : text "名前"  count <=100
  data "../masters/vendors.csv"
```

```csv title="project/masters/vendors.csv"
id,name,valid_from,valid_to
V-001,文具の店,2026-04-01,
V-002,家具の店,2026-04-01,2026-12-31
```

`valid_from` と `valid_to` は、その行を選べる最初の日と最後の日です。空なら、終わりなしです。取引をやめた相手の行は、消さずに `valid_to` を入れます。新しい申請では選べなくなり、前の申請では読めます。

## フォームで使う

申請の種類に、`use` の行、項目、英語の名前を書きます。

```ringi title="project/purchase.ringi" hl_lines="4 10 17"
use organization 組織(org) from "shared/company.ringi"
use calendar 本社(office) from "shared/office.cal"
use gate "shared/company.gate"
use masters マスタ(m) from "shared/masters.ringi"
…
form
  金額(amount)  : money[JPY] "金額"  range >=1JPY <=10_000_000JPY
  目的(purpose) : long text "目的"  count >=1 <=500
  見積書(quote) : attachment "見積書"  required when 金額 > 500_000JPY
  取引先(vendor) : choice from マスタ.vendors "取引先"  optional
…
labels en
  request     "Purchase request"
  金額        "Amount"
  目的        "Purpose"
  見積書      "Quotation"
  取引先      "Vendor"
```

`choice from マスタ.vendors` は、一覧の行から一つを選ぶ項目です。

## サーバーに組み込む

`shinsei.go` で、`project/masters` も組み込むようにします。

```go title="shinsei.go" hl_lines="1"
//go:embed project/org project/masters
var project embed.FS
```

検査して、コードを生成し、試験します。

```sh
ritsu check project
go generate ./...
go test ./...
```

## 一覧を新しくする

一覧は、[組織のデータ](organization.ja.md)の一部として扱われます。行を足すときは CSV のファイルを直し、組織のデータと同じ方法で、動いているサーバーに渡します。zip には、`org` と一緒に `masters` も入れます。

```sh
(cd project && zip -qr ../org.zip org masters -x '*/.*')
```

取り込みは、組織のデータと一覧のすべてを入れ替えます。`masters` を入れ忘れた zip を渡すと、取り込む前の確かめで誤りになります。
