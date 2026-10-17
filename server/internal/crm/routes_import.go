package crm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Bulk import (CSV / XLSX) ────────────────────────────────────────
// Plain routes. The body is read with c.req.json() and no .catch, inside the
// try: one that isn't JSON, a null body or a null row, and a cell that isn't
// text where text was trimmed, are 500s with what V8 said.
//
// Route order: POST /api/contacts/import and POST /api/companies/import are
// the only POST routes on those paths; other methods there reach the
// /api/contacts/{id} and /api/companies/{id} routes, as in Hono.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("POST /api/contacts/import", app.importContacts)
		rt.Handle("POST /api/companies/import", app.importCompanies)
	})
}

var contactStatuses = []string{"lead", "active", "inactive", "churned"}

const (
	lookupChunk = 100 // one-param `name IN (…)` lookups
	// Widest company insert is (id, name, domain, industry, phone) = 5 params/row.
	// D1 caps bound parameters at 100 per query, so chunk at 100/5 = 20 rows.
	companyCols        = 5
	companyInsertChunk = 100 / companyCols // 20 rows/stmt → 100 params ≤ 100
)

// companyNameFromDomain is a first-guess company name from a domain:
// "acme.com" → "Acme". Crude but editable post-import, matching how HubSpot
// seeds domain-derived companies.
func companyNameFromDomain(domain string) string {
	label := strings.Split(strings.TrimPrefix(domain, "www."), ".")[0]
	if label == "" {
		label = domain
	}
	return jsUpperFirst(label)
}

// jsUpperFirst is s.charAt(0).toUpperCase() + s.slice(1). charAt takes one
// UTF-16 unit: a character outside the BMP is half a surrogate pair there,
// which no case mapping changes. JS upper-cases ß to SS; the hundred other
// characters it expands (ǰ, ΐ, Greek with a iota subscript, ligatures) never
// start a domain, and keep Go's one-character mapping.
func jsUpperFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	switch {
	case size == 0 || r > 0xFFFF:
		return s
	case r == 'ß':
		return "SS" + s[size:]
	}
	return strings.ToUpper(string(r)) + s[size:]
}

// rowText is `(r.<key> || "").trim()` on an imported row: a truthy value that
// isn't a string has no trim, which TS threw as this TypeError.
func rowText(r map[string]any, key string) (string, error) {
	v := r[key]
	if !js.Truthy(v) {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf(`(r.%s || "").trim is not a function`, key)
	}
	return js.Trim(s), nil
}

