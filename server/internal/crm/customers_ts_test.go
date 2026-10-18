package crm

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"opencrm/internal/web"
)

// testdata/customers_ts.json holds scenarios (seed, steps) and what the TS
// customers.ts, deal-progress.ts and lookup.ts at 0068887 did with each: run
// by testdata/customers_ts.gen.mts under node (TZ=UTC, as on Workers) with
// @clawnify/db over node:sqlite. It recorded every step's result and every
// SQL statement with its params. This test replays each scenario through the
// Go port and wants the same. The expectations are frozen: the TS source goes
// away once the port is done.

type customersStep struct {
	Op        string          `json:"op"`
	Now       string          `json:"now"`
	Tz        float64         `json:"tz"`
	IDs       []string        `json:"ids"`
	Of        []string        `json:"of"`
	Deals     []ProgressDeal  `json:"deals"`
	Addresses []lookupAddress `json:"addresses"`
	Email     string          `json:"email"`
	Domain    string          `json:"domain"`
	Values    []string        `json:"values"`
	SQL       string          `json:"sql"`
}

type lookupAddress struct {
	Email  string `json:"email"`
	Domain string `json:"domain"`
}

type customersScenario struct {
	Name   string          `json:"name"`
	Seed   []string        `json:"seed"`
	Steps  []customersStep `json:"steps"`
	Expect struct {
		Results    []string      `json:"results"`
		Statements []tsStatement `json:"statements"`
	} `json:"expect"`
}

// asJS is v as JSON.stringify wrote it (NaN as null), keys sorted.
func asJS(t *testing.T, v any) string {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := web.JSON(rec, 200, v); err != nil {
		t.Fatal(err)
	}
	var back any
	if err := json.Unmarshal(rec.Body.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	return canonical(back)
}

func runCustomersStep(t *testing.T, ctx context.Context, rec *recorder, st customersStep) (any, error) {
	switch st.Op {
	case "overview":
		return CustomersOverview(ctx, mustTime(t, st.Now), st.Tz)
	case "accounts":
		a, err := ReadAccounts(ctx, st.IDs, mustTime(t, st.Now))
		if err != nil {
			return nil, err
		}
		of := []AccountRead{}
		for _, id := range st.Of {
			of = append(of, a.Of(id))
		}
		return map[string]any{"of": of, "calls": a.Calls, "emails": a.Emails}, nil
	case "progress":
		p, err := DealsProgress(ctx, st.Deals, mustTime(t, st.Now), st.Tz)
		if err != nil {
			return nil, err
		}
		return map[string]any{"progress": p.Progress, "sight": p.Sight}, nil
	case "closed":
		c, err := ClosedStages(ctx)
		if err != nil {
			return nil, err
		}
		// A Set of the rows' keys, in the order read: compare as a sorted list.
		keys := sortedKeys(c)
		return keys, nil
	case "lookup":
		addresses := []LookupAddress{}
		for _, a := range st.Addresses {
			addresses = append(addresses, LookupAddress{Email: a.Email, Domain: a.Domain})
		}
		return LookupMany(ctx, addresses, mustTime(t, st.Now))
	case "lookupOne":
		return Lookup(ctx, st.Email, st.Domain, mustTime(t, st.Now))
	case "bare":
		out := []string{}
		for _, v := range st.Values {
			out = append(out, BareDomain(v))
		}
		return out, nil
	case "sql":
		if _, err := rec.inner.Run(ctx, st.SQL, nil); err != nil {
			t.Fatalf("%s: %v", st.SQL, err)
		}
		return nil, nil
	}
	t.Fatalf("unknown op %s", st.Op)
	return nil, nil
}

func TestCustomersMatchTS(t *testing.T) {
	raw, err := os.ReadFile("testdata/customers_ts.json")
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []customersScenario
	if err := json.Unmarshal(raw, &scenarios); err != nil {
		t.Fatal(err)
	}
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			rec := recordedDB(t, sc.Seed)
			ctx := context.Background()
			for i, st := range sc.Steps {
				r, err := runCustomersStep(t, ctx, rec, st)
				got := ""
				if err != nil {
					got = asJS(t, map[string]string{"error": err.Error()})
				} else {
					got = asJS(t, r)
				}
				want := sc.Expect.Results[i]
				if st.Op == "closed" {
					var keys []string
					json.Unmarshal([]byte(want), &keys)
					want = canonical(sortedKeys(setOf(keys)))
				} else {
					var back any
					json.Unmarshal([]byte(want), &back)
					want = canonical(back)
				}
				if got != want {
					t.Errorf("step %d (%s now=%s tz=%v)\n got  %s\n want %s", i, st.Op, st.Now, st.Tz, got, want)
				}
			}
			rec.mu.Lock()
			got := withoutPartnerStatements(rec.log)
			rec.mu.Unlock()
			for i := 0; i < len(got) || i < len(sc.Expect.Statements); i++ {
				var g, w string
				if i < len(got) {
					g = canonical(got[i])
				}
				if i < len(sc.Expect.Statements) {
					w = canonical(sc.Expect.Statements[i])
				}
				if g != w {
					t.Fatalf("statement %d\n got  %s\n want %s", i, g, w)
				}
			}
		})
	}
}

func setOf(keys []string) map[string]bool {
	out := map[string]bool{}
	for _, k := range keys {
		out[k] = true
	}
	return out
}
