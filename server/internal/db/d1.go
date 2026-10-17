package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
)

// NewD1 is the driver the container runs on. A container cannot see Worker
// bindings, so every statement goes over plain HTTP to {baseURL}/query, which
// the Worker's outboundByHost handler answers with env.DB:
//
//	→ {"sql": "...", "params": [...], "mode": "all" | "run"}
//	← 200 {"results": [{...}], "meta": {"changes": n, "last_row_id": n}}
//	← non-200 {"error": "<D1's message>"}
//
// D1's message comes back verbatim as the error text, as TS saw it. JSON has
// no NaN or Infinity, which TS bound into D1 as numbers (?page=abc is NaN, a
// deal value "Infinity" is Infinity), so a param that is one crosses as
// {"$num": "NaN" | "Infinity" | "-Infinity"}, and the Worker binds it as that
// number again.
func NewD1(baseURL string, hc *http.Client) Driver {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &d1{url: strings.TrimRight(baseURL, "/") + "/query", hc: hc}
}

type d1 struct {
	url string
	hc  *http.Client
}

type d1Request struct {
	SQL    string `json:"sql"`
	Params []any  `json:"params"`
	Mode   string `json:"mode"`
}

type d1Response struct {
	Results []Row `json:"results"`
	Meta    struct {
		Changes   float64 `json:"changes"`
		LastRowID float64 `json:"last_row_id"`
	} `json:"meta"`
	Error string `json:"error"`
}

func (d *d1) Query(ctx context.Context, sql string, params []any) ([]Row, error) {
	res, err := d.call(ctx, sql, params, "all")
	if err != nil {
		return nil, err
	}
	if res.Results == nil {
		return []Row{}, nil
	}
	return res.Results, nil
}

func (d *d1) Run(ctx context.Context, sql string, params []any) (Result, error) {
	res, err := d.call(ctx, sql, params, "run")
	if err != nil {
		return Result{}, err
	}
	return Result{Changes: int64(res.Meta.Changes), LastRowID: int64(res.Meta.LastRowID)}, nil
}

// nonFinite is a NaN or ±Infinity param on the wire.
type nonFinite struct {
	Num string `json:"$num"`
}

// wireParams is params as the bridge carries them: a non-finite number
// becomes a nonFinite, everything else is sent as it is. The caller's slice
// is left alone.
func wireParams(params []any) []any {
	var out []any // a copy, once there is a number to replace
	for i, p := range params {
		f, ok := p.(float64)
		if !ok || !(math.IsNaN(f) || math.IsInf(f, 0)) {
			continue
		}
		if out == nil {
			out = append([]any(nil), params...)
		}
		switch {
		case math.IsNaN(f):
			out[i] = nonFinite{"NaN"}
		case f > 0:
			out[i] = nonFinite{"Infinity"}
		default:
			out[i] = nonFinite{"-Infinity"}
		}
	}
	if out == nil {
		return params
	}
	return out
}

func (d *d1) call(ctx context.Context, sql string, params []any, mode string) (*d1Response, error) {
	body, err := json.Marshal(d1Request{SQL: sql, Params: wireParams(params), Mode: mode})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", d.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out d1Response
	decodeErr := json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		if out.Error != "" {
			return nil, errors.New(out.Error)
		}
		return nil, fmt.Errorf("D1 bridge answered %d", resp.StatusCode)
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("D1 bridge: %w", decodeErr)
	}
	return &out, nil
}
