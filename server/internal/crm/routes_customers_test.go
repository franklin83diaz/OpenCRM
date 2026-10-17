package crm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opencrm/internal/db/dbtest"
)

// The routes over the demo seed (demo/seed.sql: three companies, one won
// deal), as the parity run has them. Expected bodies are the TS reference's,
// byte for byte (scripts/parity/scenarios/customers.mjs makes these calls
// against it); the populated cases are customers_ts_test.go.

const customersSeed = `INSERT INTO companies (id, name, domain, industry) VALUES
  ('company-northline', 'Northline Studio', '', 'Design agency'),
  ('company-harbour', 'Harbour Property', '', 'Property services'),
  ('company-cedar', 'Cedar & Co', '', 'Consulting');
INSERT INTO contacts (id, first_name, last_name, email, company_id, title, status) VALUES
  ('contact-alex', 'Alex', 'Morgan', 'alex@northline.example', 'company-northline', 'Operations director', 'customer'),
  ('contact-jamie', 'Jamie', 'Chen', 'jamie@harbour.example', 'company-harbour', 'Managing director', 'lead'),
  ('contact-sam', 'Sam', 'Taylor', 'sam@cedar.example', 'company-cedar', 'Partner', 'lead');
INSERT INTO stages (key, label, color, position, is_won, is_lost) VALUES
  ('prospect', 'Prospect', 'sky', 0, 0, 0),
  ('qualified', 'Qualified', 'violet', 1, 0, 0),
  ('proposal', 'Proposal', 'amber', 2, 0, 0),
  ('negotiation', 'Negotiation', 'orange', 3, 0, 0),
  ('won', 'Won', 'emerald', 4, 1, 0),
  ('lost', 'Lost', 'rose', 5, 0, 1);
INSERT INTO deals (id, name, contact_id, value, stage, notes) VALUES
  ('deal-onboarding', 'Client onboarding portal', 'contact-alex', 4800, 'qualified', ''),
  ('deal-property', 'Property operations workspace', 'contact-jamie', 7200, 'proposal', ''),
  ('deal-reporting', 'Monthly client reporting', 'contact-sam', 2400, 'prospect', ''),
  ('deal-support', 'Client support rollout', 'contact-alex', 3600, 'won', '');`

type customersFixture struct {
	t   *testing.T
	api http.Handler
}

func newCustomersFixture(t *testing.T) *customersFixture {
	ctx := dbtest.Setup(t)
	mustRun(t, ctx, customersSeed)
	return &customersFixture{t: t, api: replayAPI(&App{})}
}

func (f *customersFixture) want(method, path, contentType, body string, status int, raw string) {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	f.api.ServeHTTP(w, r)
	ct := "application/json"
	if strings.HasPrefix(raw, "Malformed") {
		ct = "text/plain;charset=UTF-8"
	}
	if w.Code != status || w.Body.String() != raw || w.Header().Get("Content-Type") != ct {
		f.t.Errorf("%s %s %q %s\n got  %d %s %s\n want %d %s %s", method, path, contentType, body,
			w.Code, w.Header().Get("Content-Type"), w.Body.String(), status, ct, raw)
	}
}

const (
	alexLookup = `{"contact":{"id":"contact-alex","first_name":"Alex","last_name":"Morgan","title":"Operations director","status":"customer"},"company":{"id":"company-northline","name":"Northline Studio","domain":"","customer_since":"%s","renewal_date":null},"deals":[{"id":"deal-onboarding","name":"Client onboarding portal","stage":"qualified","stage_label":"Qualified","state":"open","value":4800,"close_date":""},{"id":"deal-support","name":"Client support rollout","stage":"won","stage_label":"Won","state":"won","value":3600,"close_date":""}],"last_call_at":null,"next_meeting":null}`
	nobody     = `{"contact":null,"company":null,"deals":[],"last_call_at":null,"next_meeting":null}`
)

