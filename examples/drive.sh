#!/bin/sh
# Takes one expense from the request to the decision through the HTTP API of the example server
# (go run ./cmd/server), with curl and jq. Each step prints what the server answered.
#
#	./drive.sh                       # the server at http://127.0.0.1:8080
#	API=http://127.0.0.1:9090 ./drive.sh
set -eu
API=${API:-http://127.0.0.1:8080}

# call METHOD PATH PERSON [BODY]: one request as PERSON; a POST gets an idempotency key of its own.
call() {
	if [ $# -ge 4 ]; then
		curl -sS -X "$1" "$API$2" -H "X-Person: $3" -H "Content-Type: application/json" \
			-H "Idempotency-Key: $(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')" -d "$4"
	else
		curl -sS -X "$1" "$API$2" -H "X-Person: $3"
	fi
}

# upload PERSON NAME: attaches a small PDF of that name as PERSON, and prints the file's ID.
upload() {
	printf '%%PDF-1.4\n%% %s (fictional)\n' "$2" |
		curl -sS -X POST "$API/files" -H "X-Person: $1" -F "file=@-;filename=$2;type=application/pdf" | jq -r '.id'
}

echo "1. What e1201 (on the sales staff of Section 1A) may submit:"
call GET /requests e1201 | jq -r '.requests[] | "   \(.name)  \(.title)"'

echo "2. e1201 attaches the receipts, and asks for a trip to Fukuoka, 118,000 yen:"
receipt=$(upload e1201 fukuoka-receipts.pdf)
ticket=$(call POST /requests/expense/tickets e1201 '{
  "form": {
    "category": "travel",
    "items": [{"what": "Flight to Fukuoka and back", "price": 64000},
              {"what": "Hotel, three nights", "price": 54000}],
    "purpose": "A visit to a customer in Fukuoka",
    "used_on": "2026-12-02",
    "trip_to": "Fukuoka",
    "receipt": "'"$receipt"'"
  }
}')
id=$(echo "$ticket" | jq -r '.ticket.id // empty')
if [ -z "$id" ]; then
	echo "   the server did not take it: $ticket"
	exit 1
fi
echo "$ticket" | jq -r '"   ticket \(.ticket.id): \(.ticket.status), at the stage \(.ticket.stage)"'

echo "3. What e1101 (the manager of Section 1A) waits for:"
call GET /waiting e1101 | jq -r '.tickets[] | "   \(.id)  \(.request)  \(.stage)"'

echo "4. e1101 approves it:"
seen=$(echo "$ticket" | jq -c '.ticket.revision')
ticket=$(call POST "/tickets/$id/approve" e1101 "{\"revision\": $seen}")
echo "$ticket" | jq -r '"   \(.ticket.status), at the stage \(.ticket.stage)"'

echo "5. e3101 (the head of Sales Department 1) approves what was shown before e1101's approval:"
call POST "/tickets/$id/approve" e3101 "{\"revision\": $seen}" | jq -r '"   refused, \(.refused): \(.text)"'

echo "6. e3101 decides it on what is shown now:"
revision=$(echo "$ticket" | jq -c '.ticket.revision')
call POST "/tickets/$id/approve" e3101 "{\"revision\": $revision, \"comment\": \"Have a good trip\"}" |
	jq -r '"   \(.ticket.status)"'

echo "7. The ticket's history, as e1201 sees it:"
call GET "/tickets/$id" e1201 | jq -r '.history[] | "   \(.did)  \(.by // "")  \(.comment // "")"'

echo "8. e1201 asks for a day trip of 12,000 yen, which the agent decides:"
receipt=$(upload e1201 nagoya-receipts.pdf)
day=$(call POST /requests/expense/tickets e1201 '{
  "form": {
    "category": "travel",
    "items": [{"what": "Train to Nagoya and back", "price": 12000}],
    "purpose": "A visit to a customer in Nagoya",
    "used_on": "2026-12-03",
    "trip_to": "Nagoya",
    "receipt": "'"$receipt"'"
  }
}' | jq -r '.ticket.id')
sleep 2
call GET "/tickets/$day" e1201 | jq -r '"   \(.ticket.status): " + ([.history[] | .did] | join(", "))'
