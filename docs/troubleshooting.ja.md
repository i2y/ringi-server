# 監視する、困ったとき

動いているサーバーの見方と、うまくいかないときの調べ方です。英語版は [troubleshooting.md](troubleshooting.md) です。

## 社員から聞かれたとき

| 言われること | 理由と、すること |
|---|---|
| ログインできない | メールアドレスが組織のデータにないことが多い。ログの `oidc: a sign-in refused` の行に理由がある（[ログインした人に見えること](sign-in.ja.md#ログインした人に見えること)） |
| ログインし直しになる | ログインした状態は、ログインから 12 時間か、使わないまま 2 時間で終わる。すぐに切れるなら、プロセスごとにクッキーの鍵が違わないかを見る |
| 申請の種類が出てこない | その人は申請できない（`submitted by`）、版がまだ始まっていないか止めてある、その人がその日の組織のデータにいない、のどれか |
| 違う人に回った | その日の組織のデータを見る。`ritsu ringi simulate <ファイル> --requester <ID> --on <日>` で、だれに回るかが分かる（`project/org/` のデータを使うので、取り込んだものと同じかを先に確かめる） |
| 承認のボタンが出ない | その人は、いまのステップの承認者でも代理でもない。申請のページの「経路」で、だれを待っているかを見る。自分の申請を承認できない決まりのこともある |
| 申請のページが「見つからない」になる | その人は、その申請を見られない（[申請を見られる人](concepts.ja.md#申請を見られる人)） |
| 通知が来ない | [通知が届かないとき](#通知が届かないとき) |
| 期限を過ぎたのに終わらない | サーバーが動いているか、`a timer was refused` の行がないかを見る |
| 「この申請を処理できませんでした」と出る | 進行中の申請があるのに、版を上げずに申請の種類を変えたときに起きる。ステップの名前を元に戻すか、[版を上げて](types.ja.md#使われている申請の決まりを変える)両方の版を渡す |
| 「サーバーのエラー」と出る | ログの `an operation failed` の行を見る |
| サーバーが `the store keeps tickets of versions no package has` と言って起動しない | 申請の残る古い版のコードを外した。言われた版のコードを戻す（[古い版のコードを外す](maintenance.ja.md#古い版のコードを外す)） |

ページに出るほかの文の意味は、[受け付けない理由](refusals.ja.md)にあります。

## 通知が届かないとき

ログを、次の順に見ます。

1. `msg=notice` の行がある。送る部品を渡していないので、ログに書いているだけです。通知を送る部品を渡します（[通知をメールで送る](mail.ja.md)）。
2. `msg="a thing to do failed; it is tried again"` の行に `what=notify` がある。送るのに失敗しています。`err` の理由を直すと、次のやり直しで届きます。アドレスが違うときは、組織のデータを直して取り込みます。やり直すときは、そのときの組織のデータのアドレスに送ります。
3. どちらもない。送り終えています。取り込んだ `people.csv` のアドレスと、迷惑メールの振り分けを見ます。

## 動いているかを確かめる

`GET /a2ui/functions.js` は、プロセスが動いていれば、ログインなしで 200 を返します。プロキシや監視のしくみからは、これを使います。データベースには問い合わせないので、データベースの監視は別に行います。

## ログを見る

ログは、標準エラー出力に出るテキストで、一つのことが一行です。次の行を監視します。

| 行 | 意味 |
|---|---|
| `msg="a thing to do failed; it is tried again"` | 応答のあとで行うことが失敗した。`what` が何か（次の表）、`tries` が何回目か、`err` が理由。2 秒後、4 秒後、8 秒後と間隔を延ばしながら（最長 10 分）、成功するまでやり直す |
| `msg="a timer failed; it is tried again when its lease ends"` | 期限や催促を処理できなかった。30 秒後にもう一度試す |
| `msg="a timer was refused; it is tried again later"` | 期限や催促が、申請の決まりに合わなかった。多くは、その日の組織のデータが原因 |
| `msg="work ended" … failed=true` | 承認のあとの処理が失敗した。`key` と `flow` がどの処理か。理由は申請のページに出る。Temporal で動かす処理は、原因を直してからもう一度動かせる（[失敗したとき](flows.ja.md#失敗したとき)） |
| `msg="an operation failed"` | だれかに 500 を返した。`operation`、`ticket`、`person` が何の操作か、`err` が理由 |
| `msg="oidc: a sign-in refused"` | 会社のログインは済んだが、組織のデータの一人に決められなかった。`reason` が理由（[ログインした人に見えること](sign-in.ja.md#ログインした人に見えること)） |
| `msg="oidc: a sign-in failed"` | サーバーとログインのサービスのあいだの失敗。`step` がどこか |
| `msg="apikeys: a key refused"` | プログラムが通らない鍵を送り、401 を返した。`reason` が理由。`no line of the file has it` は、途中で切れた鍵か、行を消した鍵。`the organization in effect today does not have its person` は、その日の組織のデータにいない人の鍵で、組織を離れた人などである（`person` と `line` がだれの鍵か）（[プログラムから呼ぶ](api.ja.md#プログラムから呼ぶ)） |
| `msg="reading the starts of the versions"` | データベースが応答しなかった |
| `msg="the server stopped"` | 起動しなかった、または止まった。`err` が理由 |

`what` の値と、それが表すことです。下の SQL の `what` も同じです。

| `what` | 行うこと |
|---|---|
| `notify` | 通知を送る |
| `launch` | 承認のあとの処理を始める。始めたあとの進み具合は、申請のページの「承認のあとの処理」に出る |
| `ask_agent`、`ask_note` | エージェントに確かめさせる、要約を書かせる |
| `release` | 押さえた予算などを戻す。帳簿を別のデータベースに置いているときだけ |
| `audit` | 操作の記録を、別の場所に送る |

## データベースを見る

まだ済んでいない通知や処理の件数と、失敗し続けているものは、次の SQL で分かります。

```sql
select what, count(*) filter (where done_at is null) as open, count(*) as made from ringi.todo group by what order by what;

select ticket, what, tries, last_error, try_at from ringi.todo where done_at is null and tries > 3 order by created;
```

過ぎているのに処理されていない期限と催促です。

```sql
select count(*) as late, min(at) as oldest from ringi.timers where at < now() - interval '5 minutes';
```

ある人の回答や再申請を待っている申請です。`s1101` を、その人の ID に替えます。

```sql
select w.ticket, t.kind, w.since from ringi.waiting w join ringi.tickets t on t.id = w.ticket
where w.person = 's1101' order by w.since;
```

表の大きさです。

```sql
select relname as "table", n_live_tup as rows, pg_size_pretty(pg_total_relation_size(relid)) as size
from pg_stat_user_tables where schemaname = 'ringi' order by pg_total_relation_size(relid) desc;
```

表は読むだけにします。変えるときは、サーバーを通して操作します。表の中身は互いに照らし合わせているので、直接書き換えると、サーバーが動かなくなることがあります。

## 起動しないとき

ログの最後の行に理由があります。よくあるものは次のとおりです。

| ログ | 理由と、すること |
|---|---|
| `signing in through the provider takes …, and these are missing: …` | 会社のログインの設定が足りない。挙がっているものを渡す |
| `… checks no one, so it listens only on a loopback address …` | 会社のログインの設定がないので、ほかのコンピューターから届くアドレスでは動かない。設定を渡すか、`-addr` を `127.0.0.1:…` にする |
| `the environment has RINGI_SESSION_KEYS, a setting of signing in through the provider, and … checks no one` | `-oidc-…` のフラグを渡すか、その環境変数を外す |
| `-session-keys: oidc: key 1 has 33 bytes, not 32` | 鍵を `head -c 32 /dev/urandom \| base64` で作り直す |
| `the -oidc-… settings: oidc: the provider's discovery document: …` | issuer の書き方がログインのサービスと違う。または、サーバーからログインのサービスに届かない |
| `-api-keys: line 2: the SHA-256 of a key is 64 hexadecimal digits, not …` | 行に、鍵そのものか、途中で切れた SHA-256 が書いてある。鍵の SHA-256 を書く（`printf %s "$KEY" \| shasum -a 256`） |
| `-api-keys: … lists no key: …` | ファイルにコメントしかない。鍵ごとに一行書くか、`-api-keys` を外す |
| `web: Look.Accent … reads at …:1 on the pages' …` | ページの見た目の色が、ページの上で読めない。もっと濃い色にする。`AccentDark` なら、もっと明るい色にする（[ページの見た目を変える](screens.ja.md#ページの見た目を変える)） |
| `-db: postgres: the database cannot be reached: …` | 接続文字列、パスワード、ネットワークを確かめる |
| `-db: postgres: the tables in the schema ringi: ERROR: permission denied for database …` | データベースのユーザーがスキーマを作れない。`grant create on database` をする |
| `-db: postgres: the tables in the schema ringi: they are at version 14, newer than this code's 13: …` | 新しい版のサーバーが表を新しくした。その版を動かすか、その前のバックアップを復元する |
| `the package keeps contract …, and this framework reads …: …` | ritsu と ringi-server の組み合わせが合わない。[版の表](../README.ja.md#版)の組にして、`go generate ./...` を実行する |
| `org/` のファイル名と行 | 組織のデータが検査を通らない。`ritsu check project` で、起動する前に同じことが分かる |
