package crm

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Stats and dashboard widgets ────────────────────────────────────
//
// Route order: nothing else TS registered matches /api/stats or /api/widgets.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/stats", app.stats)
		rt.Handle("GET /api/widgets", app.widgets)
	})
}

type statsBody struct {
	Contacts  any `json:"contacts"`
	Companies any `json:"companies"`
	Deals     any `json:"deals"`
	DealValue any `json:"dealValue"`
	// Customers: companies that are customers (customer_since set).
	Customers any `json:"customers"`
}

// stats is the dashboard's statistics: how many contacts, companies, deals
// and customers, and the value of the deals not lost.
func (app *App) stats(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		var rows [4]db.Row
		for i, sql := range []string{
			"SELECT COUNT(*) as count FROM contacts",
			"SELECT COUNT(*) as count FROM companies",
			"SELECT COUNT(*) as count FROM deals",
			"SELECT COALESCE(SUM(value), 0) as total FROM deals WHERE stage NOT IN (SELECT key FROM stages WHERE is_lost = 1)",
		} {
			row, err := db.Get(ctx, sql)
			if err != nil {
				return 0, nil, err
			}
			rows[i] = row
		}
		if err := app.backfillCustomers(ctx); err != nil {
			return 0, nil, err
		}
		customers, err := db.Get(ctx, "SELECT COUNT(*) as count FROM companies WHERE customer_since IS NOT NULL AND TRIM(customer_since) != ''")
		if err != nil {
			return 0, nil, err
		}
		return 200, statsBody{
			Contacts:  orZero(rows[0], "count"),
			Companies: orZero(rows[1], "count"),
			Deals:     orZero(rows[2], "count"),
			DealValue: orZero(rows[3], "total"),
			Customers: orZero(customers, "count"),
		}, nil
	})
}

// ── Dashboard widgets ──────────────────────────────────────────────
// Tiles the Clawnify dashboard shows on its home page while the CRM is closed
// (GET /api/widgets). Data only: the dashboard draws them. Each value is one
// aggregate query. Money is USD, matching formatMoney in the client.

const widgetWeeks = 12

// Every widget summarises deals, so each carries the Deals section's tile:
// the same icon and colour as the "deals" item of the <AppNav> in
// client/app.tsx. Keep the two in step.
const (
	dealsIcon  = "dollar-sign"
	dealsColor = "green"
)

// weekStart is the Monday (UTC) of the week weeksAgo weeks before now's, as
// YYYY-MM-DD.
func weekStart(now time.Time, weeksAgo int) string {
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return isoDay(day.AddDate(0, 0, -((int(day.Weekday())+6)%7)-weeksAgo*7))
}

// widget is one tile, with @clawnify/app's WidgetSchema fields in the order
// its zod parse wrote them: the base fields, kind, then the kind's own.
type widget struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	At       string `json:"at"`
	Icon     string `json:"icon"`
	Color    string `json:"color"`
	Kind     string `json:"kind"`
	Value    any    `json:"value,omitempty"`  // metric
	Points   any    `json:"points,omitempty"` // series: []widgetPoint
	Items    any    `json:"items,omitempty"`  // breakdown: []breakdownItem; list: []listItem
	Format   string `json:"format,omitempty"`
	Currency string `json:"currency,omitempty"`
}

type widgetPoint struct {
	X string `json:"x"`
	Y any    `json:"y"`
}

type breakdownItem struct {
	Label jsText `json:"label"`
	Value any    `json:"value"`
}

type listItem struct {
	Label jsText `json:"label"`
	Meta  jsText `json:"meta"`
}

