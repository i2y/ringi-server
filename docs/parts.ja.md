# 部品とパッケージ

サーバーに渡せる部品の一覧と、フレームワークのパッケージの一覧です。英語版は [parts.md](parts.md) です。

## プロジェクトが渡す部品

部品は、プロジェクトの `Parts`（`shinsei.go`）が返す `server.Config` に入れます。必ず渡すのは、次の表のはじめの三つです。残りは、使うときだけ渡します。

| 部品 | `server.Config` の項目 | 渡さないとき | 手引き |
|---|---|---|---|
| ritsu が作ったコード | `Packages` | 起動しない | [申請の種類を足す、変える](types.ja.md) |
| だれが何をしてよいか | `Gate` | 起動しない | [だれが何をしてよいかを決める](permissions.ja.md) |
| 組織のデータを読む | `Organization` | 組織がないので、どの申請も受け付けない | [組織のデータを用意する](organization.ja.md) |
| 通知を送る | `Notifier` | ログに書くだけ | [通知をメールで送る](mail.ja.md) |
| 承認のあとの処理を始める | `Flows` | 始める処理のない申請の種類なら、何も起きない。あれば、渡すまで何度も試す | [承認のあとの処理を始める](flows.ja.md) |
| エージェントのステップと要約 | `Agents` | 上の行と同じ | [承認の経路でエージェントに確かめさせる](agent-steps.ja.md) |
| 予算や休暇の帳簿 | `Books` | 帳簿を使う申請の種類の操作が失敗する | [予算や休暇の残りを確かめる](books.ja.md) |
| 操作の記録を、別の場所でも受け取る | `AuditLog` | 送らない。記録はデータベースに残る | 下の[操作の記録を別の場所にも送る](#操作の記録を別の場所にも送る) |

次の三つは、パッケージ `serve` がフラグから用意するので、`Config` には入れません。`serve` を使わずに[自分で組み立てる](embedding.ja.md)ときだけ渡します。

| 部品 | 項目 | `serve` が用意するもの |
|---|---|---|
| だれのリクエストか | `Authenticator` | `-dev-login`、`-oidc-…`、どちらもなければヘッダー `X-Person` |
| 申請を置く場所 | `Store` | `-db` の PostgreSQL か、メモリ |
| 添付ファイルを置く場所 | `Files` | `-db` の PostgreSQL か、`-files` のディレクトリか、メモリ |

## 同じものを二度渡されることがある

通知、処理の開始、エージェントへの依頼、帳簿の呼び出しには、キー（`EffectKey`）が付きます。`01M89JDENBGHWVJ8XXBRHKCY64/1/17` のように、申請の番号、何回目の提出か、通し番号を並べたものです。

サーバーは、部品がエラーを返したり、答える前にプロセスが止まったりすると、同じキーでもう一度渡します。成功したかどうか分からないときも同じです。そのため、部品の側で、同じキーのものを一度だけ行うように作ります。

| 部品 | 二度目を行わない方法の例 |
|---|---|
| `Notifier` | 送ったキーを表に残しておき、同じキーなら送らない。メールなら `Message-ID` をキーから作り、受け取る側で一通にする |
| `Flows` | キーを、処理の実行の ID にする（Temporal のモジュールはそうしている） |
| `Books` | キーを、帳簿の行の ID にする（帳簿の言語 chobo から作るコードはそうしている） |

部品の関数は、いくつものゴルーチンから同時に呼ばれます。失敗したときは、エラーを返します。

| エラーを返した部品 | サーバーがすること |
|---|---|
| `Notifier`、`Flows`、`Agents`、`AuditLog` | 間隔を延ばしながら、成功するまで渡し直す |
| `Books` | その操作を行わず、500 を返す |
| `Gate` | 500 を返す |
| `Authenticator` | `server.ErrNoPerson` なら 401、ほかのエラーなら 500 を返す |

## 操作の記録を別の場所にも送る

行われた操作と、受け付けられなかった操作の記録を、監査のシステムなどにも送れます。`server.AuditLog` を書いて、`Config` の `AuditLog` に入れます。

```go
type AuditLog interface {
	Record(ctx context.Context, e AuditEntry) error
}
```

| `AuditEntry` のフィールド | 内容 |
|---|---|
| `ID` | この記録の番号。同じ記録がもう一度渡されることがあり、そのときも同じ番号になる |
| `At`、`Person` | 日時と、操作した人の ID。期限などサーバーが行ったことには、人がない |
| `Operation`、`Ticket`、`Kind` | 操作の名前、申請の番号、申請の種類 |
| `Outcome`、`Refused` | `moved`（行われた）か `refused`（受け付けられなかった）。受け付けられなかったときは、その[理由](refusals.ja.md) |
| `Event` | 操作の内容（JSON） |
| `Via` | どこから来たか。`pages`、`screens`、`api`、`mcp` |
| `Key` | 操作の番号（`Idempotency-Key`） |

## パッケージとモジュール

本体は、一つの Go のモジュール `github.com/i2y/ringi-server` です。ほかのライブラリを使う `mcp` と `temporal` は、別のモジュールになっています。

| パッケージ、モジュール | 中身 |
|---|---|
| `server`（モジュールのいちばん上） | 申請を受け付けて進めるサーバー。HTTP の API と、同じ操作をする Go の関数 |
| `serve` | サーバーのコマンドを作る。フラグ、データベース、ログイン、HTTP のサーバー |
| [`web`](../web/README.ja.md) | ページ、画面、添付ファイルを HTTP で出す |
| [`screens`](../screens/README.ja.md)、[`screens/page`](../screens/page/README.ja.md) | A2UI の画面と、HTML のページを作る |
| [`oidc`](../oidc/README.ja.md) | 会社のログイン |
| `devlogin` | 試用のログイン（`-dev-login` と `X-Person`） |
| `postgres`、`memstore` | 申請と添付ファイルを、PostgreSQL かメモリに置く |
| `orgfiles` | 組織のデータの CSV を読む |
| `servertest`、`conformance` | 試験に使う部品と、サーバーが約束のとおりかを確かめる試験 |
| [`mcp`](../mcp/README.ja.md)（モジュール） | エージェントのツール |
| [`temporal`](../temporal/README.ja.md)（モジュール） | Temporal で処理を動かす |
| `examples`（公開しないモジュール） | 五つの申請の例 |

関数の一つ一つの説明は、[Go のリファレンス](https://pkg.go.dev/github.com/i2y/ringi-server)（英語）にあります。
