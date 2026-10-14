package crm

// Gmail sync rules: parsing the addresses on a message, which senders count as
// group mail, which people may become contacts, and the Gmail searches a sync
// runs. No I/O here, so every decision can be checked without a mailbox.

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"opencrm/internal/js"
)

// Address is one party on a message.
type Address struct {
	Email string  `json:"email"` // lower-cased
	Name  *string `json:"name"`
}

// AutoCreate is the policy for which message parties become contacts:
// "none" | "sent" | "sent_and_received".
type AutoCreate string

// History is how far back the first import reaches: "3m" | "12m" | "all".
type History string

// wsClass is JavaScript's \s: Go's \s lacks \v and the Unicode spaces.
const wsClass = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

// dotClass is JavaScript's `.`: any character but a line terminator.
const dotClass = `[^\n\r\x{2028}\x{2029}]`

var (
	angledPart  = regexp.MustCompile(`^(` + dotClass + `*)<([^>]*)>` + wsClass + `*$`)
	mailtoLead  = regexp.MustCompile(`(?i)^mailto:`)
	quotedName  = regexp.MustCompile(`^"(` + dotClass + `*)"$`)
	groupLocal  = regexp.MustCompile(`no-?reply|do-?not-?reply|^bounces?[+-]?|^notifications?[+-]`)
	leadingAt   = regexp.MustCompile(`^@`)
	labelSpaces = regexp.MustCompile(`[/\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)
)

// ParseAddresses reads "Ada Lovelace <ADA@x.com>, bob@y.com" as
// [{ ada@x.com, "Ada Lovelace" }, { bob@y.com }].
// Splits on commas outside quotes and angle brackets; drops anything without an @.
func ParseAddresses(header string) []Address {
	out := []Address{}
	if header == "" {
		return out
	}
	var parts []string
	var current strings.Builder
	quoted, angled := false, false
	for _, ch := range header {
		if ch == '"' {
			quoted = !quoted
		} else if ch == '<' && !quoted {
			angled = true
		} else if ch == '>' && !quoted {
			angled = false
		}
		if ch == ',' && !quoted && !angled {
			parts = append(parts, current.String())
			current.Reset()
		} else {
			current.WriteRune(ch)
		}
	}
	parts = append(parts, current.String())

	seen := map[string]bool{}
	for _, raw := range parts {
		part := js.Trim(raw)
		if part == "" {
			continue
		}
		m := angledPart.FindStringSubmatch(part)
		target := part
		if m != nil {
			target = m[2]
		}
		email := strings.ToLower(mailtoLead.ReplaceAllString(js.Trim(target), ""))
		if !strings.Contains(email, "@") || seen[email] {
			continue
		}
		seen[email] = true
		name := ""
		if m != nil {
			name = js.Trim(quotedName.ReplaceAllString(js.Trim(m[1]), "$1"))
		}
		a := Address{Email: email}
		if name != "" && strings.ToLower(name) != email {
			n := name
			a.Name = &n
		}
		out = append(out, a)
	}
	return out
}

// DomainOf is the lower-cased part after the last @, or "".
func DomainOf(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// localPart is email.slice(0, email.lastIndexOf("@")). With no @, lastIndexOf
// is -1 and slice(0, -1) drops the last character; that is kept.
func localPart(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		r := []rune(email)
		if len(r) == 0 {
			return ""
		}
		return string(r[:len(r)-1])
	}
	return email[:at]
}

// Shared inboxes and robots, by local part: mail from these is not a person
// writing to you, and none of them should become a contact.
var groupLocalParts = map[string]bool{}

func init() {
	for _, p := range strings.Fields(`team support help info hello hi contact sales marketing
  billing invoice invoices accounts admin office hr jobs careers
  press feedback service news newsletter updates alerts notification
  notifications mailer-daemon postmaster reply`) {
		groupLocalParts[p] = true
	}
}

// IsGroupAddress is team@, support@, noreply@… (the "Exclude group emails" option).
func IsGroupAddress(email string) bool {
	local := strings.ToLower(localPart(email))
	if groupLocalParts[local] {
		return true
	}
	return groupLocal.MatchString(local)
}

// NormaliseBlocklist reads blocklist entries, which are addresses
// ("ada@x.com") or domains ("@x.com" or "x.com"). Normalised to lower case,
// blanks dropped.
func NormaliseBlocklist(entries any) []string {
	list, ok := entries.([]any)
	out := []string{}
	if !ok {
		return out
	}
	seen := map[string]bool{}
	for _, e := range list {
		s, ok := e.(string)
		if !ok {
			continue
		}
		v := strings.ToLower(js.Trim(s))
		if v == "" {
			continue
		}
		if !(strings.Contains(v, "@") && !strings.HasPrefix(v, "@")) {
			v = "@" + leadingAt.ReplaceAllString(v, "")
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// IsBlocked is whether the address, or its whole domain, is on the blocklist.
func IsBlocked(email string, blocklist []string) bool {
	domain := "@" + DomainOf(email)
	for _, b := range blocklist {
		if b == email || b == domain {
			return true
		}
	}
	return false
}

// CreateRules decide which addresses may become contacts.
type CreateRules struct {
	Mailbox         string
	Policy          AutoCreate
	ExcludeGroup    bool
	ExcludePersonal bool
	Blocklist       []string
	// IsPersonalDomain is whether a domain is a personal provider (gmail.com, outlook.com…).
	IsPersonalDomain func(domain string) bool `json:"-"`
}

// MayCreate is whether `address` may become a contact under these rules (policy aside).
func MayCreate(address Address, rules CreateRules) bool {
	email := address.Email
	domain := DomainOf(email)
	if domain == "" || email == rules.Mailbox {
		return false
	}
	if IsBlocked(email, rules.Blocklist) {
		return false
	}
	if rules.ExcludeGroup && IsGroupAddress(email) {
		return false
	}
	if rules.ExcludePersonal && rules.IsPersonalDomain(domain) {
		return false
	}
	// Colleagues: the mailbox's own work domain is the team, not its contacts.
	// A personal mailbox (gmail.com) has no team domain to skip.
	ownDomain := DomainOf(rules.Mailbox)
	if domain == ownDomain && !rules.IsPersonalDomain(ownDomain) {
		return false
	}
	return true
}

// BulkCategories are Gmail's own tabs for bulk mail; a received message filed
// there is not someone writing to you, so its sender never becomes a contact.
var BulkCategories = []string{"CATEGORY_PROMOTIONS", "CATEGORY_SOCIAL", "CATEGORY_UPDATES", "CATEGORY_FORUMS"}

// CandidateMessage is what CreationCandidates reads of a message. Direction is
// "sent" or "received".
type CandidateMessage struct {
	Direction string
	From      *Address
	To        []Address
	LabelIDs  []string
}

// CreationCandidates are the people a message would add as contacts under the
// policy: recipients of mail you sent, and (for "sent and received") senders of
// mail you received.
func CreationCandidates(msg CandidateMessage, rules CreateRules) []Address {
	out := []Address{}
	if rules.Policy == "none" {
		return out
	}
	var people []Address
	if msg.Direction == "sent" {
		people = msg.To
	} else if rules.Policy == "sent_and_received" && msg.From != nil && !hasBulkLabel(msg.LabelIDs) {
		people = []Address{*msg.From}
	}
	for _, p := range people {
		if MayCreate(p, rules) {
			out = append(out, p)
		}
	}
	return out
}

func hasBulkLabel(labels []string) bool {
	for _, l := range labels {
		for _, b := range BulkCategories {
			if l == b {
				return true
			}
		}
	}
	return false
}

// PersonName is a first and last name.
type PersonName struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

// SplitName reads "Ada Lovelace" as Ada / Lovelace; "Lovelace, Ada" as Ada /
// Lovelace; none as the address's local part.
func SplitName(address Address) PersonName {
	name := ""
	if address.Name != nil {
		name = js.Trim(*address.Name)
	}
	if name != "" && !strings.Contains(name, "@") {
		if strings.Contains(name, ",") {
			// split(",", 2) keeps the first two pieces and drops any others.
			pieces := strings.SplitN(name, ",", 3)
			last, first := js.Trim(pieces[0]), js.Trim(pieces[1])
			if first != "" {
				return PersonName{First: first, Last: last}
			}
		}
		fields := runOfSpace.Split(name, -1) // name.split(/\s+/)
		return PersonName{First: fields[0], Last: strings.Join(fields[1:], " ")}
	}
	return PersonName{First: localPart(address.Email)}
}

// gmailDate is a date as Gmail's after: reads it (YYYY/MM/DD, midnight
// Pacific): a day is precision enough for the first import's reach.
func gmailDate(d time.Time) string {
	return d.UTC().Format("2006/01/02")
}

// HistoryStart is the first import's reach: nil means all of it.
func HistoryStart(history History, now time.Time) *time.Time {
	if history == "all" {
		return nil
	}
	days := int64(365)
	if history == "3m" {
		days = 90
	}
	t := time.UnixMilli(now.UnixMilli() - days*86_400_000).UTC()
	return &t
}

// labelTerm is a Gmail label name as label: search takes it ("Clients 2024" → clients-2024).
func labelTerm(name string) string {
	return "label:" + labelSpaces.ReplaceAllString(strings.ToLower(js.Trim(name)), "-")
}

// Scope is which mail a sync reads.
type Scope struct {
	Labels []string   `json:"labels"` // label names; empty means all mail
	Since  *time.Time `json:"since"`
}

// ScopeTerms are the parts every sync search shares: the labels in scope and
// how far back. Chats never count.
func ScopeTerms(scope Scope) []string {
	terms := []string{"-in:chats"}
	if len(scope.Labels) == 1 {
		terms = append(terms, labelTerm(scope.Labels[0]))
	} else if len(scope.Labels) > 1 {
		parts := make([]string, len(scope.Labels))
		for i, l := range scope.Labels {
			parts[i] = labelTerm(l)
		}
		terms = append(terms, "{"+strings.Join(parts, " ")+"}")
	}
	if scope.Since != nil {
		terms = append(terms, "after:"+gmailDate(*scope.Since))
	}
	return terms
}

// SentQuery is mail you sent: where "sent" contacts come from.
func SentQuery(scope Scope) string {
	return strings.Join(append([]string{"in:sent"}, ScopeTerms(scope)...), " ")
}

// ReceivedQuery is mail you received, minus Gmail's bulk tabs: where "received" contacts come from.
func ReceivedQuery(scope Scope) string {
	terms := []string{"-in:sent", "-category:promotions", "-category:social", "-category:updates", "-category:forums"}
	return strings.Join(append(terms, ScopeTerms(scope)...), " ")
}

// PeopleQuery is every message from or to any of these addresses. Gmail's {a b} means a OR b.
func PeopleQuery(emails []string, scope Scope) string {
	either := make([]string, 0, len(emails)*2)
	for _, e := range emails {
		either = append(either, "from:"+e, "to:"+e)
	}
	return strings.Join(append([]string{"{" + strings.Join(either, " ") + "}"}, ScopeTerms(scope)...), " ")
}

// LiveOverlapMs is how far back each live read reaches before the last sync:
// for mail that becomes searchable late.
const LiveOverlapMs = 60 * 60_000

// LabelOverlapMs is the same when only labels sync: mail enters a label when
// someone labels it, often hours after it arrived.
const LabelOverlapMs = 86_400_000

// SinceQuery is new mail since the last sync. after: takes Unix seconds as
// well as dates (developers.google.com/workspace/gmail/api/guides/filtering),
// so a live read of all mail reaches back an hour, not the whole day a date
// would.
func SinceQuery(syncedUntil time.Time, scope Scope) string {
	overlap := int64(LiveOverlapMs)
	if len(scope.Labels) > 0 {
		overlap = LabelOverlapMs
	}
	floor := syncedUntil.UnixMilli() - overlap
	if scope.Since != nil && scope.Since.UnixMilli() > floor {
		floor = scope.Since.UnixMilli()
	}
	scope.Since = nil
	terms := append(ScopeTerms(scope), fmt.Sprintf("after:%d", floorDiv(floor, 1000)))
	return strings.Join(terms, " ")
}
