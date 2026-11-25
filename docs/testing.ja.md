# 試験する

申請の決まり、サーバー、自分で書いた部品を、社員に使ってもらう前に確かめる方法です。英語版は [testing.md](testing.md) です。

| 確かめること | 方法 |
|---|---|
| 申請の決まりが、思ったとおりに書けているか | `.ringi` の例（`scenario`）を書き、`ritsu check` を実行する |
| 期限や催促がいつになるか | `ritsu ringi run` で、例を一行ずつ実行した結果を見る |
| だれに回るか | `ritsu ringi simulate`（[だれに回るかを、動かさずに見る](change.ja.md#だれに回るかを動かさずに見る)） |
| サーバーが決まりのとおりに動くか | `go test ./...`。最初から試験が入っている |
| 通知や予算などの、自分で書いた部品 | Go の試験を書く |
| ページの使い勝手 | `-dev-login` で起動して、人を切り替えながら使う |

## 申請の決まりを、例で確かめる

`.ringi` の `scenario` は、だれがいつ何をすると、申請がどうなるかを書いた表です。プロジェクトの購買の申請には、最初から次の例があります。

```ringi title="project/purchase.ringi"
scenario だれも答えずに期限が切れる
  requester s1202
  form 金額 = 30_000JPY, 目的 = "課の本"
| at               | event   | by    | -> stage | status      |
| 2026-11-16 10:00 | submit  | s1202 | 上長     | in progress |
| 2026-11-18 09:00 | remind  |       | 上長     | in progress |
| 2026-11-20 00:00 | late    |       |          | expired     |
```

`remind`（催促）と `late`（期限切れ）は、時刻が来ると起きることです。`ritsu check` は、表の時刻にほんとうに催促や期限切れが起きるかも確かめます。休日や営業日の数え方を間違えていれば、ここで分かります。

`ritsu ringi run` は、例を実行して、そのときの承認者と期限を表示します。

```sh
ritsu ringi run project/purchase.ringi
```

```
…
2026-11-16 10:00  submit  s1202  -> 上長（s1101）、期限は 2026-11-19 の終わり
2026-11-18 09:00  remind         -> 上長（s1101）、期限は 2026-11-19 の終わり; s1101 に催促した
2026-11-20 00:00  late           -> expired; 期限切れ（late）
シナリオ `だれも答えずに期限が切れる`: 書いたとおり（3 行）
```

決まりを変えるときは、例も一緒に変えるか、足します。変わってはいけない例が通らなくなったら、変え方が間違っています。

## プロジェクトの試験

```sh
go test ./...
```

| 試験 | 確かめること |
|---|---|
| `TestVectors` | ritsu が例と決まりから作った操作（ベクタ、`requests/vectors/`）を、サーバーの API に送り、結果が合うか |
| `TestGate` | `gate.go` が、役割と部署から、してよいことを正しく答えるか |
| `TestPages` | ページで申請し、承認できるか |

申請の種類を足すと、その例から作られたベクタも `TestVectors` が使います。試験を書き足す必要はありません。`go generate ./...` を忘れて古いコードのままだと、`TestVectors` が失敗します。

## 自分の部品の試験

時刻を進めたり、送られた通知を調べたりするには、パッケージ `servertest` を使います。次の試験は、申請してから二営業日後に時計を進め、催促の通知が営業一課長に届くことを確かめます。`do`（API を呼ぶ関数）と `monday` は、最初からある `server_test.go` のものです。

```go title="remind_test.go"
package shinsei

import (
	"log/slog"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// TestRemind は、2 営業日たっても答えのない承認者に、催促が届くことを確かめる。
func TestRemind(t *testing.T) {
	cfg, err := Parts(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	clock := servertest.NewClock(monday) // 2026-11-16（月）10:00
	sent := &servertest.Notifier{}       // 渡された通知を覚えておく
	cfg.Store, cfg.Authenticator = memstore.New(), servertest.Header{}
	cfg.Clock, cfg.Notifier = clock, sent
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.ImportOrganization(t.Context()); err != nil {
		t.Fatal(err)
	}

	do(t, srv, "s1201", "submit_purchase", "", `{"form": {"amount": 120000, "purpose": "ノートパソコン"}}`)
	clock.Set(time.Date(2026, 11, 18, 9, 0, 0, 0, monday.Location())) // 水曜の 9:00
	if _, err := srv.RunTimers(t.Context()); err != nil {             // 来た期限と催促を処理する
		t.Fatal(err)
	}
	if _, err := srv.RunToDo(t.Context()); err != nil { // 通知を送る
		t.Fatal(err)
	}

	notices := sent.Notices()
	last := notices[len(notices)-1]
	if last.Notice != "reminder" || last.To[0].ID != "s1101" {
		t.Errorf("催促が届いていない：%s、%s", last.Notice, last.To[0].ID)
	}
}
```

| `servertest` にあるもの | 使い道 |
|---|---|
| `Clock` | 時刻を好きなときに進める |
| `Notifier`、`Flows`、`Agents`、`Books`、`AuditLog` | サーバーから渡されたものを覚えておく。わざと失敗させることもできる |
| `Browser`、`Form` | ページを開き、フォームに入力してボタンを押す |

## ページを自分で使って確かめる

`-dev-login` のサーバーでは、人を切り替えながら、申請から承認までを一通り試せます。管理のページを試すには、ログインのページの「役割」から `system_admin` などを選び、それから人を選びます。

サーバーの時刻は進められません。期限と催促は、`ritsu ringi run` と、`servertest` を使う試験で確かめます。

## CI で確かめる

次の四つを、変更のたびに実行します。三つ目は、`generate.go` の `ritsu gen` の行に `--check` を付けたものです。何も書かず、`go/` と `requests/` が `project/` から作ったものと違えば失敗します。

```sh
ritsu check project
go vet ./...
ritsu gen project --check --root project --target go --target requests --catalog https://example.com/acme/shinsei/a2ui/catalog.json --module example.com/acme/shinsei/go --out . --lang ja
go test ./...
```

## 自分の Store を書いたとき

申請を置く場所を自分で作ったら、パッケージ `conformance` の試験で、約束のとおりに動くかを確かめます。使い方は、[フレームワークを直す、自分のサーバーを試験する](../CONTRIBUTING.ja.md#自分のサーバーを確かめる)にあります。
