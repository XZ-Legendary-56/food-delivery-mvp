package kitchen

import (
	"context"
	"testing"
	"venue-pasta-roma/internal/generated/partnerclient"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadMenu_PopulatesStateFromSyncResponse(t *testing.T) {
	platform := newFakePlatform(t)
	carbonaraID := uuid.New()
	externalID := "pr-carbonara"
	stockQty := 10
	platform.menuResponse = partnerclient.PartnerMenu{
		MenuVersion: 1,
		Categories: []partnerclient.PartnerMenuCategory{
			{
				Id:   uuid.New(),
				Name: "Pasta",
				Items: []partnerclient.PartnerMenuItem{
					{Id: carbonaraID, Name: "Carbonara", ExternalId: &externalID, StockQty: &stockQty},
				},
			},
		},
	}

	state := NewState()
	err := LoadMenu(context.Background(), platform.client(t), state, ownMenu())
	require.NoError(t, err)

	platformID, ok := state.SetStockByExternalID(externalID, 10)
	require.True(t, ok, "LoadMenu must have recorded pr-carbonara under its external id")
	assert.Equal(t, carbonaraID, platformID)
}

func TestLoadMenu_ItemWithoutExternalID_IsSkipped(t *testing.T) {
	platform := newFakePlatform(t)
	platform.menuResponse = partnerclient.PartnerMenu{
		Categories: []partnerclient.PartnerMenuCategory{
			{Id: uuid.New(), Name: "Pasta", Items: []partnerclient.PartnerMenuItem{
				{Id: uuid.New(), Name: "No External Id"},
			}},
		},
	}

	state := NewState()
	require.NoError(t, LoadMenu(context.Background(), platform.client(t), state, ownMenu()))

	assert.Empty(t, state.StockSnapshot())
}

func TestBuildMenu_EmptyOverride_UsesDefault(t *testing.T) {
	categories, err := BuildMenu("")
	require.NoError(t, err)
	assert.Equal(t, ownMenu(), categories)
}

func TestBuildMenu_ValidOverride_ParsesIt(t *testing.T) {
	categories, err := BuildMenu(`[{"name":"Custom","items":[{"externalId":"c-1","name":"Soup","priceMinor":1000}]}]`)
	require.NoError(t, err)
	require.Len(t, categories, 1)
	assert.Equal(t, "Custom", categories[0].Name)
	require.Len(t, categories[0].Items, 1)
	assert.Equal(t, "c-1", categories[0].Items[0].ExternalId)
}

func TestBuildMenu_InvalidJSON_Errors(t *testing.T) {
	_, err := BuildMenu("not json")
	assert.Error(t, err)
}

func TestBuildMenu_EmptyArray_Errors(t *testing.T) {
	_, err := BuildMenu("[]")
	assert.Error(t, err)
}
