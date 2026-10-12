package partner_test

import (
	"context"
	"errors"
	"food-delivery-mvp/internal/usecase/partner"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
)

func TestMenuService_GetMenu(t *testing.T) {
	ctrl := gomock.NewController(t)
	menus := NewMockMenuRepository(ctrl)
	venueID := uuid.New()
	menus.EXPECT().GetFullMenu(gomock.Any(), venueID).Return(partner.Menu{MenuVersion: 3}, nil)

	svc := partner.NewMenuService(menus)
	m, err := svc.GetMenu(context.Background(), venueID)

	require.NoError(t, err)
	assert.Equal(t, int64(3), m.MenuVersion)
}

func TestMenuService_SyncMenu_ReturnsFreshMenuAfterSync(t *testing.T) {
	ctrl := gomock.NewController(t)
	menus := NewMockMenuRepository(ctrl)
	venueID := uuid.New()
	categories := []partner.MenuSyncCategory{{Name: "Pizza"}}

	menus.EXPECT().Sync(gomock.Any(), venueID, categories).Return(nil)
	menus.EXPECT().GetFullMenu(gomock.Any(), venueID).Return(partner.Menu{MenuVersion: 4}, nil)

	svc := partner.NewMenuService(menus)
	m, err := svc.SyncMenu(context.Background(), venueID, categories)

	require.NoError(t, err)
	assert.Equal(t, int64(4), m.MenuVersion)
}

func TestMenuService_SyncMenu_PropagatesError(t *testing.T) {
	ctrl := gomock.NewController(t)
	menus := NewMockMenuRepository(ctrl)
	venueID := uuid.New()
	boom := errors.New("db down")
	menus.EXPECT().Sync(gomock.Any(), venueID, gomock.Any()).Return(boom)

	svc := partner.NewMenuService(menus)
	_, err := svc.SyncMenu(context.Background(), venueID, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestMenuService_CreateItem(t *testing.T) {
	ctrl := gomock.NewController(t)
	menus := NewMockMenuRepository(ctrl)
	venueID := uuid.New()
	item := partner.NewMenuItem{Name: "Cola"}
	created := &domaincatalog.MenuItem{Name: "Cola"}
	menus.EXPECT().CreateItem(gomock.Any(), venueID, item).Return(created, nil)

	svc := partner.NewMenuService(menus)
	got, err := svc.CreateItem(context.Background(), venueID, item)

	require.NoError(t, err)
	assert.Same(t, created, got)
}

func TestMenuService_UpdateItem(t *testing.T) {
	ctrl := gomock.NewController(t)
	menus := NewMockMenuRepository(ctrl)
	venueID, itemID := uuid.New(), uuid.New()
	patch := partner.MenuItemPatch{}
	updated := &domaincatalog.MenuItem{ID: itemID}
	menus.EXPECT().UpdateItem(gomock.Any(), venueID, itemID, patch).Return(updated, nil)

	svc := partner.NewMenuService(menus)
	got, err := svc.UpdateItem(context.Background(), venueID, itemID, patch)

	require.NoError(t, err)
	assert.Same(t, updated, got)
}

func TestMenuService_UpdateAvailability(t *testing.T) {
	ctrl := gomock.NewController(t)
	menus := NewMockMenuRepository(ctrl)
	venueID := uuid.New()
	updates := []partner.AvailabilityUpdate{{MenuItemID: uuid.New()}}
	menus.EXPECT().UpdateAvailability(gomock.Any(), venueID, updates).Return([]domaincatalog.MenuItem{{}}, nil)

	svc := partner.NewMenuService(menus)
	items, err := svc.UpdateAvailability(context.Background(), venueID, updates)

	require.NoError(t, err)
	assert.Len(t, items, 1)
}
