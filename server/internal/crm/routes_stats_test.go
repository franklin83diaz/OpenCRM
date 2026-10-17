package crm

import (
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"opencrm/internal/web"
)

// Expected values are node 22's (the same V8/ICU as workerd): Intl.NumberFormat,
// the TS weekStart over a given now, and JSON.stringify of s.slice(0, 80).

func TestStatsUSD(t *testing.T) {
	for _, c := range []struct {
		in   any
		want string
	}{
		{0.0, "$0"}, {math.Copysign(0, -1), "-$0"}, {0.5, "$1"}, {1.5, "$2"}, {2.5, "$3"}, {-0.5, "-$1"}, {-0.4, "-$0"},
		{-2.5, "-$3"}, {0.49999999999999994, "$0"}, {1234567.5, "$1,234,568"}, {999.5, "$1,000"},
		{1e21, "$1,000,000,000,000,000,000,000"}, {math.Pow(2, 60), "$1,152,921,504,606,847,000"},
		{1.2345678901234567e25, "$12,345,678,901,234,566,000,000,000"}, {5e-324, "$0"}, {4800.0, "$4,800"},
		{300.5, "$301"}, {-1234.4, "-$1,234"}, {99.99, "$100"}, {math.NaN(), "$NaN"}, {math.Inf(1), "$∞"},
		{math.Inf(-1), "-$∞"}, {"12.5", "$13"}, {"abc", "$NaN"}, {"", "$0"}, {" 7 ", "$7"}, {"1e3", "$1,000"},
		{"0x10", "$16"}, {nil, "$0"},
	} {
		if got := usd(c.in); got != c.want {
			t.Errorf("usd(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStatsWeekStart(t *testing.T) {
	for _, c := range []struct {
		now      string
		weeksAgo int
		want     string
	}{
		{"2026-10-17T12:00:00Z", 0, "2026-10-12"}, {"2026-10-17T12:00:00Z", 11, "2026-07-27"},
		{"2026-10-19T00:00:00Z", 0, "2026-10-19"}, {"2026-10-18T23:59:59Z", 0, "2026-10-12"},
		{"2024-03-03T10:00:00Z", 11, "2023-12-11"}, {"2025-01-01T00:00:00Z", 1, "2024-12-23"},
		{"2028-02-29T05:00:00Z", 0, "2028-02-28"},
	} {
		now, _ := time.Parse(time.RFC3339, c.now)
		if got := weekStart(now.In(time.FixedZone("x", -11*3600)), c.weeksAgo); got != c.want {
			t.Errorf("weekStart(%s, %d) = %s, want %s", c.now, c.weeksAgo, got, c.want)
		}
	}
}

func TestStatsSlicedLabelsAsJSONStringify(t *testing.T) {
	long := ""
	for i := 0; i < 79; i++ {
		long += "A"
	}
	for _, c := range []struct{ in, want string }{
		{"plain", `"plain"`},
		{long + "😀x", `"` + long + `\ud83d"`},
		{"q\"b\\s\n\t\b\f\r\u0001\u001f", `"q\"b\\s\n\t\b\f\r\u0001\u001f"`},
		{"<&>", `"<&>"`},
		{"\u2028\u2029", "\"\u2028\u2029\""}, // raw, as JSON.stringify wrote them
		{"é😀", `"é😀"`},
	} {
		label, err := sliceText(c.in, "x")
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		if err := web.JSON(w, 200, breakdownItem{Label: label, Value: 1.0}); err != nil {
			t.Fatal(err)
		}
		if got, want := w.Body.String(), `{"label":`+c.want+`,"value":1}`; got != want {
			t.Errorf("%q: got %s, want %s", c.in, got, want)
		}
	}
}
