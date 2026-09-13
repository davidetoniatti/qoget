package qobuz

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Fetch resolves a stream URL for one track and writes the file, making another attempt
// where the failure was the kind another attempt can fix. It returns what was delivered.
//
// The URL is resolved again on every attempt rather than reused: it is signed with a
// timestamp, and one that has sat through a backoff is the likelier of the two to have gone
// stale.
func (c *Client) Fetch(ctx context.Context, trackID string, format Format, path string, progress Progress) (*File, error) {
	attempts, wait := c.attempts, c.retryWait

	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			c.logger.Warn("fetching a track again", "track", trackID,
				"attempt", attempt, "attempts", attempts, "after", wait, "error", err)
			select {
			case <-ctx.Done():
				return nil, errors.Join(err, ctx.Err())
			case <-time.After(wait):
			}
			wait *= 2
		}

		var file *File
		if file, err = c.FileURL(ctx, trackID, format); err == nil {
			_, err = c.Download(ctx, file, path, progress)
		}
		if err == nil {
			return file, nil
		}
		if !worthRepeating(err) {
			return nil, err
		}
	}
	if attempts == 1 {
		return nil, fmt.Errorf("gave up after 1 attempt: %w", err)
	}
	return nil, fmt.Errorf("gave up after %d attempts: %w", attempts, err)
}
