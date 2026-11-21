package conformance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// workedVector is one line of requests/vectors/worked/<kind>.jsonl: a form filled in so far and an
// approver's edits (input), and what the kind's Worked entry answers of it.
type workedVector struct {
	From   string          `json:"from"`
	Input  json.RawMessage `json:"input"`
	Answer json.RawMessage `json:"answer"`
}

func readWorked(p *server.Package, kind string) ([]workedVector, error) {
	f, err := p.Files().Open("vectors/worked/" + kind + ".jsonl")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []workedVector
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var v workedVector
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, fmt.Errorf("vectors/worked/%s.jsonl: %w", kind, err)
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

// Worked gives each vector of what a form works out (requests/vectors/worked/) to a server and
// checks the answers: through the API's worked_<name> (POST /requests/<name>/worked) where the
// version is the one a new request takes on the day of the vectors (2026-11-16, at 10:00 in the
// kind's offset), else through Server.Worked, the way the pages ask it. Each answer must be the
// vector's, as JSON, and of the shape the contract gives it (Package.CheckAnswer). A package whose kinds have no Worked entry has no such vectors; one
// whose kinds have one and no vectors fails. It answers the counts of each kind.
func Worked(t *testing.T, s Setup) []Counts {
	t.Helper()
	org := &onceSource{src: s.Organization}
	var all []Counts
	for _, p := range s.Packages {
		for _, k := range p.Kinds() {
			if len(s.Kinds) > 0 && !contains(s.Kinds, k.Name) {
				continue
			}
			if k.Worked == nil {
				continue
			}
			vs, err := readWorked(p, k.Name)
			if err != nil {
				t.Fatalf("%s: %v", k.Name, err)
			}
			c := Counts{Kind: k.Name, Deliver: map[string]int{}}
			t.Run(k.Name, func(t *testing.T) {
				clock := servertest.NewClock(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC))
				srv := newServer(t, s, s.Store(t), org, clock, func() string { return "T-worked" }, nil, newParts())
				clock.Set(time.Date(2026, 11, 16, 10, 0, 0, 0, srv.Zone(k.Name, k.Version)))
				if err := srv.ImportOrganization(context.Background()); err != nil {
					t.Fatalf("importing the organization: %v", err)
				}
				version, current := srv.Current(k.Name, srv.Now())
				api := current && version == k.Version
				for _, v := range vs {
					c.Vectors++
					why := ""
					if api {
						why = workedThroughAPI(srv, p, k.Name, v)
						c.HTTP++
					} else {
						why = workedThroughServer(srv, k, v)
						c.Deliver["Server.Worked"]++
					}
					if why != "" {
						c.Failures = append(c.Failures, v.From+": "+why)
						if len(c.Failures) <= 20 {
							t.Errorf("%s, %s: %s", k.Name, v.From, why)
						}
						continue
					}
					c.Agree++
				}
				t.Logf("%s: %d of %d vectors of what the form works out agree; %d through HTTP, %v", k.Name, c.Agree, c.Vectors, c.HTTP, c.Deliver)
			})
			all = append(all, c)
		}
	}
	return all
}

// workedThroughAPI asks a vector of the API's worked_<name> and answers why the answer is not the
// vector's ("" when it is).
func workedThroughAPI(srv *server.Server, p *server.Package, kind string, v workedVector) string {
	r := httptest.NewRequest("POST", "/requests/"+kind+"/worked", bytes.NewReader(v.Input))
	r.Header.Set("X-Person", "worked-vectors")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if err := p.CheckAnswer("worked_"+kind, w.Code, w.Body.Bytes()); err != nil {
		return fmt.Sprintf("the API's answer %d is not of the shape the contract gives it: %v: %s", w.Code, err, clip(w.Body.Bytes()))
	}
	if w.Code != http.StatusOK {
		return fmt.Sprintf("the API answered %d: %s", w.Code, clip(w.Body.Bytes()))
	}
	return sameJSON(w.Body.Bytes(), v.Answer)
}

// workedThroughServer asks a vector of Server.Worked.
func workedThroughServer(srv *server.Server, k server.Kind, v workedVector) string {
	out, problem, ok, err := srv.Worked(k.Name, k.Version, v.Input)
	switch {
	case err != nil:
		return err.Error()
	case !ok:
		return "the server says the version has no Worked entry"
	case problem != "":
		return "the server did not take the input: " + problem
	}
	return sameJSON(out, v.Answer)
}

// sameJSON answers why two JSON values differ ("" when they are the same value).
func sameJSON(got, want []byte) string {
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		return "the answer is not JSON: " + err.Error()
	}
	if err := json.Unmarshal(want, &b); err != nil {
		return "the vector's answer is not JSON: " + err.Error()
	}
	if !reflect.DeepEqual(a, b) {
		return fmt.Sprintf("answered %s, and the vector says %s", clip(got), clip(want))
	}
	return ""
}
