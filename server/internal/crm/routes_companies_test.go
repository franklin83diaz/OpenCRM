package crm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Review Focus cases this port owns, against the Go server alone. The
// expected bodies are the TS reference's (the parity scenario and the
// TS-replay test make the same calls against it).

func companiesRequest(t *testing.T, api http.Handler, method, path, body string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// Review Focus 2: "&", "<", ">" and non-ASCII come back as they were sent,
// not as & and friends.
func TestCompaniesNameRoundTripsUnescaped(t *testing.T) {
	recordedDB(t, nil)
	api := New(&App{})
	status, raw := companiesRequest(t, api, "POST", "/api/companies", `{"name":"Cedar & Co <b>","notes":"Zoë's"}`)
	if status != 201 || !strings.Contains(raw, `"name":"Cedar & Co <b>"`) || !strings.Contains(raw, `"notes":"Zoë's"`) {
		t.Fatalf("create: %d %s", status, raw)
	}
	status, raw = companiesRequest(t, api, "GET", "/api/companies?search=%26", "")
	if status != 200 || !strings.Contains(raw, `"name":"Cedar & Co <b>"`) || strings.Contains(raw, `\u00`) {
		t.Fatalf("list: %d %s", status, raw)
	}
}

// Review Focus 1: a field sent as null is not a field left out.
func TestCompaniesUpdateNullIsNotAbsent(t *testing.T) {
	recordedDB(t, []string{`INSERT INTO companies (id, name, domain, customer_since) VALUES ('co-1', 'Acme', 'acme.example', '2025-01-01')`})
	api := New(&App{})
	for _, c := range []struct {
		body   string
		status int
		raw    string // the whole body, or a part of it for a 200
	}{
		// domain is z.string().optional(): null is a zod error, not "unset".
		{`{"domain": null}`, 400, `{"success":false,"error":{"issues":[{"code":"invalid_type","expected":"string","received":"null","path":["domain"],"message":"Expected string, received null"}],"name":"ZodError"}}`},
		// Nothing sent: nothing to update, whatever the stored values.
		{`{}`, 400, `{"error":"No fields to update"}`},
		// customer_since is .nullable(): null clears it, and only it.
		{`{"customer_since": null}`, 200, `"customer_since":null`},
		{`{"customer_since": null}`, 200, `"domain":"acme.example"`},
		{`{"domain": ""}`, 200, `"domain":""`},
	} {
		status, raw := companiesRequest(t, api, "PUT", "/api/companies/co-1", c.body)
		if status != c.status || (c.status == 200 && !strings.Contains(raw, c.raw)) || (c.status != 200 && raw != c.raw) {
			t.Errorf("PUT %s: %d %s\n want %d %s", c.body, status, raw, c.status, c.raw)
		}
	}
}

// Review Focus 3: 150 ids are deleted in chunks under D1's 100 bound
// parameters, 90 then 60, and the relations to them cleared the same way.
func TestCompaniesBulkDeleteChunks(t *testing.T) {
	rec := recordedDB(t, []string{`INSERT INTO companies (id, name) VALUES ('co-a', 'A'), ('co-b', 'B'), ('co-c', 'C'), ('co-keep', 'Keep')`})
	ctx := context.Background()
	if _, err := CreateRelation(ctx, RelationInput{EntityType: Contact, Key: "partner_id", Label: "Partner", RelationType: ManyToOne,
		TargetEntity: Company, InverseKey: "partners", InverseLabel: "Partners"}); err != nil {
		t.Fatal(err)
	}
	mustRun(t, ctx, `INSERT INTO contacts (id, first_name, partner_id) VALUES ('ct-1', 'Ann', 'co-c'), ('ct-2', 'Bo', 'co-keep')`)
	api := New(&App{})

	ids := make([]string, 150)
	for i := range ids {
		ids[i] = `"ghost-` + string(rune('a'+i/26)) + string(rune('a'+i%26)) + `"`
	}
	ids[0], ids[95], ids[149] = `"co-a"`, `"co-b"`, `"co-c"`
	rec.mu.Lock()
	rec.log = nil
	rec.mu.Unlock()
	status, raw := companiesRequest(t, api, "POST", "/api/companies/bulk-delete", `{"ids":[`+strings.Join(append(ids, `"co-a"`), ",")+`]}`)
	if status != 200 || raw != `{"deleted":3}` {
		t.Fatalf("bulk delete: %d %s", status, raw)
	}
	var deletes, detaches []int
	for _, st := range rec.log {
		switch {
		case strings.HasPrefix(st.SQL, "DELETE FROM companies WHERE id IN ("):
			deletes = append(deletes, len(st.Params))
		case strings.HasPrefix(st.SQL, `UPDATE contacts SET "partner_id" = NULL WHERE "partner_id" IN (`):
			detaches = append(detaches, len(st.Params))
		}
	}
	if len(deletes) != 2 || deletes[0] != 90 || deletes[1] != 60 || len(detaches) != 2 || detaches[0] != 90 || detaches[1] != 60 {
		t.Fatalf("chunks: deletes %v, detaches %v", deletes, detaches)
	}
	row, err := rec.inner.Query(ctx, "SELECT id, partner_id FROM contacts ORDER BY id", nil)
	if err != nil || len(row) != 2 || row[0]["partner_id"] != nil || row[1]["partner_id"] != "co-keep" {
		t.Fatalf("relations after: %v %v", row, err)
	}
}
