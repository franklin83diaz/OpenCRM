package crm

// What the CRM knows about an email address, for whoever is about to write to
// it: the contact, their company (and whether it is a customer), its deals, and
// its calls. An app next door, such as a sequencer, reads it before an email
// goes out, so nobody cold-emails a customer or a company mid-deal; and for a
// batch of people before they join a campaign. Read only. Every read is by
// set, so a batch of 100 costs a few dozen queries, not a few per address.
//
// TS named its answer Lookup, like the function; Go has one namespace, so the
// answer is LookupResult and an address to look up is LookupAddress.

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/js"
)

// LookupDeal is one of the deals a lookup finds.
type LookupDeal struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Stage      string `json:"stage"`
	StageLabel string `json:"stage_label"`
	// State is "open", "won" or "lost", from the stage's flags; a stage not in the pipeline is open.
	State     string `json:"state"`
	Value     any    `json:"value"` // COALESCE(d.value, 0): a number as stored
	CloseDate string `json:"close_date"`
}

// LookupContact is the contact with exactly the address asked.
type LookupContact struct {
	ID        string `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Title     string `json:"title"`
	Status    string `json:"status"`
}

// LookupCompany is the company a lookup finds.
type LookupCompany struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Domain        string  `json:"domain"`
	CustomerSince *string `json:"customer_since"`
	RenewalDate   *string `json:"renewal_date"`
}

// LookupResult is what the CRM knows about one address (TS: Lookup).
type LookupResult struct {
	Contact *LookupContact `json:"contact"`
	Company *LookupCompany `json:"company"`
	// Deals are the company's deals and the contact's, open first, then the newest. At most 10.
	Deals []LookupDeal `json:"deals"`
	// LastCallAt is the latest call with the company: a synced meeting, or a call or meeting logged by hand.
	LastCallAt  *string        `json:"last_call_at"`
	NextMeeting *BookedMeeting `json:"next_meeting"`
}

// LookupAddress is one address to look up, and the company's domain when the
// address alone doesn't say it (TS: Address).
type LookupAddress struct {
	Email  string
	Domain string
}

// pathTail is /\/.*$/: from a slash to the end (`.` stops at a line break).
var pathTail = regexp.MustCompile(`/` + dotClass + `*$`)

// BareDomain is a company domain as stored or typed ("https://www.Acme.com/")
// reduced to "acme.com"; "" for a personal provider.
func BareDomain(raw string) string {
	d := strings.ToLower(js.Trim(raw))
	d = httpLead.ReplaceAllString(d, "")
	d = wwwLead.ReplaceAllString(d, "")
	d = pathTail.ReplaceAllString(d, "")
	if !strings.Contains(d, ".") || FreemailDomains[d] {
		return ""
	}
	return d
}

// domainSQL is the same reduction in SQL, for the stored `companies.domain`
// (as FindOrCreateCompanyByDomain does).
const domainSQL = "lower(replace(replace(replace(rtrim(domain,'/'),'https://',''),'http://',''),'www.',''))"

const lookupCompanySQL = `SELECT id, name, COALESCE(domain, '') AS domain, customer_since, renewal_date, updated_at, ` + domainSQL + ` AS bare FROM companies`
const lookupDealSQL = `SELECT d.id, d.name, d.stage, COALESCE(s.label, d.stage) AS stage_label, s.is_won, s.is_lost,
       COALESCE(d.value, 0) AS value, COALESCE(d.close_date, '') AS close_date, d.company_id, d.contact_id, d.updated_at
  FROM deals d LEFT JOIN stages s ON s.key = d.stage`

type lookupContactRow struct {
	LookupContact
	companyID *string
}

type lookupCompanyRow struct {
	row       db.Row
	id, bare  string
	updatedAt any
}

// customer is whether the company has a customer_since.
func (c *lookupCompanyRow) customer() bool {
	s, ok := c.row["customer_since"].(string)
	return ok && js.Trim(s) != ""
}

// laterStamp is `a > b` for two updated_at values, as JS compared them: strings
// by their characters, anything else (null) as numbers, where NaN is never
// greater.
func laterStamp(a, b any) bool {
	sa, aok := a.(string)
	sb, bok := b.(string)
	if aok && bok {
		return sa > sb
	}
	return js.Number(a) > js.Number(b)
}

// trimmedOrNil is `v?.trim() || null`.
func trimmedOrNil(v any) *string {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	if s = js.Trim(s); s == "" {
		return nil
	}
	return &s
}

// LookupMany is, for each address, in order: the contact with exactly that
// address; their company, else the one whose domain is the address's
// `domain` (else the address's own, when it is a work address); its and the
// contact's deals; and its calls. Of several companies on one domain, a
// customer wins, then the latest updated.
func LookupMany(ctx context.Context, addresses []LookupAddress, now time.Time) ([]LookupResult, error) {
	type ask struct{ email, site string }
	asked := make([]ask, len(addresses))
	for i, a := range addresses {
		email := strings.ToLower(js.Trim(a.Email))
		site := BareDomain(a.Domain)
		if site == "" {
			site = BareDomain(email[strings.LastIndex(email, "@")+1:])
		}
		asked[i] = ask{email, site}
	}

	emails, seen := []string{}, map[string]bool{}
	for _, a := range asked {
		if a.email != "" {
			emails = appendNew(emails, seen, a.email)
		}
	}
	rows, err := InChunks(ctx, emails, func(m string) string {
		return `SELECT id, first_name, COALESCE(last_name, '') AS last_name, COALESCE(title, '') AS title, status, company_id, lower(trim(email)) AS email
       FROM contacts WHERE lower(trim(email)) IN (` + m + `) ORDER BY updated_at DESC`
	})
	if err != nil {
		return nil, err
	}
	contactOf := map[string]*lookupContactRow{}
	var contactOrder []string // the Map's insertion order
	for _, c := range rows {
		email := colText(c["email"])
		if _, ok := contactOf[email]; ok {
			continue
		}
		contactOf[email] = &lookupContactRow{
			LookupContact: LookupContact{ID: colText(c["id"]), FirstName: colText(c["first_name"]), LastName: colText(c["last_name"]),
				Title: colText(c["title"]), Status: colText(c["status"])},
			companyID: textOf(c["company_id"]),
		}
		contactOrder = append(contactOrder, email)
	}

	ownIDs, seen := []string{}, map[string]bool{}
	for _, email := range contactOrder {
		if id := contactOf[email].companyID; hasText(id) {
			ownIDs = appendNew(ownIDs, seen, *id)
		}
	}
	companyOf := func(r db.Row) *lookupCompanyRow {
		return &lookupCompanyRow{row: r, id: colText(r["id"]), bare: colText(r["bare"]), updatedAt: r["updated_at"]}
	}
	rows, err = InChunks(ctx, ownIDs, func(m string) string { return lookupCompanySQL + ` WHERE id IN (` + m + `)` })
	if err != nil {
		return nil, err
	}
	byID := map[string]*lookupCompanyRow{}
	for _, r := range rows {
		c := companyOf(r)
		byID[c.id] = c
	}
	ownCompany := func(email string) *lookupCompanyRow {
		if email == "" {
			return nil
		}
		c := contactOf[email]
		if c == nil || !hasText(c.companyID) {
			return nil
		}
		return byID[*c.companyID]
	}
	sites, seen := []string{}, map[string]bool{}
	for _, a := range asked {
		if a.site != "" && ownCompany(a.email) == nil {
			sites = appendNew(sites, seen, a.site)
		}
	}
	rows, err = InChunks(ctx, sites, func(m string) string { return lookupCompanySQL + ` WHERE ` + domainSQL + ` IN (` + m + `)` })
	if err != nil {
		return nil, err
	}
	onSite := map[string]*lookupCompanyRow{}
	for _, r := range rows {
		c := companyOf(r)
		best := onSite[c.bare]
		if best == nil {
			onSite[c.bare] = c
		} else if c.customer() != best.customer() {
			if c.customer() {
				onSite[c.bare] = c
			}
		} else if laterStamp(c.updatedAt, best.updatedAt) {
			onSite[c.bare] = c
		}
	}

	type pair struct {
		contact *lookupContactRow
		company *lookupCompanyRow
	}
	found := make([]pair, len(asked))
	companyIDs, contactIDs := []string{}, []string{}
	seenCompany, seenContact := map[string]bool{}, map[string]bool{}
	for i, a := range asked {
		var f pair
		if a.email != "" {
			f.contact = contactOf[a.email]
		}
		f.company = ownCompany(a.email)
		if f.company == nil && a.site != "" {
			f.company = onSite[a.site]
		}
		found[i] = f
	}
	for _, f := range found {
		if f.company != nil && f.company.id != "" {
			companyIDs = appendNew(companyIDs, seenCompany, f.company.id)
		}
	}
	for _, f := range found {
		if f.contact != nil && f.contact.ID != "" {
			contactIDs = appendNew(contactIDs, seenContact, f.contact.ID)
		}
	}

	// The deals, by id, in the order first read (a Map: setting a key again
	// keeps its place).
	deals := map[string]db.Row{}
	var dealOrder []string
	for _, q := range []struct {
		ids []string
		col string
	}{{companyIDs, "d.company_id"}, {contactIDs, "d.contact_id"}} {
		rows, err := InChunks(ctx, q.ids, func(m string) string { return lookupDealSQL + ` WHERE ` + q.col + ` IN (` + m + `)` })
		if err != nil {
			return nil, err
		}
		for _, d := range rows {
			id := colText(d["id"])
			if _, ok := deals[id]; !ok {
				dealOrder = append(dealOrder, id)
			}
			deals[id] = d
		}
	}

	// Calls logged by hand count too, but not emails: an app that writes here
	// logs its own sends as emails, and those are not a relationship.
	logged := map[string]string{}
	for _, sql := range []func(m string) string{
		func(m string) string {
			return `SELECT a.entity_id AS company_id, ` + isoMax + ` AS at FROM activities a
    WHERE a.entity_type = 'company' AND a.type IN ('call', 'meeting') AND a.entity_id IN (` + m + `) GROUP BY a.entity_id`
		},
		func(m string) string {
			return `SELECT c.company_id, ` + isoMax + ` AS at FROM activities a JOIN contacts c ON c.id = a.entity_id
    WHERE a.entity_type = 'contact' AND a.type IN ('call', 'meeting') AND c.company_id IN (` + m + `) GROUP BY c.company_id`
		},
		func(m string) string {
			return `SELECT d.company_id, ` + isoMax + ` AS at FROM activities a JOIN deals d ON d.id = a.entity_id
    WHERE a.entity_type = 'deal' AND a.type IN ('call', 'meeting') AND d.company_id IN (` + m + `) GROUP BY d.company_id`
		},
	} {
		rows, err := InChunks(ctx, companyIDs, sql)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			at, _ := r["at"].(string)
			id := colText(r["company_id"])
			if at != "" && at > logged[id] {
				logged[id] = at
			}
		}
	}
	var accounts *Accounts
	if len(companyIDs) > 0 {
		if accounts, err = ReadAccounts(ctx, companyIDs, now); err != nil {
			return nil, err
		}
	}

	isClosed := func(d db.Row) bool { return js.Truthy(d["is_won"]) || js.Truthy(d["is_lost"]) }
	out := make([]LookupResult, len(found))
	for i, f := range found {
		var account *AccountRead
		if f.company != nil && accounts != nil {
			a := accounts.Of(f.company.id)
			account = &a
		}
		var theirs []db.Row
		for _, id := range dealOrder {
			d := deals[id]
			if (f.company != nil && d["company_id"] == any(f.company.id)) || (f.contact != nil && d["contact_id"] == any(f.contact.ID)) {
				theirs = append(theirs, d)
			}
		}
		updated := func(d db.Row) string {
			s, _ := d["updated_at"].(string) // null ?? ""
			return s
		}
		sort.SliceStable(theirs, func(i, j int) bool {
			a, b := theirs[i], theirs[j]
			if ca, cb := isClosed(a), isClosed(b); ca != cb {
				return !ca
			}
			return localeCompare(updated(b), updated(a)) < 0
		})
		if len(theirs) > 10 {
			theirs = theirs[:10]
		}
		r := LookupResult{Deals: []LookupDeal{}}
		if f.contact != nil {
			c := f.contact.LookupContact
			r.Contact = &c
		}
		if f.company != nil {
			c := f.company.row
			r.Company = &LookupCompany{ID: f.company.id, Name: colText(c["name"]), Domain: colText(c["domain"]),
				CustomerSince: trimmedOrNil(c["customer_since"]), RenewalDate: trimmedOrNil(c["renewal_date"])}
		}
		for _, d := range theirs {
			state := "open"
			if js.Truthy(d["is_won"]) {
				state = "won"
			} else if js.Truthy(d["is_lost"]) {
				state = "lost"
			}
			r.Deals = append(r.Deals, LookupDeal{ID: colText(d["id"]), Name: colText(d["name"]), Stage: colText(d["stage"]),
				StageLabel: colText(d["stage_label"]), State: state, Value: d["value"], CloseDate: colText(d["close_date"])})
		}
		// The later of the last synced meeting and the last call logged by hand.
		if account != nil && hasText(account.LastMeetingAt) {
			r.LastCallAt = account.LastMeetingAt
		}
		if f.company != nil {
			if at := logged[f.company.id]; at != "" && (r.LastCallAt == nil || at > *r.LastCallAt) {
				r.LastCallAt = &at
			}
		}
		if account != nil {
			r.NextMeeting = account.NextMeeting
		}
		out[i] = r
	}
	return out, nil
}

// Lookup is what the CRM knows about one address: LookupMany for one.
func Lookup(ctx context.Context, email, domain string, now time.Time) (LookupResult, error) {
	out, err := LookupMany(ctx, []LookupAddress{{Email: email, Domain: domain}}, now)
	if err != nil {
		return LookupResult{}, err
	}
	return out[0], nil
}
