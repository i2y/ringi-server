package server

import (
	"net/http/httptest"
	"testing"
	"time"
)

// TestAdminAnswersAsTheAPI checks that the reply of an administration's operation is written as the
// API writes every reply (writeReply): each outcome with the same status, Retry-After and body, the
// record's CSV aside. Unavailable is 503 with Retry-After of the seconds to wait. No operation of the
// administration ends Unavailable now, as none gives a ticket an event (tryOnce is the one place that
// answers it), so the replies are written as the operations would answer them.
func TestAdminAnswersAsTheAPI(t *testing.T) {
	r := httptest.NewRequest("POST", "/admin/bundles/uploads", nil)
	unavailable := Reply{Outcome: Unavailable, Problem: "the kind of this ticket failed to answer this event less than a minute ago", RetryAfter: 41500 * time.Millisecond}
	w := httptest.NewRecorder()
	writeAdmin(w, r, "admin_upload_bundle", unavailable)
	if w.Code != 503 || w.Header().Get("Retry-After") != "42" {
		t.Errorf("an administration's operation that ends Unavailable: %d, Retry-After %q, %s; want 503, 42", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	for _, reply := range []Reply{
		unavailable,
		{Outcome: Unavailable, Problem: "no wait is known"},
		{Outcome: OK, Body: []byte(`{"bundles":[]}`)},
		{Outcome: Refused, Body: []byte(`{"refused":"signature","text":"t","text_ja":"t","at":null}`)},
		{Outcome: NotFound, Problem: "no such draft"},
		{Outcome: NotFound, Problem: "no version of trip is in effect now", From: "2027-04-01"},
		{Outcome: Invalid, Problem: "start: now, a day written YYYY-MM-DD, description or never is expected"},
		{Outcome: Invalid, Problem: "the bundle does not pass its checks", Problems: []OrgProblem{{At: "vectors/trip", Text: "a vector does not agree"}}, MoreProblems: 2},
		{Outcome: Limited, Problem: "the server checks another bundle now", RetryAfter: time.Second},
		{Outcome: Limited, Problem: "no more comments"},
		{Outcome: TooLarge, Problem: "the bundle is larger than the server takes (64 bytes)"},
	} {
		api, admin := httptest.NewRecorder(), httptest.NewRecorder()
		writeReply(api, reply)
		writeAdmin(admin, r, "admin_upload_bundle", reply)
		if admin.Code != api.Code || admin.Header().Get("Retry-After") != api.Header().Get("Retry-After") ||
			admin.Header().Get("Content-Type") != api.Header().Get("Content-Type") || admin.Body.String() != api.Body.String() {
			t.Errorf("%s (%s): the administration answers %d, Retry-After %q, %s; the API %d, Retry-After %q, %s", reply.Outcome, reply.Problem,
				admin.Code, admin.Header().Get("Retry-After"), admin.Body, api.Code, api.Header().Get("Retry-After"), api.Body)
		}
	}
}
