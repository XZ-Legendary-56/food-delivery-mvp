package partner

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
)

// MenuService backs the partner menu-management endpoints (docs/PLAN.md 5.3
// items 2-4): full sync, incremental create/update, and the
// fast-changing availability/stock surface kept separate from structural
// edits (see UpdateMenuItemRequest's own doc comment in partner.yaml).
type MenuService struct {
	menus MenuRepository
}

func NewMenuService(menus MenuRepository) *MenuService {
	return &MenuService{menus: menus}
}

func (s *MenuService) GetMenu(ctx context.Context, venueID uuid.UUID) (Menu, error) {
	menu, err := s.menus.GetFullMenu(ctx, venueID)
	if err != nil {
		return Menu{}, fmt.Errorf("get menu: %w", err)
	}
	return menu, nil
}

func (s *MenuService) SyncMenu(ctx context.Context, venueID uuid.UUID, categories []MenuSyncCategory) (Menu, error) {
	if err := s.menus.Sync(ctx, venueID, categories); err != nil {
		return Menu{}, fmt.Errorf("sync menu: %w", err)
	}
	return s.GetMenu(ctx, venueID)
}

func (s *MenuService) CreateItem(ctx context.Context, venueID uuid.UUID, item NewMenuItem) (*domaincatalog.MenuItem, error) {
	mi, err := s.menus.CreateItem(ctx, venueID, item)
	if err != nil {
		return nil, err
	}
	return mi, nil
}

func (s *MenuService) UpdateItem(ctx context.Context, venueID, itemID uuid.UUID, patch MenuItemPatch) (*domaincatalog.MenuItem, error) {
	mi, err := s.menus.UpdateItem(ctx, venueID, itemID, patch)
	if err != nil {
		return nil, err
	}
	return mi, nil
}

func (s *MenuService) UpdateAvailability(ctx context.Context, venueID uuid.UUID, updates []AvailabilityUpdate) ([]domaincatalog.MenuItem, error) {
	items, err := s.menus.UpdateAvailability(ctx, venueID, updates)
	if err != nil {
		return nil, fmt.Errorf("update availability: %w", err)
	}
	return items, nil
}
