package web

import (
	"errors"
	"net/http"
	"strings"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/screens/page"
)

// FindPath is where a picker finds the values it has too many of to list (DESIGN 5.1), the address
// the screens' /find holds (ringi's DESIGN 13.2): a renderer's PersonPicker and MasterPicker, and
// the pages' script, find under it, at "/people", "/teams" or "/masters/<file>/<master>".
const FindPath = "/a2ui/find"

// among answers what a picker of the pages chooses among, as Server.FindChoices names it: "people"
// for a person's picker ("person"), "teams" for a team's ("team"), and a master's name
// (masters.master) for a master's path ("company_masters/vendors").
func among(of string) string {
	switch of {
	case "person":
		return "people"
	case "team":
		return "teams"
	}
	return strings.Replace(of, "/", ".", 1)
}

// options are values as a picker of the pages offers them, in their order: each named in the
// language with its ID or key, as a find answers it (server.Choice.Found).
func options(cs []server.Choice, lang string) []page.Option {
	out := make([]page.Option, len(cs))
	for i, c := range cs {
		f := c.Found(lang)
		out[i] = page.Option{Value: f.Value, Label: f.Label, Name: f.Name}
	}
	return out
}

// finder finds among the values of a picker of the pages as the API's finds do (server.Find): the
// ones the words find, the closest first, at most limit of them, named in lang, and whether more
// are found. It is the way a part with no list finds its value (page.Picks.Find).
func finder(cs []server.Choice, lang string) func(query string, limit int) ([]page.Option, bool) {
	return func(query string, limit int) ([]page.Option, bool) {
		found, more := server.Find(cs, query, limit)
		return options(found, lang), more
	}
}

// findValues answers GET /a2ui/find/people, /a2ui/find/teams and /a2ui/find/masters/{file}/{master}
// (DESIGN 5.1) as the API answers find_people, find_teams and find_master_<file>_<master>, with the
// same function (Server.FindValues): the same parameters, read in the same order, the same values in
// the same order, the same 400, and the same 404: to a person the version of the organization in
// effect does not have, the people and the teams are not there, as get_organization says, and
// anything else to find, or a master no kind chooses from, is a path the API does not have. A
// ?lang= left out is the first of Accept-Language that is en or ja, else en.
func (h *Handler) findValues(w http.ResponseWriter, r *http.Request, p server.Person) {
	var of string
	switch {
	case r.PathValue("file") != "":
		of = r.PathValue("file") + "." + r.PathValue("master")
	case r.PathValue("of") == "people", r.PathValue("of") == "teams":
		of = r.PathValue("of")
	default:
		writeError(w, http.StatusNotFound, noPath)
		return
	}
	a, err := h.srv.FindValues(r.Context(), p, of, r.URL.Query(), server.AcceptLanguages(r.Header.Get("Accept-Language")))
	var bad *server.FindError
	var unread *server.OrgUnread
	switch {
	case errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, bad.Error())
	case errors.As(err, &unread):
		writeError(w, http.StatusNotFound, unread.Error())
	case notFound(err):
		writeError(w, http.StatusNotFound, noPath)
	case err != nil:
		h.fail(w, "finding the values of "+of, err)
	default:
		writeJSON(w, http.StatusOK, a)
	}
}

// noPath is the API's 404 of a path it does not have, which the screens' /find answers for
// anything the API's finds do not find.
const noPath = "no such path"
