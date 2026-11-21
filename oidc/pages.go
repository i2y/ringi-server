package oidc

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/internal/jatext"
)

// words are a text in English and in Japanese.
type words struct{ en, ja string }

func (w words) in(lang string) string {
	if lang == "ja" && w.ja != "" {
		return w.ja
	}
	return w.en
}

// with puts arg in the text's %s: in Japanese, as the framework's sentences in Japanese set a name
// (jatext.Join), by a space next to kana and kanji where it ends or starts in ASCII.
func (w words) with(arg string) words {
	before, after, _ := strings.Cut(w.ja, "%s")
	return words{fmt.Sprintf(w.en, arg), jatext.Join(before, arg, after)}
}

// The words of the module's pages.
var (
	wordsSignIn         = words{"Sign in", "ログインする"}
	wordsSignInAgain    = words{"Sign in again", "もう一度ログインする"}
	wordsSignInTitle    = words{"Sign in", "ログイン"}
	wordsSignOut        = words{"Sign out", "ログアウト"}
	wordsBadReturnTo    = words{"The page to come back to after signing in is not a page of this server.", "ログインのあとに戻るページが、このサーバーのページではありません。"}
	wordsStartAgain     = words{"This sign-in was not started in this browser, or more than ten minutes have passed since it started. Sign in again.", "このログインは、このブラウザで始めたものではないか、始めてから10分を過ぎました。もう一度ログインしてください。"}
	wordsDenied         = words{"Your organization's sign-in did not let you in.", "組織のログイン先で、ログインが許可されませんでした。"}
	wordsProvider       = words{"The sign-in could not be finished: your organization's sign-in did not answer as this server expects. Try again later; if it keeps happening, tell the people who run this server.", "組織のログイン先から、このサーバーで受け付けられない応答が返ったため、ログインを終えられませんでした。しばらくしてからもう一度試してください。続くときは、このサーバーの管理者に知らせてください。"}
	wordsFailed         = words{"The server failed. Try again later.", "サーバーでエラーが起きました。しばらくしてからもう一度試してください。"}
	wordsSignedInAs     = words{"You signed in to your organization's account as %s.", "組織のアカウントには%sでログインしました。"}
	wordsNotSignedIn    = words{"You are not signed in, or your sign-in has ended.", "ログインしていないか、ログインの期限が切れました。"}
	wordsCrossSite      = words{"This request came from another site, so it was not carried out.", "ほかのサイトから送られたリクエストのため、受け付けませんでした。"}
	wordsSignedOut      = words{"You have signed out of this server. Going on to sign out of your organization's account.", "このサーバーからログアウトしました。続けて、組織のアカウントからもログアウトします。"}
	wordsProviderLogout = words{"Sign out of your organization's account", "組織のアカウントからログアウトする"}
)

// sheet is what a page says: a title, paragraphs, a link, and whether the page goes on to the
// link at once.
type sheet struct {
	title    words
	text     []words
	link     string
	linkText words
	refresh  bool
}

// with answers the sheet with one more paragraph.
func (sh sheet) with(text words) sheet {
	sh.text = append(append([]words{}, sh.text...), text)
	return sh
}

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
{{- if .Refresh}}
<meta http-equiv="refresh" content="{{.Refresh}}">
{{- end}}
<title>{{.Title}}</title>
</head>
<body>
<main>
<h1>{{.Title}}</h1>
{{- range .Text}}
<p>{{.}}</p>
{{- end}}
{{- if .Link}}
<p><a href="{{.Link}}">{{.LinkText}}</a></p>
{{- end}}
</main>
</body>
</html>
`))

// page writes a page of plain HTML, with no script or style, in the person's language: the one
// they chose on the pages (pageLanguage), else their browser's.
func (s *SignIn) page(w http.ResponseWriter, r *http.Request, status int, sh sheet) {
	lang := pageLanguage(r)
	if sh.title == (words{}) {
		sh.title = wordsSignInTitle
	}
	data := struct {
		Lang, Title, Link, LinkText, Refresh string
		Text                                 []string
	}{Lang: lang, Title: sh.title.in(lang), Link: sh.link, LinkText: sh.linkText.in(lang)}
	for _, t := range sh.text {
		data.Text = append(data.Text, t.in(lang))
	}
	if sh.refresh && sh.link != "" {
		data.Refresh = "0; url=" + sh.link
	}
	var b bytes.Buffer
	if err := pageTemplate.Execute(&b, data); err != nil {
		s.log.Error("oidc: writing a page", "err", err)
		http.Error(w, "the server failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(b.Bytes())
}

// languageCookie is the cookie of the language a person chose on the pages, which the pages of
// signing in speak too: web.LanguageCookie, named here so that this package does not need web (a
// test signs in with web's cookie).
const languageCookie = "ringi-lang"

// pageLanguage answers the language of a page of signing in, as the pages choose theirs before
// anyone is signed in (server.LanguageChoice): the pages' cookie when it is English or Japanese,
// else the browser's (language).
func pageLanguage(r *http.Request) string {
	chosen := ""
	if c, err := r.Cookie(languageCookie); err == nil {
		chosen = c.Value
	}
	return choose(server.LanguageChoice{Chosen: chosen, Accept: r.Header.Get("Accept-Language")})
}

// language answers the language of the pages of signing in for an Accept-Language header: the most
// wanted of English and Japanese, read as the pages read it (server.AcceptLanguages), else "en".
func language(header string) string {
	return choose(server.LanguageChoice{Accept: header})
}

// choose answers the first language of a choice that is English or Japanese, else "en".
func choose(c server.LanguageChoice) string {
	if l := c.Choose(func(l string) bool { return l == "en" || l == "ja" }); l != "" {
		return l
	}
	return "en"
}
