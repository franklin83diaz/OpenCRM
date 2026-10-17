package crm

// Email domains: telling a work address from a personal one, and the company a
// work address belongs to. Shared by contact import, contact create and the
// Gmail sync, so the three agree on what counts as a company's domain.

import (
	"context"
	_ "embed"
	"strings"

	"opencrm/internal/db"
	"opencrm/internal/js"
)

// freemail.txt is a copy of the maintained `free-email-domains` npm list, which
// is no longer one of the repo's dependencies: Copyright © 2017 Kiko Beats,
// under the MIT licence, whose notice is in freemail.LICENSE beside it. To
// refresh it, install that package somewhere else and run, from the repo
// root, with D its domains.json:
//
//	D=<dir>/node_modules/free-email-domains/domains.json node -e 'const d=require(process.env.D);const s=[...new Set(d.map(x=>x.toLowerCase()))].sort();require("fs").writeFileSync("server/internal/crm/freemail.txt",s.join("\n")+"\n")'
//
//go:embed freemail.txt
var freemailTxt string

// FreemailDomains are the personal/free email providers (gmail, outlook, …): a
// company is never inferred from these, else every import would spawn a
// "Gmail" company. Sourced from the maintained `free-email-domains` list
// (~12.8k domains) rather than hand-curated.
var FreemailDomains = func() map[string]bool {
	m := map[string]bool{}
	for _, line := range strings.Split(freemailTxt, "\n") {
		if d := strings.TrimSpace(line); d != "" {
			m[d] = true
		}
	}
	return m
}()

// WorkEmailDomain is the domain of a work email, or "" if it has none or is a
// free provider.
func WorkEmailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	domain := strings.ToLower(js.Trim(email[at+1:]))
	if domain == "" || !strings.Contains(domain, ".") {
		return ""
	}
	if FreemailDomains[domain] {
		return ""
	}
	return domain
}

// FindOrCreateCompanyByDomain finds a company whose stored domain resolves to
// `domain` (tolerating protocol / www / trailing slash), else creates a
// lightweight one named after the domain. Used to auto-link a contact to a
// company from its work email.
func FindOrCreateCompanyByDomain(ctx context.Context, domain string) (string, error) {
	existing, err := db.Get(ctx,
		`SELECT id FROM companies
      WHERE lower(replace(replace(replace(rtrim(domain,'/'),'https://',''),'http://',''),'www.','')) = ?
      LIMIT 1`,
		domain)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return js.String(existing["id"]), nil
	}
	id := js.UUID()
	sld := strings.SplitN(domain, ".", 2)[0]
	if sld == "" {
		sld = domain
	}
	name := sld
	if sld != "" {
		r := []rune(sld)
		name = strings.ToUpper(string(r[0])) + string(r[1:])
	}
	if _, err := db.Run(ctx, "INSERT INTO companies (id, name, domain) VALUES (?, ?, ?)", id, name, domain); err != nil {
		return "", err
	}
	return id, nil
}
