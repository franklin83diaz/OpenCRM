package js

import (
	"math"
	"regexp"
	"testing"
	"time"
)

func TestParseDate(t *testing.T) {
	utc := func(h, m, s, ms int) time.Time {
		return time.Date(2026, 10, 14, h, m, s, ms*1e6, time.UTC)
	}
	cases := []struct {
		in   string
		want time.Time
		ok   bool
	}{
		// SQLite's datetime('now') has no zone: UTC, not the machine's local time.
		{"2026-10-14 16:10:00", utc(16, 10, 0, 0), true},
		{"2026-10-14", utc(0, 0, 0, 0), true},
		{"2026-10-14T16:10:00+02:00", utc(14, 10, 0, 0), true},
		{"2026-10-14T16:10:00.005Z", utc(16, 10, 0, 5), true},
		{"2026-10-14T16:10", utc(16, 10, 0, 0), true},
		{"2026-10-14T16:10:00-0130", utc(17, 40, 0, 0), true},
		{"2026-10-14T16:10:00.1239Z", utc(16, 10, 0, 123), true},
		{" 2026-10-14 ", utc(0, 0, 0, 0), true},
		// V8 rolls an out-of-range day over instead of rejecting it.
		{"2026-02-31", time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC), true},
		{"2026-10-14T24:00:00Z", time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), true},
		{"nope", time.Time{}, false},
		{"", time.Time{}, false},
		{"2026-13-01", time.Time{}, false},
		{"2026-10-14T24:00:01Z", time.Time{}, false},
		{"2026-10-14T16:10:60Z", time.Time{}, false},
	}
	for _, c := range cases {
		got, ok := ParseDate(c.in)
		if ok != c.ok || !got.Equal(c.want) {
			t.Errorf("ParseDate(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
		if ok && got.Location() != time.UTC {
			t.Errorf("ParseDate(%q) location = %v; want UTC", c.in, got.Location())
		}
	}
}

func TestISO(t *testing.T) {
	if got := ISO(time.Date(2026, 10, 14, 16, 10, 0, 5e6, time.UTC)); got != "2026-10-14T16:10:00.005Z" {
		t.Errorf("ISO = %q", got)
	}
	plus2 := time.FixedZone("+02", 2*3600)
	if got := ISO(time.Date(2026, 10, 14, 18, 10, 0, 0, plus2)); got != "2026-10-14T16:10:00.000Z" {
		t.Errorf("ISO in +02 = %q", got)
	}
}

func TestNumber(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{" 12 ", 12},
		{"", 0},
		{"   ", 0},
		{nil, 0},
		{true, 1},
		{false, 0},
		{"1e3", 1000},
		{"-2.5", -2.5},
		{".5", 0.5},
		{"5.", 5},
		{"0x10", 16},
		{"0b101", 5},
		{"Infinity", math.Inf(1)},
		{3.25, 3.25},
		{7, 7},
		{int64(8), 8},
		{[]any{}, 0},
		{[]any{"4"}, 4},
	}
	for _, c := range cases {
		if got := Number(c.in); got != c.want {
			t.Errorf("Number(%#v) = %v; want %v", c.in, got, c.want)
		}
	}
	for _, in := range []any{"x", "12px", "1_000", "-0x10", "inf", "NaN", map[string]any{}, []any{1.0, 2.0}} {
		if got := Number(in); !math.IsNaN(got) {
			t.Errorf("Number(%#v) = %v; want NaN", in, got)
		}
	}
}

func TestString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{3.0, "3"},
		{0.1, "0.1"},
		{-0.0, "0"},
		{1e21, "1e+21"},
		{1e-7, "1e-7"},
		{1.5e-6, "0.0000015"},
		{123456789012345680000.0, "123456789012345680000"},
		{1.25e25, "1.25e+25"},
		{1.0 / 3, "0.3333333333333333"},
		{math.NaN(), "NaN"},
		{math.Inf(-1), "-Infinity"},
		{42, "42"},
		{int64(-7), "-7"},
		{true, "true"},
		{"Zoë", "Zoë"},
		{nil, "null"},
		{[]any{1.0, nil, "a"}, "1,,a"},
		{map[string]any{"a": 1.0}, "[object Object]"},
	}
	for _, c := range cases {
		if got := String(c.in); got != c.want {
			t.Errorf("String(%#v) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestTruthy(t *testing.T) {
	for _, v := range []any{true, "0", " ", 1.0, -1, int64(2), []any{}, map[string]any{}} {
		if !Truthy(v) {
			t.Errorf("Truthy(%#v) = false", v)
		}
	}
	for _, v := range []any{nil, false, "", 0.0, math.NaN(), 0, int64(0)} {
		if Truthy(v) {
			t.Errorf("Truthy(%#v) = true", v)
		}
	}
}

func TestUUID(t *testing.T) {
	v4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a, b := UUID(), UUID()
	if !v4.MatchString(a) || a == b {
		t.Errorf("UUID() = %q, %q", a, b)
	}
}

func TestParseInt(t *testing.T) {
	// Expectations from node: parseInt(s, 10).
	cases := []struct {
		in   string
		want float64
	}{
		{"25", 25}, {"0", 0}, {"-3", -3}, {" 7", 7}, {" \n12px", 12}, {"+4", 4}, {"1e3", 1}, {"2.9", 2},
		{"0x10", 0}, {"99999999999999999999", 1e20}, {"12345678901234567890123", 1.2345678901234568e+22},
		{" 7", 7}, {"00012", 12}, {"1_000", 1},
	}
	for _, c := range cases {
		if got := ParseInt(c.in); got != c.want {
			t.Errorf("ParseInt(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, in := range []string{"abc", "", "-", "+", "--1", "٣"} {
		if got := ParseInt(in); !math.IsNaN(got) {
			t.Errorf("ParseInt(%q) = %v, want NaN", in, got)
		}
	}
	if got := ParseInt("-0"); got != 0 || !math.Signbit(got) {
		t.Errorf("ParseInt(-0) = %v, want -0", got)
	}
}
