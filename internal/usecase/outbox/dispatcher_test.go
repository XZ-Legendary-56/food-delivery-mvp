package outbox_test

import (
	"context"
	"errors"
	"food-delivery-mvp/internal/usecase/outbox"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestDispatcher_ProcessOnce_MarksDeliveredEventSent(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	publisher := NewMockEventPublisher(ctrl)

	e := outbox.Event{ID: uuid.New()}
	repo.EXPECT().FetchDue(gomock.Any(), gomock.Any(), gomock.Any()).Return([]outbox.Event{e}, nil)
	publisher.EXPECT().Publish(gomock.Any(), e).Return(nil)
	repo.EXPECT().MarkSent(gomock.Any(), e.ID, gomock.Any()).Return(nil)

	d := outbox.NewDispatcher(repo, publisher)
	n, err := d.ProcessOnce(context.Background(), 20)

	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestDispatcher_ProcessOnce_FailureBeforeLastAttempt_SchedulesRetryWithBackoff(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	publisher := NewMockEventPublisher(ctrl)

	// Attempts: 1 already failed before this poll, so this is the 2nd try —
	// still short of the 5-try limit, so it must be retried, not failed.
	e := outbox.Event{ID: uuid.New(), Attempts: 1}
	boom := errors.New("connection refused")
	repo.EXPECT().FetchDue(gomock.Any(), gomock.Any(), gomock.Any()).Return([]outbox.Event{e}, nil)
	publisher.EXPECT().Publish(gomock.Any(), e).Return(boom)
	repo.EXPECT().
		MarkRetry(gomock.Any(), e.ID, gomock.Any(), boom.Error()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, nextAttemptAt time.Time, _ string) error {
			assert.True(t, nextAttemptAt.After(time.Now()), "next attempt must be scheduled in the future")
			return nil
		})

	d := outbox.NewDispatcher(repo, publisher)
	_, err := d.ProcessOnce(context.Background(), 20)

	require.NoError(t, err)
}

func TestDispatcher_ProcessOnce_FifthFailure_MarksFailedInsteadOfRetrying(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	publisher := NewMockEventPublisher(ctrl)

	// 4 attempts already failed; this poll is the 5th and last one.
	e := outbox.Event{ID: uuid.New(), Attempts: 4}
	boom := errors.New("still down")
	repo.EXPECT().FetchDue(gomock.Any(), gomock.Any(), gomock.Any()).Return([]outbox.Event{e}, nil)
	publisher.EXPECT().Publish(gomock.Any(), e).Return(boom)
	repo.EXPECT().MarkFailed(gomock.Any(), e.ID, boom.Error()).Return(nil)
	// No MarkRetry: the event must not be scheduled for a 6th attempt.

	d := outbox.NewDispatcher(repo, publisher)
	_, err := d.ProcessOnce(context.Background(), 20)

	require.NoError(t, err)
}

func TestDispatcher_ProcessOnce_NoDueEvents_DoesNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	publisher := NewMockEventPublisher(ctrl)

	repo.EXPECT().FetchDue(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	// No Publish, MarkSent, MarkRetry or MarkFailed calls at all.

	d := outbox.NewDispatcher(repo, publisher)
	n, err := d.ProcessOnce(context.Background(), 20)

	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestDispatcher_Run_StopsWhenContextCancelled(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	publisher := NewMockEventPublisher(ctrl)
	repo.EXPECT().FetchDue(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	d := outbox.NewDispatcher(repo, publisher)
	done := make(chan struct{})
	go func() {
		d.Run(ctx, 5*time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was canceled")
	}
}
