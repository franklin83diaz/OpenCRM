package crm

// Clawnify integrations — one place that wraps the connections client so the
// rest of the app never thinks about credentials or brokers. Every capability
// routes through Connections.Run(service, ACTION, args): the platform brokers
// the org's connection and executes the managed action. Off-platform (local
// dev) nothing can broker, so nothing reads as connected and the UI disables
// the buttons instead of failing.
//
// Action slugs + argument shapes verified against docs.composio.dev/toolkits/*.
// Keeping the (service, action) pairs here means a Composio rename is a
// one-line edit, not a hunt across the codebase.

import (
	"context"
	"errors"
	"math"
	"strings"

	"opencrm/internal/clawnify"
)

// Services are the canonical service ids (never invent these — they come from
// the Clawnify connections catalog). Mail goes through Gmail and meetings
// through Google Calendar; Google Workspace (googlesuper) covers both, and
// stands in for whichever of the two the org hasn't connected. slack = Slack.
// All Composio-managed, except Granola (call notes and transcripts): an API
// key the platform holds, reached with Connections.Get on Granola's own API.
var Services = struct {
	Email, Meeting, Google, Slack, Notes string
}{
	Email:   "gmail",
	Meeting: "googlecalendar",
	Google:  "googlesuper",
	Slack:   "slack",
	Notes:   "granola",
}

// IntegrationStatus is which integrations the org has connected (TS's
// ConnectionStatus interface; the function keeps that name).
type IntegrationStatus struct {
	Email   bool `json:"email"`
	Meeting bool `json:"meeting"`
	Slack   bool `json:"slack"`
	Notes   bool `json:"notes"`
}

// GoogleConnection is a connected Google service. Composio names every action
// <TOOLKIT>_<ACTION>, and Google Workspace carries Gmail's and Calendar's
// actions under the same names with the same arguments (GMAIL_FETCH_EMAILS =
// GOOGLESUPER_FETCH_EMAILS), so callers name the action without its toolkit:
// mail.Run(ctx, "FETCH_EMAILS", …).
type GoogleConnection struct {
	Service string
	conn    *clawnify.Connections
}

// Run executes <TOOLKIT>_<action> on the connection's service.
func (g *GoogleConnection) Run(ctx context.Context, action string, args map[string]any) (any, error) {
	return g.conn.Run(ctx, g.Service, strings.ToUpper(g.Service)+"_"+action, args)
}

// connectedServices is the services the org has connected, in one call to the
// broker. A broker that cannot be reached reads as nothing connected.
func connectedServices(ctx context.Context, conn *clawnify.Connections) map[string]bool {
	set := map[string]bool{}
	for _, s := range conn.Connected(ctx) {
		switch s {
		case Services.Email, Services.Meeting, Services.Google, Services.Slack, Services.Notes:
			set[s] = true
		}
	}
	return set
}

// serviceFor is the service that does own's job: itself when connected, else
// Google Workspace; "" when neither is.
func serviceFor(connected map[string]bool, own string) string {
	if connected[own] {
		return own
	}
	if connected[Services.Google] {
		return Services.Google
	}
	return ""
}

func google(conn *clawnify.Connections, connected map[string]bool, own string) *GoogleConnection {
	service := serviceFor(connected, own)
	if service == "" {
		return nil
	}
	return &GoogleConnection{Service: service, conn: conn}
}

// ConnectionStatus is which integrations the org has connected right now
// (drives UI enable/disable).
func ConnectionStatus(ctx context.Context, conn *clawnify.Connections) IntegrationStatus {
	connected := connectedServices(ctx, conn)
	return IntegrationStatus{
		Email:   serviceFor(connected, Services.Email) != "",
		Meeting: serviceFor(connected, Services.Meeting) != "",
		Slack:   connected[Services.Slack],
		Notes:   connected[Services.Notes],
	}
}

// MailConnection is the org's mail: Gmail, or Google Workspace when Gmail
// isn't connected; nil when neither is.
func MailConnection(ctx context.Context, conn *clawnify.Connections) *GoogleConnection {
	return google(conn, connectedServices(ctx, conn), Services.Email)
}

// CalendarConnection is the org's calendar: Google Calendar, or Google
// Workspace when Calendar isn't connected; nil when neither is.
func CalendarConnection(ctx context.Context, conn *clawnify.Connections) *GoogleConnection {
	return google(conn, connectedServices(ctx, conn), Services.Meeting)
}

// NotesClient is Granola's API with the org's key.
type NotesClient struct {
	conn *clawnify.Connections
}

// Get calls GET endpoint on Granola's API and returns the parsed answer.
func (n *NotesClient) Get(ctx context.Context, endpoint string, query map[string]string) (any, error) {
	return n.conn.Get(ctx, Services.Notes, endpoint, query)
}

// NotesConnection is Granola's API with the org's key, or nil when Granola
// isn't connected.
func NotesConnection(ctx context.Context, conn *clawnify.Connections) *NotesClient {
	if !connectedServices(ctx, conn)[Services.Notes] {
		return nil
	}
	return &NotesClient{conn: conn}
}

