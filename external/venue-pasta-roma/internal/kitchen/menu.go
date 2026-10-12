package kitchen

import (
	"context"
	"encoding/json"
	"fmt"
	"venue-pasta-roma/internal/generated/partnerclient"
)

// BuildMenu returns the menu this service should push to the platform:
// menuJSON (config.Config.MenuJSON, env MENU_JSON) when set — the same
// []MenuSyncCategory shape PUT /menu itself accepts, so an operator can
// swap this venue's menu without a rebuild — or ownMenu()'s built-in
// default otherwise.
func BuildMenu(menuJSON string) ([]partnerclient.MenuSyncCategory, error) {
	if menuJSON == "" {
		return ownMenu(), nil
	}
	var categories []partnerclient.MenuSyncCategory
	if err := json.Unmarshal([]byte(menuJSON), &categories); err != nil {
		return nil, fmt.Errorf("parse MENU_JSON: %w", err)
	}
	if len(categories) == 0 {
		return nil, fmt.Errorf("MENU_JSON must contain at least one category")
	}
	return categories, nil
}

// ownMenu is this venue's own default menu, as it exists in the venue's
// imagined internal system — external ids are picked here, before the
// platform has ever heard of any of these dishes, which is the whole point
// of docs/PLAN.md's "source/external_id" mechanism (docs/schema-decisions.md).
func ownMenu() []partnerclient.MenuSyncCategory {
	boolPtr := func(b bool) *bool { return &b }
	intPtr := func(i int) *int { return &i }

	return []partnerclient.MenuSyncCategory{
		{
			Name: "Pasta",
			Items: []partnerclient.MenuSyncItem{
				{ExternalId: "pr-carbonara", Name: "Carbonara", PriceMinor: 45000, StockQty: intPtr(10), IsAvailable: boolPtr(true)},
				{ExternalId: "pr-bolognese", Name: "Bolognese", PriceMinor: 42000, StockQty: intPtr(8), IsAvailable: boolPtr(true)},
			},
		},
		{
			Name: "Pizza",
			Items: []partnerclient.MenuSyncItem{
				{ExternalId: "pr-margherita", Name: "Margherita", PriceMinor: 39000, StockQty: intPtr(12), IsAvailable: boolPtr(true)},
			},
		},
		{
			Name: "Dessert",
			Items: []partnerclient.MenuSyncItem{
				{ExternalId: "pr-tiramisu", Name: "Tiramisu", PriceMinor: 25000, StockQty: intPtr(15), IsAvailable: boolPtr(true)},
			},
		},
	}
}

// LoadMenu pushes categories (see BuildMenu) to the platform via PUT
// /partner/menu (docs/PLAN.md 8.2 item 1) and records the platform ids it gets
// back into state, so later webhook payloads (which only ever carry the
// platform's id) can be resolved back to this service's own stock counters.
func LoadMenu(ctx context.Context, client *partnerclient.ClientWithResponses, state *State, categories []partnerclient.MenuSyncCategory) error {
	resp, err := client.SyncMenuWithResponse(ctx, partnerclient.SyncMenuJSONRequestBody{
		Categories: categories,
	})
	if err != nil {
		return fmt.Errorf("call syncMenu: %w", err)
	}
	if resp.JSON200 == nil {
		return fmt.Errorf("syncMenu: unexpected response (status %d): %s", resp.HTTPResponse.StatusCode, string(resp.Body))
	}

	var items []MenuItem
	for _, category := range resp.JSON200.Categories {
		for _, item := range category.Items {
			if item.ExternalId == nil {
				continue
			}
			items = append(items, MenuItem{
				PlatformID: item.Id,
				ExternalID: *item.ExternalId,
				Name:       item.Name,
				StockQty:   stockQtyOf(item),
			})
		}
	}
	state.SetMenu(items)
	return nil
}

// stockQtyOf treats "unlimited" (nil StockQty) as a very large but finite
// number: this emulator's own CheckAndReserve needs a plain int to compare
// against an order line's quantity, and nothing in ownMenu ever sets an
// item to unlimited stock, so this path only matters if that changes later.
func stockQtyOf(item partnerclient.PartnerMenuItem) int {
	if item.StockQty == nil {
		return 1 << 30
	}
	return *item.StockQty
}
