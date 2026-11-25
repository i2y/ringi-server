# フレームワークを直す、自分のサーバーを試験する

フレームワークを直す人と、自分の部品（とくに Store）を書いて確かめたい人のためのページです。サーバーが守ることとその理由は、[設計](DESIGN.ja.md)にあります。英語版は [CONTRIBUTING.md](CONTRIBUTING.md) です。

## 自分のサーバーを確かめる

パッケージ `conformance` は、サーバーを、ritsu が生成した約束と突き合わせます。Store は何でもかまいません。関数ごとに、確かめることが決まっています。

| 関数 | 確かめること |
|---|---|
| `Vectors` | すべてのベクタを API に流す。実行されることと、どの応答も約束の形であること |
| `Worked` | ベクタにある書きかけのフォームから、フォームが計算するもの |
| `Trials` | 人の操作を、実行する前に試す。試しは何も残さず、実行したときと同じ応答になること |
| `Concurrent` | 一つの Store の上の二つのサーバーから、同じチケットに多くの操作を同時に送る |
| `WaitingCounts` | 人を待っているものの件数を、チケットと突き合わせる。代理があり、その期間の前、中、後の日で |
| `WaitingOrder` | 待っているものの順（新しい順、マイクロ秒まで）と、コメントで順が変わらないこと |
| `MineOrder` | 自分の申請の順と、状態ごとの件数 |
| `Reminded` | 催促を出したかどうかが、一時停止と再開のあとも残ること |
| `Administration` | 版の切り替え、組織のデータの確認と取り込み、記録。gate が許さない人には何も変えられず、何も読めないこと |
| `Keeping` | 時刻が UTC であること、古い操作を順に、決まった件数ずつ消すこと、一つのキーのアップロードを一度だけ記録すること |

最後の二つは、ベクタなしで動きます。二つの順は、ページに分けて読んだときも確かめます。

```go
func TestConformance(t *testing.T) {
	p, err := server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, os.DirFS("requests"))
	if err != nil {
		t.Fatal(err)
	}
	s := conformance.Setup{
		Packages:     []*server.Package{p},
		Store:        func(testing.TB) server.Store { return newEmptyStore() }, // yours
		Organization: orgfiles.New(os.DirFS("project")),
	}
	t.Log(conformance.Summary(conformance.Vectors(t, s)))
	conformance.Worked(t, s)
	conformance.Trials(t, s)
	conformance.Concurrent(t, s)
	conformance.WaitingCounts(t, s)
	conformance.WaitingOrder(t, s)
	conformance.MineOrder(t, s)
	conformance.Reminded(t, s)
	conformance.Administration(t, s)
	conformance.Keeping(t, s)
}
```

Store は、人を待っているものや自分の申請の件数を、チケットを読まずに数えられます（`server.WaitingCounter`、`server.MineCounter`。`memstore` と `postgres` にあります）。あれば、`WaitingCounts` と `MineOrder` がその件数も確かめます。なければ、サーバーがチケットを読んで数えます。

自分の試験では、`Package.CheckAnswer` で、API の応答を約束と突き合わせられます。

## 試験を動かす

```sh
go test ./...
cd examples
go test ./...
RINGI_TEST_DATABASE="postgres://localhost:5432/ringi_test" go test -race ./...
```

モジュール `mcp` と `temporal` には、それぞれの試験があります（それぞれで `go test ./...`）。Go のほかに要るものがある試験は、それがないと飛ばします。飛ばした試験は、`go test -v` の `--- SKIP` の行に出ます。

| 要るもの | 使う試験 |
|---|---|
| `RINGI_TEST_DATABASE`（試験が書き込んでよい PostgreSQL のデータベース） | PostgreSQL の上の Store、突き合わせ、帳簿 |
| `PATH` にある `node` | 画面と、ページのスクリプト（ブラウザと同じように実行する） |
| `PATH` にある `bash`、`curl`、`jq` | 文書のコマンド |
| ritsu（`PATH` か、環境変数 `RITSU`） | `ritsu ringi init` が書くプロジェクトと、README に載せた `ritsu ringi init` のコマンド（どちらも `scaffold`。数分）、例の flow（`temporal`） |
| Temporal の CLI（SDK のキャッシュか、環境変数 `TEMPORAL_CLI`） | Temporal の開発用サーバーの上の flow |

