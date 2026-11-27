# フラグと設定

サーバーのフラグ、Go で渡す設定、上限、URL の一覧です。英語版は [reference.md](reference.md) です。

## フラグ

`ritsu ringi init` が作るサーバーをはじめ、パッケージ `serve` を使うサーバーのフラグです。`go run ./cmd/server -h` でも表示できます。

| フラグ | 内容 |
|---|---|
| `-addr` | 待ち受けるアドレス。指定しなければ `127.0.0.1:8080` |
| `-db` | PostgreSQL の接続文字列。申請と添付ファイルを、スキーマ `ringi` に置く。指定しなければメモリに置き、止めると消える |
| `-files` | `-db` を使わないときに、添付ファイルを置くディレクトリ。指定しなければメモリに置く |
| `-dev-login` | 試用のログインを使う。だれとして使うかをページで選ぶ。本人を確かめないので、自分のコンピューターのアドレスでしか動かない |
| `-oidc-issuer` | 会社のログインの issuer の URL |
| `-oidc-client-id` | クライアント ID |
| `-oidc-client-secret` | クライアントシークレット。ほかの利用者に見えないよう、環境変数 `RINGI_OIDC_CLIENT_SECRET` で渡すほうがよい |
| `-oidc-redirect-url` | ログインのサービスから戻ってくる URL。`https://<ホスト>/login/callback` |
| `-oidc-roles-claim` | 人の役割が入る、ID トークンのクレーム。指定しなければ、だれも役割を持たない |
| `-oidc-scopes` | `openid`、`email`、`profile` のほかに求めるスコープ。空白で区切る |
| `-session-keys` | ログインのクッキーの鍵。32 バイトの乱数の base64 を、新しい順にコンマで区切る。環境変数 `RINGI_SESSION_KEYS` で渡すほうがよい |
| `-tls-cert`、`-tls-key` | https の証明書とその鍵 |
| `-api-keys` | プログラムが API を呼ぶときの鍵のファイル。鍵ごとに一行で、鍵の人の ID、鍵の SHA-256、役割があればコンマで区切った役割を書く（[プログラムから呼ぶ](api.ja.md#プログラムから呼ぶ)）。起動のときに読む |
| `-bundles` | 起動のときに読むバンドルのディレクトリ。`<名前>.zip` のどれもと、署名した物だけを受け取るときは署名の `<名前>.zip.sig` を置く。どれもアップロードと同じに確かめてから受け持つ（[動いているサーバーに版を足す](bundles.ja.md#4-起動のときにバンドルを読む)） |
| `-bundle-keys` | バンドルの署名の公開鍵（PEM）のファイル。これを渡すと、そのどれかで署名したバンドルだけを受け取る（[バンドルに署名する](signing.ja.md)） |

`--temporal` で作ったサーバーには、Temporal のアドレスを渡す `-temporal`（指定しなければ `localhost:7233`）もあります。

PostgreSQL のパスワードは、環境変数 `PGPASSWORD` か `.pgpass` ファイルで渡せます。

## server.Config

プロジェクトの `Parts` が返す設定です。渡す部品は[部品とパッケージ](parts.ja.md)にあります。ほかに次の値を変えられます。`0` か空のままなら、表の値になります。

| 項目 | 意味 | 変えないとき |
|---|---|---|
| `MaxBody` | リクエストの本文の上限 | 1 MiB |
| `CommentEvery` | 同じ人が、同じ申請に続けてコメントできる間隔 | 10 秒 |
| `CommentsPerTicket` | 一件の申請に付けられるコメントの数 | 100 |
| `CommentsPerPerson` | そのうち、一人が付けられる数 | 25 |
| `UploadsPerPerson` | 一人がアップロードした、どの申請も持たない添付ファイルの数 | 50 |
| `UploadsKept` | どの申請も持たない添付ファイルを残す期間 | 7 日 |
| `OperationsKept` | 操作の番号と応答を残す期間 | 30 日 |
| `MaxOrganization` | 組織のデータのアップロードの上限 | 64 MiB |
| `MaxBundle` | バンドルの上限。アップロードしたままの大きさと、展開したファイルの大きさ | 64 MiB |
| `MaxCheckTime` | アップロードしたバンドルを確かめる時間の上限。確かめがそれより長くかかるバンドルは受け付けず、何も残さない | 20 秒 |
| `DraftsKept` | 確かめただけの組織のデータを残す期間 | 1 日 |
| `ImportsPerDay` | 24 時間に取り込める、組織の新しいデータの回数 | 20 |
| `SweepEvery` | 期間の過ぎたものを消す間隔 | 1 時間 |
| `BooksApart` | 帳簿が、申請と別のデータベースにある | `false` |
| `Logger` | サーバーのログ | `slog.Default()` |

## ページの見た目

サーバーのページの見た目です。`cmd/server/main.go` で、置き場の `Look` に渡します（`web.Look`、[ページの見た目を変える](screens.ja.md#ページの見た目を変える)）。空のままなら、フレームワークの見た目になります。

| 項目 | 意味 | 受け付けるもの |
|---|---|---|
| `Name` | ページの上に出す名前。ホームへのリンクになる。無ければ、ロゴも無いときに帯が「ホーム」と書く | 60 文字まで。改行を含まず、両端に空白が無い |
| `Logo` | 名前の前に出す画像。高さ 28 ピクセルで見せる。`/pages/assets/logo` で出す | SVG、PNG、JPEG、GIF、WebP のどれかで、256 KiB まで |
| `Accent` | リンク、ボタン、申請がいるステップの色。`#rrggbb` で書く | ページの背景の `#ffffff` と `#f1f4f9` のどちらとも 4.5:1 以上 |
| `AccentDark` | 同じ色の、暗い配色にしたブラウザのための物。無ければ、サーバーが `Accent` から作る | 暗いページの背景の `#151b26` と `#1d2534` のどちらとも 4.5:1 以上 |

これを満たさない見た目では、サーバーは起動せず、理由を言います。

## そのほかの上限

| もの | 上限 | 変える場所 |
|---|---|---|
| 添付ファイル一つ | 10 MiB | `web.Config` の `MaxFile` |
| 添付ファイルの種類 | PDF、画像（PNG、JPEG、GIF、WebP）、テキストと CSV、Office と OpenDocument のファイル | `web.Config` の `FileTypes` |
| セッション | ログインから 12 時間、使わないまま 2 時間 | `oidc.Config` の `SessionLimit`、`IdleLimit` |
| コメントの長さ | 2,000 字 | 変えられない |
| リクエストのヘッダー | 5 秒以内に読み終える。大きさは 64 KiB まで | 変えられない |
| リクエストを読む時間、応答する時間、使っていない接続 | 30 秒、30 秒、2 分 | 変えられない |

`web.Config` と `oidc.Config` は、[自分で組み立てるサーバー](embedding.ja.md)で変えられます。パッケージ `serve` のサーバーでは、表の値です。

## URL

| URL | 内容 |
|---|---|
| `/` | ホーム（`/pages/`）に移る |
| `/pages/` | 申請、承認、管理のページ |
| `/pages/admin/` | 管理のページ。役割を持つ人だけ |
| `/login`、`/login/callback`、`/logout` | 会社のログイン |
| `/requests/…`<br>`/tickets`<br>`/tickets/{id}`<br>`/waiting`<br>`/mine`<br>`/find/…`<br>`/organization`<br>`/masters/…`<br>`/admin/…`<br>`/files` | API（[プログラムから API を呼ぶ](api.ja.md)） |
| `/a2ui/…` | A2UI の画面（[自分の画面を作る](screens.ja.md#a2ui-の画面を使う)） |
| `/pages/assets/page.css`、`/pages/assets/logo` | ページのスタイルシート（最後に見た目の色が付く）と、ロゴ（[ページの見た目](#ページの見た目)）。ログインなしで返す |
| `/a2ui/functions.js` | ログインなしで 200 を返す。動いているかの確認に使える |
| `/mcp` | エージェントのツール。足したサーバーだけ（[エージェントに申請と承認をさせる](agents.ja.md)） |

## プロジェクトのファイル

| ファイル | 内容 |
|---|---|
| `project/*.ringi` | 申請の種類 |
| `project/shared/*.ringi` | 組織の形（役職、チームの種類）、選択肢の一覧（マスタ） |
| `project/shared/*.gate` | 権限 |
| `project/shared/*.cal` | 営業日 |
| `project/shared/*.book`、`*.flow` | 帳簿、承認のあとの処理。使うときだけ |
| `project/org/<日付>/*.csv` | 組織のデータ。日付は有効になる日 |
| `go/` | ritsu が作った Go のコード |
| `requests/openapi.json` | API の記述 |
| `requests/types/<種類>.json` | 申請の種類の説明 |
| `requests/a2ui/`、`requests/mcp/` | 画面とエージェントのツールの定義 |
| `requests/vectors/` | 試験に使う操作と結果 |
