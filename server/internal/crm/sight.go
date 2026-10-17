package crm

// What the CRM can see of a customer's mail and calls. The email and meeting
// syncs that filled email_accounts and meeting_sync are gone (no
// integrations), but the tables stay in the schema, and the health rules read
// their settings rows to say what the CRM can't see. These are those reads,
// read-only, with the SQL the syncs used.

import (
	"context"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// EmailAccount is what the health rules read of an email_accounts row.
type EmailAccount struct {
	Mailbox   string
	Enabled   float64
	Blocklist string
	Phase     string // "idle" | "importing" | "live"
	LastError *string
}

func accountOf(row db.Row) *EmailAccount {
	if row == nil {
		return nil
	}
	return &EmailAccount{
		Mailbox:   colText(row["mailbox"]),
		Enabled:   js.Number(row["enabled"]),
		Blocklist: colText(row["blocklist"]),
		Phase:     colText(row["phase"]),
		LastError: nullableText(row["last_error"]),
	}
}

// CurrentAccount is the mailbox being synced. v1 synced one mailbox at a
// time, the org's default connection; the table allows more. The enabled one
// wins, then the most recently changed. nil when there is none.
func CurrentAccount(ctx context.Context) (*EmailAccount, error) {
	row, err := db.Get(ctx, "SELECT * FROM email_accounts ORDER BY enabled DESC, updated_at DESC LIMIT 1")
	if err != nil {
		return nil, err
	}
	return accountOf(row), nil
}

// BlocklistOf is the addresses and @domains this mailbox never imports.
func BlocklistOf(a *EmailAccount) []string {
	return NormaliseBlocklist(jsonArray(&a.Blocklist))
}

// jsonArray is JSON.parse(v ?? "[]") when that is an array, else [].
func jsonArray(v *string) []any {
	s := "[]"
	if v != nil {
		s = *v
	}
	parsed, err := web.ParseJSON(s)
	if arr, ok := parsed.([]any); ok && err == nil {
		return arr
	}
	return []any{}
}

// MeetingSync is what the health rules read of the meeting_sync row.
type MeetingSync struct {
	Enabled   float64
	Phase     string // "idle" | "importing" | "live"
	LastError *string
}

func meetingSyncOf(row db.Row) *MeetingSync {
	if row == nil {
		return nil
	}
	return &MeetingSync{
		Enabled:   js.Number(row["enabled"]),
		Phase:     colText(row["phase"]),
		LastError: nullableText(row["last_error"]),
	}
}

// SyncSettings is the meeting sync's settings row, nil when it was never saved.
func SyncSettings(ctx context.Context) (*MeetingSync, error) {
	row, err := db.Get(ctx, "SELECT * FROM meeting_sync WHERE id = 1")
	if err != nil {
		return nil, err
	}
	return meetingSyncOf(row), nil
}
