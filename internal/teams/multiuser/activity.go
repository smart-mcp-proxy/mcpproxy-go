//go:build teams

package multiuser

import (
	"context"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// ActivityStorageProvider is the interface for accessing stored activity records.
// It abstracts the storage layer so ActivityFilter can be tested independently.
type ActivityStorageProvider interface {
	ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error)
	GetActivity(id string) (*storage.ActivityRecord, error)
}

// ActivityFilter provides user-scoped activity log access for the teams edition.
// It wraps the storage provider and filters activity records based on user identity
// extracted from the request context.
type ActivityFilter struct {
	storageProvider ActivityStorageProvider
}

// NewActivityFilter creates a new ActivityFilter with the given storage provider.
func NewActivityFilter(provider ActivityStorageProvider) *ActivityFilter {
	return &ActivityFilter{
		storageProvider: provider,
	}
}

// GetUserActivity returns activity records filtered to the authenticated user.
// For admin users, all records are returned (no user filtering).
// For regular users, only records matching their UserID are returned.
// Returns the filtered records, total matching count, and any error.
func (f *ActivityFilter) GetUserActivity(ctx context.Context, limit, offset int) ([]*storage.ActivityRecord, int, error) {
	ac := auth.AuthContextFromContext(ctx)

	filter := storage.DefaultActivityFilter()
	filter.Limit = limit
	filter.Offset = offset

	// Admin users see all activity
	if ac != nil && ac.IsAdmin() {
		return f.storageProvider.ListActivities(filter)
	}

	// Regular users only see their own activity
	if ac == nil || ac.UserID == "" {
		return nil, 0, fmt.Errorf("authentication required")
	}

	return f.listByUserID(ac.UserID, filter)
}

// GetFilteredActivity returns activity for a specific user. Only admins can call this.
// Non-admin callers receive an authorization error.
// Returns the filtered records, total matching count, and any error.
func (f *ActivityFilter) GetFilteredActivity(ctx context.Context, userID string, limit, offset int) ([]*storage.ActivityRecord, int, error) {
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil || !ac.IsAdmin() {
		return nil, 0, fmt.Errorf("admin access required")
	}

	filter := storage.DefaultActivityFilter()
	filter.Limit = limit
	filter.Offset = offset

	return f.listByUserID(userID, filter)
}

// EnrichRecord sets UserID and UserEmail on an activity record from the request context.
// This should be called when creating new activity records in the teams edition
// to associate each record with the authenticated user.
// If no auth context is present or the user has no ID, the record is left unchanged.
func (f *ActivityFilter) EnrichRecord(ctx context.Context, record *storage.ActivityRecord) {
	if record == nil {
		return
	}

	ac := auth.AuthContextFromContext(ctx)
	if ac == nil {
		return
	}

	if ac.UserID != "" {
		record.UserID = ac.UserID
	}
	if ac.Email != "" {
		record.UserEmail = ac.Email
	}
}

// listByUserID retrieves all records from storage and post-filters by UserID.
// This is a simple post-filter approach; a future iteration could push
// the user filter down to the database query for better performance.
func (f *ActivityFilter) listByUserID(userID string, baseFilter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	// Fetch a larger set from storage to account for post-filtering.
	// We request more records than needed because we'll filter some out.
	fetchFilter := baseFilter
	fetchFilter.Limit = 100 // max allowed by storage
	fetchFilter.Offset = 0

	var userRecords []*storage.ActivityRecord
	totalMatching := 0
	scanned := 0

	for {
		fetchFilter.Offset = scanned
		records, _, err := f.storageProvider.ListActivities(fetchFilter)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to list activities: %w", err)
		}

		if len(records) == 0 {
			break
		}

		for _, r := range records {
			if r.UserID == userID {
				totalMatching++
				// Collect records within the requested pagination window
				if totalMatching > baseFilter.Offset && len(userRecords) < baseFilter.Limit {
					userRecords = append(userRecords, r)
				}
			}
		}

		scanned += len(records)

		// If we got fewer records than requested, we've exhausted the store
		if len(records) < fetchFilter.Limit {
			break
		}
	}

	return userRecords, totalMatching, nil
}
