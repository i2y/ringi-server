# だれが何をしてよいかを決める

申請を見る、承認する、管理する、といった操作のたびに、サーバーは「この人は、これをしてよいか」を権限の決まりに尋ねます。この決まりを **gate** と呼び、`project/shared/company.gate` に書きます。ritsu の言語の一つ、sekisho で書いたファイルです。英語版は [permissions.md](permissions.md) です。

承認者がだれかは、gate には書きません。経路（`.ringi`）と組織のデータから決まります（[承認者はどう決まるか](concepts.ja.md#承認者はどう決まるか)）。

## 最初から書いてある決まり

`ritsu ringi init` が書く gate の決まりは、次のとおりです。

| すること | してよい人 |
|---|---|
| 申請する | 社員のだれでも。`.ringi` の `submitted by` でさらに絞る |
| 承認する、却下する、差し戻す、保留にする | 経路が決めたそのステップの承認者と、その代理 |
| 自分の申請を承認、却下、差し戻しする | できない。`.ringi` の `self approval` で認めた申請だけはできる |
| 取り下げる、申請をやめる | 申請者の側 |
| コメントする | 申請者の側、いまのステップの承認者（申請者に戻っているあいだは、差し戻した人）、受け持つ部の人 |
| 申請を見る | 申請者の側、承認者、回覧を受け取った人、受け持つ部の人 |
| 承認者を加える、引き継ぐ | 受け持つ部の `workflow_admin` |
| 申請の種類の版を切り替える | 受け持つ部の `workflow_admin`、`system_admin` |
| 申請の記録を読む | 受け持つ部の `workflow_admin`、`system_admin`、`auditor` |
| 組織のデータを取り込む | `system_admin` |
| 組織のデータの記録を読む | `system_admin`、`auditor` |

「申請者の側」は、申請者と、その日に申請の代理をする人です。代理が出した申請なら、出した人も入ります。

「受け持つ部の人」は、`.ringi` の `owner` に書いたチームの人です。その下のチーム（部が受け持つなら、その課）の人も入ります。

`workflow_admin` などの役割は、[ログイン](sign-in.ja.md#4-役割を渡す)から来ます。管理の役割を持つ人も、社員（`staff`）としてできることは変わりません。

## gate の読み方

`company.gate` から、決まりを一つ抜き出します。

```gate title="project/shared/company.gate"
permit the_owning_department_sees
  description "申請の種類を受け持つ部は、その種類のチケットを見られる"
  principal is Staff
  action see
  when resource.owner is principal.department
```

| 言葉 | 意味 |
|---|---|
| `principal` | 操作する人。`principal is Staff` はログインした人のだれでも、`principal in auditor` は役割 `auditor` を持つ人 |
| `action` | 操作の名前。`see` は申請を見ること（[一覧](#サーバーが尋ねるとき)） |
| `resource` | 操作の相手。申請や、申請の種類など。`resource.owner` は、受け持つチーム |
| `when`、`unless` | 決まりが当てはまる条件と、当てはまらない条件 |
| `permit` | 許す決まり。どれにも当てはまらない操作は、許されない |
| `forbid` | 許さない決まり。`permit` に当てはまっても許されない |
| `expect deny`、`expect allow` | 許してはいけない組み合わせと、許すはずの組み合わせ。`ritsu check` が、決まりがそのとおりになっているかを確かめる |

ritsu は、このファイルを検査して Go のコード（`go/authz/company`）を作ります。`gate.go` は、サーバーから尋ねられるたびに、人と申請の値をそのコードに渡して答えを返します。

## 決まりを変える

例として、`auditor` の人が、どの申請も見られるようにします。`company.gate` の最後の `permit` のあとに、決まりを一つ足します。

```gate title="project/shared/company.gate" hl_lines="5 6 7 8 9"
permit auditors_read_every_record
  description "監査する人は、どの申請の種類の記録も組織の記録も読む"
  principal in auditor
  action read_audit, read_organization_audit

permit auditors_see_every_ticket
  description "監査する人は、どのチケットも見られる"
  principal in auditor
  action see
```

検査すると、エラーになります。

```sh
ritsu check project
```

```
…
エラー[sekisho E304]: project/shared/company.gate:265:1: 期待 `other_departments_do_not_see` が成り立ちません。選んだ 15 通りのうち 8 通りが許されます
   265 | expect deny other_departments_do_not_see
  = 例：auditor を持つ Staff、Ticket、resource.owner は principal.department と違う。`auditors_see_every_ticket` が許します。
  = ポリシーを直すか、期待が言いすぎているなら期待を直してください。
…
```

同じファイルに、「ほかの部の人は見られない」という期待（`expect deny`）が書いてあるためです。今度の変更は期待のほうを変えるものなので、期待を直します。

```gate title="project/shared/company.gate" hl_lines="2 5"
expect deny other_departments_do_not_see
  description "ほかの部の人は、監査する人でなければ、自分の載っていないチケットを見ない"
  principal in staff
  action see
  unless principal in auditor
  unless resource.owner is principal.department
```

検査が通ったら、生成して試験します。

```sh
ritsu check project
go generate ./...
go test ./...
```

決まりと期待を別々に書いておくと、決まりを足したときに、意図しない人まで許してしまう誤りに気づけます。決まりの書き方は [sekisho のページ](https://i2y.github.io/ritsu/ja/sekisho/)にあります。

## 役割や属性を足す

役割を足すときは、`company.gate` に `role` を書き、`principal Staff` の `roles` に並べ、それを使う決まりを書きます。Go のコードは変えません。

人や申請について新しい値（勤務地など）を決まりで使うときは、`company.gate` の `attributes` に足し、`gate.go` の `gateStore` でその値を返します。

## サーバーが尋ねるとき

| 操作 | 尋ねる `action` |
|---|---|
| 申請する、再申請する | `submit` |
| 取り下げる、申請をやめる | `withdraw` |
| 承認する、変更を保存する | `approve` |
| 却下する | `reject` |
| 差し戻す | `send_back` |
| 保留にする、保留を解く | `pause` |
| 承認を取り消す | `retract` |
| 承認者を加える | `add_approver` |
| 引き継ぐ | `hand_over` |
| コメントする | `comment` |
| 申請者でも承認者でもない申請を見る | `see` |
| だれが出した物でも、申請の種類のすべての申請を並べる | `see`。だれの名前も挙げない申請について（下） |
| 申請の種類の版を切り替える | `change_request_type` |
| 申請の記録を読む | `read_audit` |
| 組織のデータを取り込む | `import_organization` |
| 組織のデータの記録を読む | `read_organization_audit` |

画面のボタンも、これと同じことを尋ねてから出します。押せないボタンは出ません。

申請の種類のすべての申請を並べるとき（`GET /tickets?kind=` と、ホームの「すべての申請」）、サーバーは、だれの名前も挙げない申請について尋ねるように、その種類について `see` を尋ねます。`resource.owner` はその種類を受け持つチームで、申請者、申請者の側、承認者はありません。`the_owning_department_sees` のように持ち主だけを読む規則は、これを許します。申請者や承認者を読む規則は許しません。そのあと、一覧の申請を一件ずつ、いつもどおりに尋ねます。一部の申請を禁じる `forbid` があれば、その申請は一覧に出ません。

決まりが許しても、申請がその操作を受け付けないことがあります。終わった申請の承認などです。そのときは、[受け付けない理由](refusals.ja.md)が返ります。
