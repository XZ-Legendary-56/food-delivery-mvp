package order_test

import (
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/domain/order"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allStatuses lists every order.Status so the tests below can check the
// full from/to matrix, not just the edges the author remembered to write.
var allStatuses = []order.Status{
	order.StatusCreated,
	order.StatusConfirmed,
	order.StatusCooking,
	order.StatusReady,
	order.StatusDelivering,
	order.StatusDelivered,
	order.StatusRejected,
	order.StatusCancelled,
}

// allowedEdges is docs/PLAN.md 5.4's diagram, spelled out as a set, independent
// of status.go's own transitions table — this test must fail if someone
// edits the table without also meaning to change the business rule.
var allowedEdges = map[order.Status]map[order.Status]bool{
	order.StatusCreated: {
		order.StatusConfirmed: true,
		order.StatusRejected:  true,
		order.StatusCancelled: true,
	},
	order.StatusConfirmed: {
		order.StatusCooking:   true,
		order.StatusCancelled: true,
	},
	order.StatusCooking: {
		order.StatusReady: true,
	},
	order.StatusReady: {
		order.StatusDelivering: true,
	},
	order.StatusDelivering: {
		order.StatusDelivered: true,
	},
}

func TestCanTransition_FullMatrix(t *testing.T) {
	for _, from := range allStatuses {
		for _, to := range allStatuses {
			from, to := from, to
			want := allowedEdges[from][to]
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				assert.Equal(t, want, order.CanTransition(from, to))
			})
		}
	}
}

func TestValidateTransition_AllowedReturnsNil(t *testing.T) {
	for from, tos := range allowedEdges {
		for to := range tos {
			assert.NoError(t, order.ValidateTransition(from, to), "%s -> %s should be allowed", from, to)
		}
	}
}

func TestValidateTransition_DisallowedReturnsInvalidStateTransition(t *testing.T) {
	err := order.ValidateTransition(order.StatusCreated, order.StatusDelivered)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeOrderInvalidStateTransition, code)
}

func TestValidateTransition_TerminalStatusesRejectEverything(t *testing.T) {
	for _, terminal := range []order.Status{order.StatusDelivered, order.StatusRejected, order.StatusCancelled} {
		for _, to := range allStatuses {
			err := order.ValidateTransition(terminal, to)
			assert.Error(t, err, "%s -> %s should be rejected: %s is terminal", terminal, to, terminal)
		}
	}
}

func TestValidateTransition_CancelOnlyBeforeCooking(t *testing.T) {
	assert.NoError(t, order.ValidateTransition(order.StatusCreated, order.StatusCancelled))
	assert.NoError(t, order.ValidateTransition(order.StatusConfirmed, order.StatusCancelled))

	for _, from := range []order.Status{order.StatusCooking, order.StatusReady, order.StatusDelivering} {
		assert.Error(t, order.ValidateTransition(from, order.StatusCancelled),
			"cancel from %s should be rejected: cancellation is only allowed before cooking starts", from)
	}
}