### 例での結果

突き合わせは、英語と日本語の五つの例で、メモリの Store と PostgreSQL の両方で行っています。

| 突き合わせ | 例での結果 |
|---|---|
| ベクタ | 2,990 のすべてが一致。2,166 は HTTP のハンドラーを通し、残り（タイマー、エージェントの答え、人のいない操作）は `Deliver` を通す |
| 試し | 人の操作 2,176 のすべてが一致 |
| 画面 | ベクタの 2,048 行を、HTTP で読んだ画面で行う。サーバーに届く 1,804 回は一致。残りの 332 回は、画面が先に止めるか、隠しているフィールドを送らない。ベクタも拒否する |
| ページ | ベクタの 2,054 行を、HTTP で読んだページで 2,142 回送って行う。サーバーに届く 1,752 回は、1,664 回が受け付けられ、88 回が拒否され、ベクタと一致。残りの 390 回はページが先に止める。ボタンがない 142、ページの検査 146、表示のあとにチケットが変わった 70、その人が開けないページ 12、ページが選ばせない値 20 |
| ボタン | ボタンは、その人が押せるときにだけある。`TestCanIsWhatIsTaken` が、すべての操作について、ベクタのチケットがいる 350 か所のそれぞれで確かめる |

### ブラウザで

ビルドタグ `browser` の試験は、本物のブラウザでページを使います。`RINGI_BROWSER` に、`--headless` と `--remote-debugging-pipe` を取るプログラムを指定します。試験ごとに、専用のプロファイルで起動し、127.0.0.1 の試験のサーバーにだけつなぎ、試験が終わると止めます。

```sh
cd examples
RINGI_BROWSER=/path/to/a/browser go test -tags browser -run Browser .
```

管理のページも含めて、英語と日本語、明るい配色と暗い配色、幅 1,280 px と 320 px で開き、次を確かめます。

- どの文字もコントラストが 4.5:1（大きい文字は 3:1）、入力したり押したりする部分の縁が 3:1 以上であること。ページが画面の幅を越えないこと。Tab で、3:1 以上の枠が見える形で、すべての部分に届くこと。文字を 2 倍にしても同じこと。
- ボタンを二度押す、戻ったあとにもう一度押す。送信の応答が来ないうちにすぐ押す、3 秒後に押す。
- スクリプトなしでフォームを送ったときの、フォーカスの行き先。スクリプトなしで、フィールドで Enter を押す。変換の確定の Enter を押す。
- スクリプトが書く名前に、言語の印が付いていること。
- 版の切り替えの日付のフィールド、組織のファイルの zip を選んで取り込むこと、記録の CSV を保存すること。

文書の写真は、`RINGI_SHOTS` に置き場所を指定して、`TestShots` で撮ります。

```sh
cd examples
RINGI_SHOTS=../docs/images RINGI_BROWSER=/path/to/a/browser go test -tags browser -run TestShots .
```

### CI

`.github/workflows/ci.yml` は、ブランチへの push とプルリクエストのたびに、これらの試験を Linux で動かします。フレームワーク、例、`mcp` は Go 1.26 と 1.27、PostgreSQL 18 で、ページはランナーのブラウザで、flow は Temporal の CLI で試します。書式、`go vet`、`go.mod` が tidy であること、モジュールが macOS と Windows 向けにビルドできること、例が ritsu の書くとおりであることも確かめます。試験は `ci/test.sh` で動かすので、飛ばした試験も失敗になります。

ritsu が要る手順では、例を書いた版（例のファイルの一行目にあります）のリリースを使います。その版がまだリリースされていなければ、その手順は動かさず、警告を出します。

## 文書