// Recipients is who an email goes to. To holds at least one address.
type Recipients struct {
	To  []string
	Cc  []string
	Bcc []string
}

func mail(ctx context.Context, conn *clawnify.Connections) (*GoogleConnection, error) {
	m := MailConnection(ctx, conn)
	if m == nil {
		return nil, errors.New("Connect Gmail in Clawnify first.")
	}
	return m, nil
}

// addressing is how Composio takes recipients: the first "To" alone and the
// rest as extra_recipients.
func addressing(r Recipients) map[string]any {
	args := map[string]any{"recipient_email": r.To[0]}
	if len(r.To) > 1 {
		args["extra_recipients"] = r.To[1:]
	}
	if len(r.Cc) > 0 {
		args["cc"] = r.Cc
	}
	if len(r.Bcc) > 0 {
		args["bcc"] = r.Bcc
	}
	return args
}

// EmailArgs is a new email.
type EmailArgs struct {
	Recipients
	Subject string
	Body    string
	IsHTML  bool
}

// SendEmail sends a new email from the org's mailbox (Composio GMAIL_SEND_EMAIL).
func SendEmail(ctx context.Context, conn *clawnify.Connections, a EmailArgs) (any, error) {
	m, err := mail(ctx, conn)
	if err != nil {
		return nil, err
	}
	args := addressing(a.Recipients)
	args["subject"] = a.Subject
	args["body"] = a.Body
	args["is_html"] = a.IsHTML
	return m.Run(ctx, "SEND_EMAIL", args)
}

// ReplyArgs is a reply inside a Gmail thread.
type ReplyArgs struct {
	Recipients
	ThreadID string
	Body     string
	IsHTML   bool
}

// ReplyToThread replies inside a Gmail thread, which keeps the thread's
// subject (GMAIL_REPLY_TO_THREAD).
func ReplyToThread(ctx context.Context, conn *clawnify.Connections, a ReplyArgs) (any, error) {
	m, err := mail(ctx, conn)
	if err != nil {
		return nil, err
	}
	args := addressing(a.Recipients)
	args["thread_id"] = a.ThreadID
	args["message_body"] = a.Body
	args["is_html"] = a.IsHTML
	return m.Run(ctx, "REPLY_TO_THREAD", args)
}

// ForwardArgs is a forward of one Gmail message.
type ForwardArgs struct {
	Recipients
	MessageID string
	Note      string
}

// ForwardMessage forwards one Gmail message, with an optional note above it
// (GMAIL_FORWARD_MESSAGE).
func ForwardMessage(ctx context.Context, conn *clawnify.Connections, a ForwardArgs) (any, error) {
	m, err := mail(ctx, conn)
	if err != nil {
		return nil, err
	}
	args := map[string]any{"message_id": a.MessageID, "recipients": a.To}
	if len(a.Cc) > 0 {
		args["cc"] = a.Cc
	}
	if len(a.Bcc) > 0 {
		args["bcc"] = a.Bcc
	}
	if a.Note != "" {
		args["additional_text"] = a.Note
	}
	return m.Run(ctx, "FORWARD_MESSAGE", args)
}

// MeetingArgs is a Google Calendar event. A nil duration takes TS's default
// (0 hours, 30 minutes).
type MeetingArgs struct {
	Summary         string
	StartDatetime   string // e.g. "2026-07-16T13:00:00" (no offset — timezone is separate)
	Timezone        string // e.g. "America/New_York"
	DurationHour    *float64
	DurationMinutes *float64
	Attendees       []string // email strings
	Description     string
}

// CreateMeeting creates a Google Calendar event (Composio
// GOOGLECALENDAR_CREATE_EVENT), through Google Workspace when Calendar isn't
// connected.
func CreateMeeting(ctx context.Context, conn *clawnify.Connections, a MeetingArgs) (any, error) {
	calendar := google(conn, connectedServices(ctx, conn), Services.Meeting)
	if calendar == nil {
		return nil, errors.New("Connect Google Calendar in Clawnify first.")
	}
	hour, minutes := 0.0, 30.0
	if a.DurationHour != nil {
		hour = *a.DurationHour
	}
	if a.DurationMinutes != nil {
		minutes = *a.DurationMinutes
	}
	args := map[string]any{
		"summary":                a.Summary,
		"start_datetime":         a.StartDatetime,
		"timezone":               a.Timezone,
		"event_duration_hour":    jsonNumber(hour),
		"event_duration_minutes": jsonNumber(minutes),
	}
	if len(a.Attendees) > 0 {
		args["attendees"] = a.Attendees
	}
	if a.Description != "" {
		args["description"] = a.Description
	}
	return calendar.Run(ctx, "CREATE_EVENT", args)
}

// jsonNumber is a number as JSON.stringify writes it: NaN and ±Infinity are null.
func jsonNumber(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return f
}

// NotifySlack posts a message to a Slack channel (Composio SLACK_SEND_MESSAGE).
func NotifySlack(ctx context.Context, conn *clawnify.Connections, channel, text string) (any, error) {
	return conn.Run(ctx, Services.Slack, "SLACK_SEND_MESSAGE", map[string]any{
		"channel":       channel,
		"markdown_text": text,
	})
}