// rowTexts reads the keys of an imported row in order: the first one that
// throws is TS's error.
func rowTexts(r map[string]any, keys ...string) ([]string, error) {
	out := make([]string, len(keys))
	for i, k := range keys {
		s, err := rowText(r, k)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

// importBody is the body read with c.req.json() and no catch, and its rows:
// `Array.isArray(body.<key>) ? body.<key> : []`, with each row's key order.
func importBody(r *http.Request, key string) (map[string]any, []any, *keyOrder, error) {
	v, err := web.JSONBody(r)
	if err != nil {
		return nil, nil, nil, err
	}
	if v == nil {
		return nil, nil, nil, readNull(key)
	}
	body := web.Object(v) // an array, string or number has no rows
	rows, _ := body[key].([]any)
	return body, rows, bodyOrder(r).field(key), nil
}

// rowCustom is a row's `custom` bag when it is an object (TS kept an array
// too, whose index keys are never a field's key), and its keys in the
// request's order.
func rowCustom(r map[string]any, order *keyOrder) (map[string]any, []string) {
	custom, _ := r["custom"].(map[string]any)
	if custom == nil {
		return nil, nil
	}
	return custom, order.field("custom").orderOf(custom)
}

// importCell is coerceForImport(row.custom?.[key], def). A json field's value
// that isn't text is JSON.stringify'd, keeping the request's key order.
func importCell(custom map[string]any, key string, def CustomFieldDef, order *keyOrder) any {
	v := custom[key]
	if def.FieldType == "json" && v != nil {
		if _, isText := v.(string); !isText {
			v = jsStringify(v, order.field("custom").field(key))
		}
	}
	return CoerceForImport(v, def)
}

type importedContactsBody struct {
	Imported         int `json:"imported"`
	CompaniesCreated int `json:"companiesCreated"`
	Skipped          int `json:"skipped"`
}

// importContact is one cleaned contact row. inferDomain is the work-email
// domain to build a company from — set only when opted in, the row has no
// explicit company, and the email domain isn't a free provider.
type importContact struct {
	firstName, lastName, email, phone, title, status      string
	company, companyDomain, companyIndustry, companyPhone string
	inferDomain                                           string
	custom                                                map[string]any
	customKeys                                            []string
	order                                                 *keyOrder
}

// companyDraft is a company to create: its attributes from the first row
// that carries each one.
type companyDraft struct{ name, domain, industry, phone string }

// importContacts is the bulk contact import (CSV / XLSX): contacts with a
// first name go in, their companies found by name (or, opted in, by
// work-email domain) or created.
func (app *App) importContacts(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		body, rows, orders, err := importBody(r, "contacts")
		if err != nil {
			return 0, nil, err
		}
		if len(rows) == 0 {
			return fail(400, "No rows to import")
		}
		if len(rows) > 2000 {
			return fail(400, "Import is limited to 2000 rows at a time")
		}
		// Opt-in: infer/associate a company from each contact's work-email
		// domain when the row has no explicit company column.
		inferFromEmail := body["inferCompanyFromEmail"] == true

		// Keep only rows with at least a first name; normalize fields.
		var clean []importContact
		for i, raw := range rows {
			if raw == nil {
				return 0, nil, readNull("email")
			}
			row, _ := raw.(map[string]any) // a number, string or list has none of the keys
			f, err := rowTexts(row, "email", "company", "first_name", "last_name", "phone", "title", "status",
				"company_domain", "company_industry", "company_phone")
			if err != nil {
				return 0, nil, err
			}
			c := importContact{email: f[0], company: f[1], firstName: f[2], lastName: f[3], phone: f[4], title: f[5],
				status: "lead", companyDomain: f[7], companyIndustry: f[8], companyPhone: f[9], order: orders.item(i)}
			if indexOf(contactStatuses, f[6]) >= 0 {
				c.status = f[6]
			}
			if inferFromEmail && c.company == "" && c.email != "" {
				c.inferDomain = WorkEmailDomain(c.email)
			}
			c.custom, c.customKeys = rowCustom(row, c.order)
			if c.firstName != "" {
				clean = append(clean, c)
			}
		}
		skipped := len(rows) - len(clean)
		if len(clean) == 0 {
			return fail(400, "No rows had a first name to import")
		}

		// ── Resolve company names → ids (set-based, case-insensitive) ──
		// Distinct names, keeping the first-seen original casing for any we
		// create. Company attributes (domain/industry/phone) are captured from
		// the first row that carries each one, so a new company lands fully
		// populated instead of as a name-only stub.
		drafts := map[string]*companyDraft{}
		var draftKeys []string // the Map's insertion order
		for _, c := range clean {
			if c.company == "" {
				continue
			}
			key := jsLower(c.company)
			d := drafts[key]
			if d == nil {
				drafts[key] = &companyDraft{c.company, c.companyDomain, c.companyIndustry, c.companyPhone}
				draftKeys = append(draftKeys, key)
				continue
			}
			if d.domain == "" {
				d.domain = c.companyDomain
			}
			if d.industry == "" {
				d.industry = c.companyIndustry
			}
			if d.phone == "" {
				d.phone = c.companyPhone
			}
		}
		companyIDs := map[string]any{} // lowercased name → id
		loadIDs := func(names []string) error {
			for _, group := range Chunk(names, lookupChunk) {
				found, err := db.Query(ctx, fmt.Sprintf("SELECT id, name FROM companies WHERE name COLLATE NOCASE IN (%s)", marks(len(group))), stringParams(group)...)
				if err != nil {
					return err
				}
				for _, co := range found {
					companyIDs[jsLower(colText(co["name"]))] = co["id"]
				}
			}
			return nil
		}
		allNames := make([]string, len(draftKeys))
		for i, k := range draftKeys {
			allNames[i] = drafts[k].name
		}
		if err := loadIDs(allNames); err != nil {
			return 0, nil, err
		}

		// Create the ones that don't exist yet (multi-row insert), then reload
		// ids. Existing companies are reused untouched — dedupe-by-name wins,
		// so we never overwrite an established company's attributes from an
		// import.
		var missing []*companyDraft
		for _, k := range draftKeys {
			if _, ok := companyIDs[k]; !ok {
				missing = append(missing, drafts[k])
			}
		}
		for _, group := range Chunk(missing, companyInsertChunk) {
			placeholders := strings.TrimSuffix(strings.Repeat("(?, ?, ?, ?, ?), ", len(group)), ", ")
			params := make([]any, 0, len(group)*companyCols)
			for _, co := range group {
				params = append(params, js.UUID(), co.name, co.domain, co.industry, co.phone)
			}
			if _, err := db.Run(ctx, "INSERT INTO companies (id, name, domain, industry, phone) VALUES "+placeholders, params...); err != nil {
				return 0, nil, err
			}
		}
		if len(missing) > 0 {
			names := make([]string, len(missing))
			for i, co := range missing {
				names[i] = co.name
			}
			if err := loadIDs(names); err != nil {
				return 0, nil, err
			}
		}

		// ── Infer companies from work-email domains (opt-in) ──
		// Runs after the name phase so a domain match can land on a company
		// that phase just created (e.g. a mapped "Acme" with domain acme.com
		// absorbs a contact whose email is @acme.com). Existing companies
		// match by domain first; unmatched domains create a company named
		// from the domain.
		var allDomains []string
		seenDomain := map[string]bool{}
		for _, c := range clean {
			if c.inferDomain != "" && !seenDomain[c.inferDomain] {
				seenDomain[c.inferDomain] = true
				allDomains = append(allDomains, c.inferDomain)
			}
		}
		companyIDByDomain := map[string]any{} // domain (lower) → id (UUID)
		loadIDsByDomain := func(domains []string) error {
			for _, group := range Chunk(domains, lookupChunk) {
				found, err := db.Query(ctx, fmt.Sprintf("SELECT id, domain FROM companies WHERE domain <> '' AND domain COLLATE NOCASE IN (%s)", marks(len(group))), stringParams(group)...)
				if err != nil {
					return err
				}
				for _, co := range found {
					if js.Truthy(co["domain"]) {
						companyIDByDomain[jsLower(colText(co["domain"]))] = co["id"]
					}
				}
			}
			return nil
		}
		if len(allDomains) > 0 {
			if err := loadIDsByDomain(allDomains); err != nil {
				return 0, nil, err
			}
		}
		var missingDomains []string
		for _, d := range allDomains {
			if _, ok := companyIDByDomain[d]; !ok {
				missingDomains = append(missingDomains, d)
			}
		}
		for _, group := range Chunk(missingDomains, companyInsertChunk) {
			placeholders := strings.TrimSuffix(strings.Repeat("(?, ?, ?), ", len(group)), ", ")
			params := make([]any, 0, len(group)*3)
			for _, d := range group {
				params = append(params, js.UUID(), companyNameFromDomain(d), d)
			}
			if _, err := db.Run(ctx, "INSERT INTO companies (id, name, domain) VALUES "+placeholders, params...); err != nil {
				return 0, nil, err
			}
		}
		if len(missingDomains) > 0 {
			if err := loadIDsByDomain(missingDomains); err != nil {
				return 0, nil, err
			}
		}
		companiesCreated := len(missing) + len(missingDomains)

		// ── Bulk-insert contacts (multi-row VALUES, chunked) ──
		// Mapped custom-field columns ride along in the same INSERT. Chunk size
		// is derived from the real column count so bound params stay ≤ 100
		// (D1 cap).
		customs := make([]map[string]any, len(clean))
		keyOrders := make([][]string, len(clean))
		for i, c := range clean {
			customs[i], keyOrders[i] = c.custom, c.customKeys
		}
		keys, defByKey, err := ResolveImportCustomColumnsInOrder(ctx, Contact, customs, keyOrders)
		if err != nil {
			return 0, nil, err
		}
		cols := []string{"id", "first_name", "last_name", "email", "phone", "company_id", "title", "status"}
		for _, k := range keys {
			cols = append(cols, QuoteIdent(k))
		}
		imported, err := importInsert(ctx, "contacts", cols, len(clean), func(i int) []any {
			c := clean[i]
			var companyID any // null
			if c.company != "" {
				companyID = companyIDs[jsLower(c.company)]
			} else if c.inferDomain != "" {
				companyID = companyIDByDomain[c.inferDomain]
			}
			params := []any{js.UUID(), c.firstName, c.lastName, c.email, c.phone, companyID, c.title, c.status}
			for _, k := range keys {
				params = append(params, importCell(c.custom, k, defByKey[k], c.order))
			}
			return params
		})
		if err != nil {
			return 0, nil, err
		}
		return 200, importedContactsBody{imported, companiesCreated, skipped}, nil
	})
}