func TestCustomersRoutes(t *testing.T) {
	f := newCustomersFixture(t)
	// The first read gives Northline its customer_since: the day its won deal
	// (through its contact) was last changed. The seed leaves that at SQLite's
	// datetime('now') (today, in the parity run); pinned here, so the day can't
	// change between the seed and a Go clock read across midnight UTC.
	mustRun(t, context.Background(), "UPDATE deals SET updated_at = '2026-10-14 23:59:59' WHERE id = 'deal-support'")
	const since = "2026-10-14"
	northline := `{"customers":[{"id":"company-northline","name":"Northline Studio","domain":"","customer_since":"` + since + `","renewal_date":null,"status":"unknown","reasons":["Calls aren't synced, email isn't synced"],"last_touch_at":null,"days_quiet":null,"last_meeting_at":null,"next_meeting_at":null,"last_summary":null,"last_sentiment":null,"ours_open":0,"ours_overdue":0,"theirs_open":0,"ideas":0,"expansion":0,"risks":0}],"focus":[],"upcoming":[],"counts":{"red":0,"yellow":0,"unknown":1,"green":0},"sight":{"calls":"off","emails":"off"}}`
	for _, q := range []string{"", "?tz=120", "?tz=90.5", "?tz=abc", "?tz=", "?tz=99999", "?tz=-Infinity", "?tz=0x1F", "?tz", "?TZ=5&other=1"} {
		f.want("GET", "/api/customers"+q, "", "", 200, northline)
	}
	tzTwice := `{"success":false,"error":{"issues":[{"code":"invalid_type","expected":"string","received":"array","path":["tz"],"message":"Expected string, received array"}],"name":"ZodError"}}`
	f.want("GET", "/api/customers?tz=1&tz=2", "", "", 400, tzTwice)
	f.want("GET", "/api/customers?tz=1&tz=", "", "", 400, tzTwice)

	alex := strings.Replace(alexLookup, "%s", since, 1)
	f.want("GET", "/api/lookup?email=alex@northline.example", "", "", 200, alex)
	f.want("GET", "/api/lookup?email=%20ALEX@Northline.EXAMPLE%20", "", "", 200, alex)
	f.want("GET", "/api/lookup?email=+alex@northline.example+&domain=harbour.example", "", "", 200, alex)
	f.want("GET", "/api/lookup?email=jamie@harbour.example", "", "", 200, `{"contact":{"id":"contact-jamie","first_name":"Jamie","last_name":"Chen","title":"Managing director","status":"lead"},"company":{"id":"company-harbour","name":"Harbour Property","domain":"","customer_since":null,"renewal_date":null},"deals":[{"id":"deal-property","name":"Property operations workspace","stage":"proposal","stage_label":"Proposal","state":"open","value":7200,"close_date":""}],"last_call_at":null,"next_meeting":null}`)
	f.want("GET", "/api/lookup?domain=northline.example", "", "", 200, nobody)
	f.want("GET", "/api/lookup?email=someone@gmail.com", "", "", 200, nobody)
	neither := `{"error":"Give an email, a domain, or both"}`
	for _, q := range []string{"", "?email=", "?email=%20%20&domain=+", "?other=alex@northline.example", "?email"} {
		f.want("GET", "/api/lookup"+q, "", "", 400, neither)
	}
	f.want("GET", "/api/lookup?domain=a&domain=b&email=x&email=y", "", "", 400, `{"success":false,"error":{"issues":[{"code":"invalid_type","expected":"string","received":"array","path":["email"],"message":"Expected string, received array"},{"code":"invalid_type","expected":"string","received":"array","path":["domain"],"message":"Expected string, received array"}],"name":"ZodError"}}`)

	const js = "application/json"
	f.want("POST", "/api/lookup", js, `{"addresses":[{"email":"alex@northline.example"},{},{"domain":"gmail.com"},{"email":"sam@cedar.example","domain":"https://www.cedar.example/","extra":1}]}`, 200,
		`{"results":[`+alex+`,`+nobody+`,`+nobody+`,{"contact":{"id":"contact-sam","first_name":"Sam","last_name":"Taylor","title":"Partner","status":"lead"},"company":{"id":"company-cedar","name":"Cedar & Co","domain":"","customer_since":null,"renewal_date":null},"deals":[{"id":"deal-reporting","name":"Monthly client reporting","stage":"prospect","stage_label":"Prospect","state":"open","value":2400,"close_date":""}],"last_call_at":null,"next_meeting":null}]}`)
	f.want("POST", "/api/lookup", js, `{"addresses":[]}`, 400, `{"success":false,"error":{"issues":[{"code":"too_small","minimum":1,"type":"array","inclusive":true,"exact":false,"message":"Array must contain at least 1 element(s)","path":["addresses"]}],"name":"ZodError"}}`)
	tooMany := `{"addresses":[` + strings.Repeat(`{},`, 100) + `{}]}`
	f.want("POST", "/api/lookup", js, tooMany, 400, `{"success":false,"error":{"issues":[{"code":"too_big","maximum":100,"type":"array","inclusive":true,"exact":false,"message":"Array must contain at most 100 element(s)","path":["addresses"]}],"name":"ZodError"}}`)
	required := `{"success":false,"error":{"issues":[{"code":"invalid_type","expected":"array","received":"undefined","path":["addresses"],"message":"Required"}],"name":"ZodError"}}`
	f.want("POST", "/api/lookup", js, `{}`, 400, required)
	f.want("POST", "/api/lookup", js, `{"other":[]}`, 400, required)
	f.want("POST", "/api/lookup", js, `{"addresses":null}`, 400, `{"success":false,"error":{"issues":[{"code":"invalid_type","expected":"array","received":"null","path":["addresses"],"message":"Expected array, received null"}],"name":"ZodError"}}`)
	f.want("POST", "/api/lookup", js, `{"addresses":[{"email":5,"domain":null},"x",[],{"email":"a@b.c","extra":1},null,{"domain":true}]}`, 400, `{"success":false,"error":{"issues":[{"code":"invalid_type","expected":"string","received":"number","path":["addresses",0,"email"],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"null","path":["addresses",0,"domain"],"message":"Expected string, received null"},{"code":"invalid_type","expected":"object","received":"string","path":["addresses",1],"message":"Expected object, received string"},{"code":"invalid_type","expected":"object","received":"array","path":["addresses",2],"message":"Expected object, received array"},{"code":"invalid_type","expected":"object","received":"null","path":["addresses",4],"message":"Expected object, received null"},{"code":"invalid_type","expected":"string","received":"boolean","path":["addresses",5,"domain"],"message":"Expected string, received boolean"}],"name":"ZodError"}}`)
	f.want("POST", "/api/lookup", js, `[]`, 400, `{"success":false,"error":{"issues":[{"code":"invalid_type","expected":"object","received":"array","path":[],"message":"Expected object, received array"}],"name":"ZodError"}}`)
	for _, bad := range []string{"{", "", "nope"} {
		f.want("POST", "/api/lookup", js, bad, 400, "Malformed JSON in request body")
	}
	// Validated only when the request says it is JSON: otherwise the handler
	// got {} and threw reading addresses.map.
	body := `{"addresses":[{"email":"alex@northline.example"}]}`
	noMap := `{"error":"Cannot read properties of undefined (reading 'map')"}`
	for _, ct := range []string{"", "text/plain", "Application/JSON", "multipart/form-data; boundary=x"} {
		f.want("POST", "/api/lookup", ct, body, 500, noMap)
	}
	for _, ct := range []string{"application/json;", "application/jsonx"} {
		f.want("POST", "/api/lookup", ct, body, 400, required)
	}
	for _, ct := range []string{"application/json; charset=utf-8", "application/vnd.api+json"} {
		f.want("POST", "/api/lookup", ct, body, 200, `{"results":[`+alex+`]}`)
	}
}
