package crm

import (
	"context"
	"fmt"
	"net/http"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── The team: who deals are assigned to ────────────────────────────
//
// A deal is assigned to one account (deals.owner_id) or to nobody yet. Any
// signed-in caller, an API token included, reads the accounts it can be
// assigned to here; managing them stays with GET /api/users, admins only.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/team", app.listTeam)
	})
}

// TeamMember is an account as an owner picker shows it.
type TeamMember struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type teamBody struct {
	Team []TeamMember `json:"team"`
}

func toMember(row db.Row) TeamMember {
	return TeamMember{ID: colText(row["id"]), Name: colText(row["name"]), Email: colText(row["email"])}
}

// listTeam is every account not disabled, by name.
func (app *App) listTeam(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		rows, err := db.Query(ctx, "SELECT id, name, email FROM users WHERE disabled = 0 ORDER BY CASE WHEN name = '' THEN email ELSE name END COLLATE NOCASE")
		if err != nil {
			return 0, nil, err
		}
		team := make([]TeamMember, len(rows))
		for i, row := range rows {
			team[i] = toMember(row)
		}
		return 200, teamBody{team}, nil
	})
}

// dealOwnerID reads a write's owner_id (validated as a string or null): the
// account to assign the deal to, or nil for nobody; sent is false when the
// write leaves it alone. problem is the 400 answer for an id that names no
// active account.
func dealOwnerID(ctx context.Context, body map[string]any) (owner any, sent bool, problem string, err error) {
	v, sent := body["owner_id"]
	if !sent {
		return nil, false, "", nil
	}
	id, _ := v.(string)
	if id = js.Trim(id); id == "" {
		return nil, true, "", nil
	}
	found, err := db.Get(ctx, "SELECT id FROM users WHERE id = ? AND disabled = 0", id)
	if err != nil {
		return nil, true, "", err
	}
	if found == nil {
		return nil, true, fmt.Sprintf(`owner_id: no active account with id "%s". GET /api/team lists them.`, id), nil
	}
	return id, true, "", nil
}

// withDealOwners sets each deal row's `owner`: the account it is assigned to
// ({id, name, email}), or null. No query when no deal has one.
func withDealOwners(ctx context.Context, rows []db.Row) error {
	seen := map[string]bool{}
	ids := []string{}
	for _, r := range rows {
		if r == nil {
			continue
		}
		if id, ok := r["owner_id"].(string); ok && id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	found := map[string]TeamMember{}
	for _, part := range Chunk(ids, 90) {
		users, err := db.Query(ctx, fmt.Sprintf("SELECT id, name, email FROM users WHERE id IN (%s)", marks(len(part))), stringParams(part)...)
		if err != nil {
			return err
		}
		for _, u := range users {
			found[colText(u["id"])] = toMember(u)
		}
	}
	for _, r := range rows {
		if r == nil {
			continue
		}
		var owner any // null when nobody's, or the account is gone
		if id, ok := r["owner_id"].(string); ok {
			if m, ok := found[id]; ok {
				owner = m
			}
		}
		r["owner"] = owner
	}
	return nil
}

// withDealLinks sets what a deal row carries beyond its columns and
// relations: its partners and its owner.
func withDealLinks(ctx context.Context, rows []db.Row) error {
	if err := withDealPartners(ctx, rows); err != nil {
		return err
	}
	return withDealOwners(ctx, rows)
}