// importInsert inserts n rows into table, as many to a statement as keep the
// bound params ≤ 100 (D1's cap); row(i) is row i's params, in cols' order.
// It answers how many rows went in.
func importInsert(ctx context.Context, table string, cols []string, n int, row func(i int) []any) (int, error) {
	rowsPerStmt := max(1, 100/len(cols))
	rowPlaceholder := "(" + marks(len(cols)) + ")"
	indexes := make([]int, n)
	for i := range indexes {
		indexes[i] = i
	}
	imported := 0
	for _, group := range Chunk(indexes, rowsPerStmt) {
		placeholders := strings.TrimSuffix(strings.Repeat(rowPlaceholder+", ", len(group)), ", ")
		var params []any
		for _, i := range group {
			params = append(params, row(i)...)
		}
		if _, err := db.Run(ctx, fmt.Sprintf("INSERT INTO %s (%s) VALUES %s", table, strings.Join(cols, ", "), placeholders), params...); err != nil {
			return imported, err
		}
		imported += len(group)
	}
	return imported, nil
}

type importedCompaniesBody struct {
	Imported   int `json:"imported"`
	Skipped    int `json:"skipped"`
	Duplicates int `json:"duplicates"`
}

// importCompany is one company row to import.
type importCompany struct {
	name, domain, industry, phone, email, notes string
	custom                                      map[string]any
	customKeys                                  []string
	order                                       *keyOrder
}

