package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// D1's message comes back verbatim as the error text, as TS saw it.
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

func (d *d1) call(ctx context.Context, sql string, params []any, mode string) (*d1Response, error) {
	body, err := json.Marshal(d1Request{SQL: sql, Params: params, Mode: mode})
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
