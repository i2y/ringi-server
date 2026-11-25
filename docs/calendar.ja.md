# 営業日と祝日を決める

「3 営業日のうちに」のような期限は、会社の営業日で数えます。営業日は `project/shared/office.cal` に、ritsu の言語の一つ、koyomi で書きます。`ritsu ringi init` が書くファイルでは、祝日が休みになっていません。社員に使ってもらう前に、祝日と会社の休みを書き足します。英語版は [calendar.md](calendar.md) です。

## 最初から書いてあること

```cal title="project/shared/office.cal"
calendar office v1
description "会社の営業日。土曜、日曜、年末年始は休み。時刻は東京の時刻"
offset +09:00

closed weekly sat, sun
closed every 12-29..01-03 "年末年始"
```

| 書いたもの | 意味 |
|---|---|
| `offset +09:00` | 日付と時刻を数える時差。期限の「その日の終わり」や、催促の `09:00` は、この時差で決まる |
| `closed weekly sat, sun` | 毎週土曜と日曜は休み |
| `closed every 12-29..01-03 "年末年始"` | 毎年 12 月 29 日から 1 月 3 日までは休み |

## 会社の休みを足す

毎年の休みは `closed every`、一度だけの休みは `closed` と日付で書きます。

```cal title="project/shared/office.cal" hl_lines="3 4"
closed weekly sat, sun
closed every 12-29..01-03 "年末年始"
closed every 06-10 "創立記念日"
closed 2027-04-30 "臨時の休み"
```

## 祝日の一覧を読む

国民の祝日は、内閣府が CSV のファイルで公開しています。このファイルをプロジェクトにダウンロードして読みます。

1. `source` の三行と、`closed national_holidays` の行を足します。

    ```cal title="project/shared/office.cal" hl_lines="4 5 6 7 9"
    description "会社の営業日。土曜、日曜、年末年始は休み。時刻は東京の時刻"
    offset +09:00

    source national_holidays = file "data/syukujitsu.csv" url "https://www8.cao.go.jp/chosei/shukujitsu/syukujitsu.csv"
      format csv shift_jis
      covers listed years

    closed weekly sat, sun
    closed national_holidays
    closed every 12-29..01-03 "年末年始"
    ```

    | 書いたもの | 意味 |
    |---|---|
    | `file` | ダウンロードしたファイルを置く場所。`office.cal` のあるディレクトリからの相対で書く |
    | `url` | ダウンロードする元 |
    | `format csv shift_jis` | 一行が「日付,名前」の CSV で、文字コードは Shift_JIS |
    | `covers listed years` | 載っている年については、休みがすべて載っている。載っていない年の日は、営業日かどうかが分からない |

2. ダウンロードして、ハッシュを書き込みます。

    ```sh
    ritsu koyomi source fetch project/shared/office.cal
    ritsu koyomi source pin project/shared/office.cal
    ```

    `fetch` は、ファイルを `project/shared/data/` に保存します。`pin` は、`source` の行にファイルのハッシュ（`sha256:`）を書き足します。知らないうちにファイルの中身が変わっていれば、`ritsu check` がエラーにします。

3. 検査し、コードを作り直します。

    ```sh
    ritsu check project
    go generate ./...
    go test ./...
    ```

    ```
    …
      2027-12-21 まで使えます: カレンダー `本社`（`project/shared/office.cal`）のデータが 2027-12-31 までなので、その後に申請者へ戻ったチケットの再申請の期限を数えられません（`resubmit within 5 business days`）
    …
    ```

祝日の一覧が 2027 年までなら、購買の申請は 2027 年 12 月 21 日まで出せます。その日までに一覧を新しくしないと、申請できなくなります。

会社の休みを CSV のファイルで持っていれば、同じ書き方で読めます。`url` は書かなくてもかまいません。

## 一年に一度、祝日を足す

翌年の祝日が公開されたら、次のとおり新しくします。

1. もう一度ダウンロードして、ハッシュを書き換えます。

    ```sh
    ritsu koyomi source fetch project/shared/office.cal
    ritsu koyomi source pin project/shared/office.cal
    ```

2. 検査し、コードを作り直し、試験します。

    ```sh
    ritsu check project
    go generate ./...
    go test ./...
    ```

3. サーバーを作り直して入れ替えます（[申請の種類などを変えたサーバーに入れ替える](maintenance.ja.md#申請の種類などを変えたサーバーに入れ替える)）。

申請の種類の版は上げません。待っているステップの期限はそのままで、これから始まるステップの期限が、新しい営業日で数えられます。

[前の版のコード](types.ja.md#使われている申請の決まりを変える)を残していて、その版の申請が進行中なら、前の版のカレンダー（`v1/project/shared/office.cal` とデータ）も同じように新しくします。前の版の申請の期限は、そちらのカレンダーで数えるからです。
