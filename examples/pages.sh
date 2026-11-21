#!/bin/sh
# Takes one leave request from the request to the decision through the pages of the example server
# (go run ./cmd/server -dev-login), with curl, as a browser with no script does: each person signs
# in on the page of the development sign-in, and each form is sent with the fields the page holds
# and the values typed in. Each step prints what the server answered. The API answers the last
# step, with jq.
#
#	./pages.sh                          # the server at http://127.0.0.1:8080
#	SERVER=http://127.0.0.1:9090 ./pages.sh
set -eu
SERVER=${SERVER:-http://127.0.0.1:8080}
requester=$(mktemp)
approver=$(mktemp)
cfg=$(mktemp)
trap 'rm -f "$requester" "$approver" "$cfg"' EXIT

# signin PERSON JAR: chooses PERSON on the page of the development sign-in; the cookie goes to JAR.
signin() {
	curl -sS -o /dev/null -c "$2" -X POST "$SERVER/login" --data-urlencode "person=$1"
}

# hidden PAGE: the hidden fields of the page's form, as lines of curl's configuration that send each
# value as the page wrote it.
hidden() {
	printf '%s\n' "$1" | grep -o '<input type="hidden" name="[^"]*" value="[^"]*"' |
		sed -e 's/^<input type="hidden" name="\([^"]*\)" value="\([^"]*\)"$/\1=\2/' \
			-e 's/&#34;/"/g' -e "s/&#39;/'/g" -e 's/&#43;/+/g' -e 's/&lt;/</g' -e 's/&gt;/>/g' -e 's/&amp;/\&/g' \
			-e 's/[\\"]/\\&/g' -e 's/^/data-urlencode = "/' -e 's/$/"/'
}

# send JAR PATH PAGE ARGS...: sends the form of PAGE to PATH as the person of JAR, with its hidden
# fields and the values in ARGS. It prints the status, and where the server sends the browser or
# what the page shown again says first.
send() {
	jar=$1 path=$2
	hidden "$3" >"$cfg"
	shift 3
	out=$(curl -sS -b "$jar" -K "$cfg" -w '\n%{http_code} %{redirect_url}' "$@" "$SERVER$path")
	status=$(printf '%s\n' "$out" | tail -n 1)
	echo "$status"
	if [ "${status%% *}" != 303 ]; then
		printf '%s\n' "$out" | sed -n '/class="summary"/{n;s/<[^>]*>//g;s/^/   /;p;}'
	fi
}

echo "1. e1203 (Sales staff 3) signs in, opens the form of a leave request and sends it:"
signin e1203 "$requester"
form=$(curl -sS -b "$requester" "$SERVER/pages/requests/leave/new?lang=en")
answer=$(send "$requester" "/pages/requests/leave/new?lang=en" "$form" --data-urlencode "_do=button.submit" \
	--data-urlencode "f.kind=paid" --data-urlencode "f.first_day=2026-12-21" --data-urlencode "f.days=2" \
	--data-urlencode "f.reason=A family visit" --data-urlencode "comment=Two days before the holidays.")
echo "   $answer"
id=$(echo "$answer" | sed -n 's|.*/pages/tickets/\([^?]*\).*|\1|p')
if [ -z "$id" ]; then
	echo "   the server did not take it"
	exit 1
fi

echo "2. e1101 (the manager of Section 1A) signs in; the start page and the list say what waits:"
signin e1101 "$approver"
curl -sS -b "$approver" "$SERVER/pages/?lang=en" | grep -o '<a href="/pages/waiting/[^"]*">[^<]*</a> *<span class="count">[^<]*' |
	sed -e 's/<[^>]*>//g' -e 's/^/   /'
curl -sS -b "$approver" "$SERVER/pages/waiting/leave?lang=en" | grep -o "href=\"/pages/tickets/$id[^\"]*\"" | sed 's/^/   /'

echo "3. e1101 opens the card and approves:"
card=$(curl -sS -b "$approver" "$SERVER/pages/tickets/$id?lang=en")
echo "   $(send "$approver" "/pages/tickets/$id?lang=en" "$card" --data-urlencode "_do=button.approve" --data-urlencode "comment=Enjoy the holidays.")"

echo "4. The same card, sent again, is refused: the request changed after it was shown:"
send "$approver" "/pages/tickets/$id?lang=en" "$card" --data-urlencode "_do=button.approve" | sed 's/^/   /'

echo "5. The ticket, as the API answers e1203 with the same cookie:"
curl -sS -b "$requester" "$SERVER/tickets/$id" |
	jq -r '"   \(.ticket.status)", (.history[] | "   \(.did)  \(.by // "")  \(.comment // "")")'