どの文書にも英語版があり、隣に日本語版があります。`TestDocuments` は、README.md、docs/api.md、web/README.md、mcp/README.md と、それぞれの日本語版にあるコマンドを、書いてあるとおりに例のサーバーで実行し、出力を文書と突き合わせます。ほかの試験は、二つの言語のコマンドとコードが同じであること、Go のコードがコンパイルしているファイルのとおりであること、SQL が表に合うこと、リンクの先があることを確かめます。`scaffold` の `TestReadme` は、README に載せた `ritsu ringi init` のコマンドを実行し、表示を README と突き合わせます。

出力が変わるのが正しいときは、文書に書き直して、差分を読みます。

```sh
cd examples
RINGI_DOCS=write go test -run 'TestDocuments$' .
```

### サイト

同じ文書は、<https://i2y.github.io/ringi-server/ja/> でも読めます。一つの文書が 1 ページです。`website/pages.py` が、文書からページを書きます。GitHub とサイトで見え方が違う行や、日本語の文の途中の改行（空白になって見えます）があると、そこで止まります。`website/finish.py` は、できたサイトを確かめます。どちらの言語のメニューにもすべてのページが並んでいることと、すべてのリンクの先があることです。文書を足すときは、両方のメニュー（`website/zensical.toml` と `website/zensical.ja.toml`）に並べます。

サイトを自分のコンピューターで作って見るには、Python 3.11 以降で次を実行します。

```sh
python3 -m venv website/.venv
website/.venv/bin/pip install --require-hashes -r website/requirements.txt
website/build.sh
website/serve.sh
```

`.github/workflows/docs.yml` は、プルリクエストではサイトを作って確かめるところまでを行い、`main` からは GitHub Pages に公開します。

## 例を作り直す

`examples/project/` は、ritsu の例の写しです。違うのは、エージェントを持つ三つの flow のモデルの名前だけで、ここでは仮の名前（`example-model`）です。エージェントの答えは `examples/parts.go` と試験が返し、どちらのモデルでも、ritsu が書く `go/` と `requests/` は同じです。`examples/regen.sh` は、`PATH` にある `ritsu` で、写しから `examples/go/` と `examples/requests/` を書き直します。

## 版を公開する

フレームワークは三つのモジュールで、同じ版を三つのタグで公開します。`vX.Y.Z`、`mcp/vX.Y.Z`、`temporal/vX.Y.Z` です。タグを付ける前に、次のことをします。

1. `mcp/go.mod`、`temporal/go.mod`、`examples/go.mod` で、フレームワークの require をその版に上げる（`go mod edit -require=github.com/i2y/ringi-server@vX.Y.Z`）。`mcp` か `temporal` だけを要求するプロジェクトは、これらのファイルが要求する版のフレームワークを受け取るので、取得できる版でなければならない。`replace github.com/i2y/ringi-server => ../` は残す。go コマンドは main モジュールの replace だけを読み、`mcp` と `temporal` には、replace のあるモジュールを断る `go install` で入れるコマンドがない。
2. `mcp/` と `temporal/` にある `LICENSE-APACHE` と `LICENSE-MIT` の写しを残す（リンクでなくファイル）。go コマンドは、サブディレクトリのモジュールの zip に、ルートの `LICENSE` という名前のファイルしか写さない。`TestLicensesOfTheModules` が、ルートのものと同じことを確かめる。
3. タグを付けるコミットで CI が通っていることを見る。ritsu が要る手順も通っていること。その版の ritsu がまだリリースされていなければ、その手順を、自分のコンピューターで、その ritsu で行う。
4. プロジェクトが取得するのと同じ形で確かめる。タグを付けるコミットから作ったファイルのプロキシ（`GOPROXY=file://…`）で、ネットワークも replace もなしに、`mcp` だけを要求するモジュールと `temporal` だけを要求するモジュールを tidy、build、vet する。`ritsu ringi init` が `--mcp` と、`--mcp --temporal` で書くプロジェクトを、tidy、generate、vet、test する。どのモジュールの zip にも、二つのライセンスのファイルがあることを見る。
5. [README.ja.md](README.ja.md#版) と README.md の表に、その版と、組になる ritsu と約束の版を足す。
