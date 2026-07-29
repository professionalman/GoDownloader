package tracker

import (
	"context"
	"time"

	"downloader/internal/networkpolicy"
)

type Repository interface {
	List(ctx context.Context) ([]networkpolicy.TrackerSource, error)
	Get(ctx context.Context, id string) (*networkpolicy.TrackerSource, error)
	Create(ctx context.Context, source *networkpolicy.TrackerSource) error
	Update(ctx context.Context, source *networkpolicy.TrackerSource) error
	Delete(ctx context.Context, id string) error
	Entries(ctx context.Context, sourceID string) ([]string, error)
	EnabledEntries(ctx context.Context) ([]string, error)
	ReplaceEntries(ctx context.Context, source *networkpolicy.TrackerSource, entries []string) error
	RecordFailure(ctx context.Context, id, message string, checkedAt time.Time) error
}