// importCompanies is the bulk company import (CSV / XLSX). Dedupe by name
// (case-insensitive): a company whose name already exists is skipped, never
// duplicated or overwritten. New companies land with their built-in columns +
// any mapped custom fields.
func (app *App) importCompanies(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		_, rows, orders, err := importBody(r, "companies")
		if err != nil {
			return 0, nil, err
		}
		if len(rows) == 0 {
			return fail(400, "No rows to import")
		}
		if len(rows) > 2000 {
			return fail(400, "Import is limited to 2000 rows at a time")
		}

		// Keep only rows with a name; collapse to the first-seen row per name
		// so a duplicated name in the file resolves to one company (first wins).
		byKey := map[string]*importCompany{}
		var keys []string // the Map's insertion order
		named := 0
		for i, raw := range rows {
			if raw == nil {
				return 0, nil, readNull("name")
			}
			row, _ := raw.(map[string]any)
			name, err := rowText(row, "name")
			if err != nil {
				return 0, nil, err
			}
			if name == "" {
				continue
			}
			named++ // rows.filter((r) => (r.name || "").trim()).length
			key := jsLower(name)
			if byKey[key] != nil {
				continue
			}
			f, err := rowTexts(row, "domain", "industry", "phone", "email", "notes")
			if err != nil {
				return 0, nil, err
			}
			co := &importCompany{name: name, domain: f[0], industry: f[1], phone: f[2], email: f[3], notes: f[4], order: orders.item(i)}
			co.custom, co.customKeys = rowCustom(row, co.order)
			byKey[key] = co
			keys = append(keys, key)
		}
		noName := len(rows) - named          // rows with no company name at all
		fileDuplicates := named - len(byKey) // same name repeated within the file
		if len(byKey) == 0 {
			return fail(400, "No rows had a company name to import")
		}

		// Which names already exist → skip those (dedupe).
		existing := map[string]bool{}
		names := make([]string, len(keys))
		for i, k := range keys {
			names[i] = byKey[k].name
		}
		for _, group := range Chunk(names, lookupChunk) {
			found, err := db.Query(ctx, fmt.Sprintf("SELECT name FROM companies WHERE name COLLATE NOCASE IN (%s)", marks(len(group))), stringParams(group)...)
			if err != nil {
				return 0, nil, err
			}
			for _, co := range found {
				existing[jsLower(colText(co["name"]))] = true
			}
		}
		var fresh []*importCompany
		for _, k := range keys {
			if !existing[k] {
				fresh = append(fresh, byKey[k])
			}
		}
		// Duplicates = names repeated within the file + names that already exist.
		duplicates := fileDuplicates + (len(byKey) - len(fresh))

		// Bulk-insert new companies + mapped custom columns (chunk from col count).
		customs := make([]map[string]any, len(fresh))
		keyOrders := make([][]string, len(fresh))
		for i, co := range fresh {
			customs[i], keyOrders[i] = co.custom, co.customKeys
		}
		customKeys, defByKey, err := ResolveImportCustomColumnsInOrder(ctx, Company, customs, keyOrders)
		if err != nil {
			return 0, nil, err
		}
		cols := []string{"id", "name", "domain", "industry", "phone", "email", "notes"}
		for _, k := range customKeys {
			cols = append(cols, QuoteIdent(k))
		}
		imported, err := importInsert(ctx, "companies", cols, len(fresh), func(i int) []any {
			co := fresh[i]
			params := []any{js.UUID(), co.name, co.domain, co.industry, co.phone, co.email, co.notes}
			for _, k := range customKeys {
				params = append(params, importCell(co.custom, k, defByKey[k], co.order))
			}
			return params
		})
		if err != nil {
			return 0, nil, err
		}
		return 200, importedCompaniesBody{imported, noName, duplicates}, nil
	})
}
