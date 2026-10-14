package crm

import (
	"testing"

	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
)

func TestDomainWorkEmail(t *testing.T) {
	cases := map[string]string{
		"a@gmail.com":        "",
		"a@GMAIL.com":        "",
		"a@acme.com":         "acme.com",
		"a@ACME.com ":        "acme.com",
		"a@localhost":        "",
		"no-at-sign":         "",
		"a@":                 "",
		"a@b@sub.acme.co.uk": "sub.acme.co.uk",
		"a@outlook.com":      "",
	}
	for email, want := range cases {
		if got := WorkEmailDomain(email); got != want {
			t.Errorf("WorkEmailDomain(%q) = %q, want %q", email, got, want)
		}
	}
	if !FreemailDomains["gmail.com"] || FreemailDomains["acme.com"] || len(FreemailDomains) < 12000 {
		t.Errorf("freemail list looks wrong: %d entries", len(FreemailDomains))
	}
}

func TestDomainFindOrCreateCompany(t *testing.T) {
	ctx := dbtest.Setup(t)
	id1, err := FindOrCreateCompanyByDomain(ctx, "acme.com")
	if err != nil || id1 == "" {
		t.Fatalf("create: %q %v", id1, err)
	}
	id2, err := FindOrCreateCompanyByDomain(ctx, "acme.com")
	if err != nil || id2 != id1 {
		t.Fatalf("second call: %q %v, want %q", id2, err, id1)
	}
	row, _ := db.Get(ctx, "SELECT name, domain, (SELECT COUNT(*) FROM companies) AS n FROM companies WHERE id = ?", id1)
	if row["name"] != "Acme" || row["domain"] != "acme.com" || row["n"] != float64(1) {
		t.Errorf("row = %v", row)
	}
	// A stored domain with protocol, www and trailing slash resolves to the same company.
	other, _ := db.Run(ctx, "INSERT INTO companies (id, name, domain) VALUES ('x1', 'Other', 'https://www.other.io/')")
	_ = other
	got, err := FindOrCreateCompanyByDomain(ctx, "other.io")
	if err != nil || got != "x1" {
		t.Errorf("tolerant match: %q %v", got, err)
	}
}