// widgets is the app's dashboard tiles for the current caller. A tile that
// fails @clawnify/app's schema (an empty deal name, say) answers 500 with the
// first five issues, as widgets() did. The route had no try: a database error
// is Hono's plain 500.
func (app *App) widgets(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	now := time.Now()
	since := weekStart(now, widgetWeeks-1)
	thisMonth, err := db.Get(ctx, "SELECT COUNT(*) AS n FROM deals WHERE created_at >= date('now', 'start of month')")
	if err != nil {
		return err
	}
	open, err := db.Get(ctx, "SELECT COALESCE(SUM(value), 0) AS total FROM deals WHERE stage IN (SELECT key FROM stages WHERE is_won = 0 AND is_lost = 0)")
	if err != nil {
		return err
	}
	byStage, err := db.Query(ctx, `SELECT s.label, COALESCE(SUM(d.value), 0) AS total
         FROM stages s JOIN deals d ON d.stage = s.key
        WHERE s.is_won = 0 AND s.is_lost = 0
        GROUP BY s.key ORDER BY total DESC LIMIT 12`)
	if err != nil {
		return err
	}
	// date(x, '-6 days', 'weekday 1') is the Monday on or before x.
	weekly, err := db.Query(ctx, `SELECT date(created_at, '-6 days', 'weekday 1') AS wk, COUNT(*) AS n
         FROM deals WHERE created_at >= ? GROUP BY wk`, since)
	if err != nil {
		return err
	}
	latest, err := db.Query(ctx, "SELECT name, value FROM deals ORDER BY created_at DESC, rowid DESC LIMIT 5")
	if err != nil {
		return err
	}

	perWeek := map[string]any{}
	for _, row := range weekly {
		if wk, ok := row["wk"].(string); ok {
			perWeek[wk] = row["n"]
		}
	}
	stages := []breakdownItem{}
	for _, row := range byStage {
		label, err := sliceText(row["label"], "r.label")
		if err != nil {
			return err
		}
		stages = append(stages, breakdownItem{Label: label, Value: row["total"]})
	}
	points := make([]widgetPoint, widgetWeeks)
	for i := range points {
		wk := weekStart(now, widgetWeeks-1-i)
		y, ok := perWeek[wk]
		if !ok || y == nil {
			y = 0.0
		}
		points[i] = widgetPoint{X: wk, Y: y}
	}
	deals := []listItem{}
	for _, row := range latest {
		label, err := sliceText(row["name"], "d.name")
		if err != nil {
			return err
		}
		deals = append(deals, listItem{Label: label, Meta: jsTextOf(usd(row["value"]))})
	}

	tiles := []widget{
		{Key: "deals-this-month", Kind: "metric", Title: "Deals created this month", Value: nullishOrZero(thisMonth, "n")},
		{Key: "open-pipeline", Kind: "metric", Title: "Open pipeline value", Value: nullishOrZero(open, "total"), Format: "currency", Currency: "USD"},
		{Key: "pipeline-by-stage", Kind: "breakdown", Title: "Open pipeline by stage", Format: "currency", Currency: "USD", Items: stages},
		{Key: "deals-per-week", Kind: "series", Title: "Deals created per week", Points: points},
		{Key: "latest-deals", Kind: "list", Title: "Latest deals", Items: deals},
	}
	for i := range tiles {
		tiles[i].At, tiles[i].Icon, tiles[i].Color = "/deals", dealsIcon, dealsColor
	}
	if issues := widgetIssues(tiles); len(issues) > 0 {
		return web.JSON(w, http.StatusInternalServerError, struct {
			Error  string      `json:"error"`
			Issues []web.Issue `json:"issues"`
		}{"invalid widgets", issues[:min(len(issues), 5)]})
	}
	return web.JSON(w, http.StatusOK, struct {
		Widgets []widget `json:"widgets"`
	}{tiles})
}

// nullishOrZero is `row?.<key> ?? 0` (orZero is `|| 0`).
func nullishOrZero(row db.Row, key string) any {
	if row == nil || row[key] == nil {
		return 0.0
	}
	return row[key]
}

// sliceText is `<v>.slice(0, 80)` on a string read from the database; what
// TS threw on anything else.
func sliceText(v any, expr string) (jsText, error) {
	s, ok := v.(string)
	if !ok {
		if v == nil {
			return nil, fmt.Errorf("Cannot read properties of null (reading 'slice')")
		}
		return nil, fmt.Errorf("%s.slice is not a function", expr)
	}
	t := jsTextOf(s)
	return t[:min(len(t), 80)], nil
}

// ── Checking the tiles: @clawnify/app's WidgetsResponse.safeParse ──
// Only what the data can break is checked; the keys, titles, paths, icons
// and formats are constants that pass.

// widgetText is the schema's `text`: z.string().min(1).max(80).
func widgetText(path []any, t jsText) []web.Issue {
	switch {
	case len(t) < 1:
		return []web.Issue{stringTooSmall(path, 1)}
	case len(t) > 80:
		return []web.Issue{stringTooBig(path, 80)}
	}
	return nil
}

