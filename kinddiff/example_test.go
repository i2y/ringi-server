package kinddiff_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/i2y/ringi-server/kinddiff"
)

func ExampleCompare() {
	oldDir := os.DirFS(filepath.Join("testdata", "v1"))
	newDir := os.DirFS(filepath.Join("testdata", "v2"))
	d, err := kinddiff.Compare(oldDir, newDir, "leave")
	if err != nil {
		panic(err)
	}
	for _, line := range d.Lines("en") {
		fmt.Printf("%s api=%v: %s\n", line.Section, line.API, line.Text)
	}
	for _, line := range d.Lines("ja") {
		fmt.Printf("%s api=%v: %s\n", line.Section, line.API, line.Text)
	}
	// Output:
	// form api=false: The range of the field "Business days" (days) changes from 1 through 20 to 1 through 25.
	// usable api=false: This version now takes effect from 2027-04-01.
	// usable api=false: The first day the kind can be used changes from 2026-04-01 to 2027-04-01.
	// usable api=false: The data deciding the first day the kind can be used changes from "the first version of the organization `shared/company.ringi` (data `../org`)" to "`effective from`".
	// api api=true: In the request body of `submit_leave`, `maximum` of `form.days` changes from `20` to `25`.
	// api api=true: In the request body of `resubmit_leave`, `maximum` of `form.days` changes from `20` to `25`.
	// other api=false: The version number changes from 1 to 2.
	// form api=false: 項目「営業日の日数」（days）の範囲が1以上20以下から1以上25以下に変わります。
	// usable api=false: この版は2027-04-01から効きます。
	// usable api=false: この申請の種類を出せる最初の日が2026-04-01から2027-04-01に変わります。
	// usable api=false: 申請を出せる最初の日を決めるデータが「the first version of the organization `shared/company.ringi` (data `../org`)」から「`effective from`」に変わります。
	// api api=true: `submit_leave`に送る本文では、`form.days`の`maximum`が`20`から`25`に変わります。
	// api api=true: `resubmit_leave`に送る本文では、`form.days`の`maximum`が`20`から`25`に変わります。
	// other api=false: 版の番号が1から2に変わります。
}