// widgetNumber is z.number().finite().
func widgetNumber(path []any, v any) []web.Issue {
	f, isNumber := v.(float64)
	switch {
	case isNumber && math.IsNaN(f):
		return []web.Issue{{Code: "invalid_type", Expected: "number", Received: "nan", Path: path, Message: "Expected number, received nan"}}
	case !isNumber:
		return []web.Issue{invalidType(path, "number", v)}
	case math.IsInf(f, 0):
		return []web.Issue{{Code: "not_finite", Path: path, Message: "Number must be finite", Keys: []string{"code", "message", "path"}}}
	}
	return nil
}

func widgetIssues(tiles []widget) []web.Issue {
	var issues []web.Issue
	for i, t := range tiles {
		at := func(keys ...any) []any { return zodPath(append([]any{"widgets", i}, keys...)...) }
		switch t.Kind {
		case "metric":
			issues = append(issues, widgetNumber(at("value"), t.Value)...)
		case "series":
			for j, p := range t.Points.([]widgetPoint) {
				issues = append(issues, widgetText(at("points", j, "x"), jsTextOf(p.X))...)
				issues = append(issues, widgetNumber(at("points", j, "y"), p.Y)...)
			}
		case "breakdown":
			for j, item := range t.Items.([]breakdownItem) {
				issues = append(issues, widgetText(at("items", j, "label"), item.Label)...)
				issues = append(issues, widgetNumber(at("items", j, "value"), item.Value)...)
			}
		case "list":
			for j, item := range t.Items.([]listItem) {
				issues = append(issues, widgetText(at("items", j, "label"), item.Label)...)
				issues = append(issues, widgetText(at("items", j, "meta"), item.Meta)...)
			}
		}
	}
	return issues
}

// ── Money and JS strings ───────────────────────────────────────────

// usd is `new Intl.NumberFormat("en-US", { style: "currency", currency:
// "USD", maximumFractionDigits: 0 }).format(v ?? 0)`: whole dollars, halves
// rounded away from zero, thousands separated. ICU formats the shortest
// digits that read back as the double, so 2^60 is "$1,152,921,504,606,847,000".
// A string is read as a number first; one that isn't is "$NaN".
func usd(v any) string {
	var n float64
	switch v := v.(type) {
	case nil:
	case float64:
		n = v
	default:
		n = js.Number(v)
	}
	if math.IsNaN(n) {
		return "$NaN"
	}
	sign := ""
	if math.Signbit(n) {
		sign = "-"
	}
	if math.IsInf(n, 0) {
		return sign + "$∞"
	}
	whole, frac, _ := strings.Cut(strconv.FormatFloat(math.Abs(n), 'f', -1, 64), ".")
	if frac != "" && frac[0] >= '5' {
		whole = incrementDigits(whole)
	}
	var b strings.Builder
	for i, d := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return sign + "$" + b.String()
}

// incrementDigits adds one to a run of decimal digits ("199" → "200").
func incrementDigits(digits string) string {
	b := []byte(digits)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < '9' {
			b[i]++
			return string(b)
		}
		b[i] = '0'
	}
	return "1" + string(b)
}

// jsText is a JS string: UTF-16 code units, so a slice can cut a surrogate
// pair in half as JS's did. It is written as JSON.stringify wrote it: a lone
// surrogate as a \u escape, no HTML escaping.
type jsText []uint16

func jsTextOf(s string) jsText { return utf16.Encode([]rune(s)) }

func (t jsText) MarshalJSON() ([]byte, error) {
	b := []byte{'"'}
	for i := 0; i < len(t); i++ {
		c := t[i]
		switch {
		case c == '"':
			b = append(b, '\\', '"')
		case c == '\\':
			b = append(b, '\\', '\\')
		case c == '\b':
			b = append(b, '\\', 'b')
		case c == '\f':
			b = append(b, '\\', 'f')
		case c == '\n':
			b = append(b, '\\', 'n')
		case c == '\r':
			b = append(b, '\\', 'r')
		case c == '\t':
			b = append(b, '\\', 't')
		case c < 0x20:
			b = fmt.Appendf(b, `\u%04x`, c)
		case utf16.IsSurrogate(rune(c)):
			if c < 0xdc00 && i+1 < len(t) && t[i+1] >= 0xdc00 && t[i+1] < 0xe000 {
				b = utf8.AppendRune(b, utf16.DecodeRune(rune(c), rune(t[i+1])))
				i++
			} else {
				b = fmt.Appendf(b, `\u%04x`, c)
			}
		default:
			b = utf8.AppendRune(b, rune(c))
		}
	}
	return append(b, '"'), nil
}
