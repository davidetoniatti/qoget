package qobuz

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
)

// statusError is an HTTP status this package could not use, kept typed so that a server
// having a bad minute can be told from one that has given an answer.
type statusError struct {
	// path is the API call, and empty for a stream: a stream is not one of the API's calls
	// and has no message body worth quoting.
	path    string
	code    int
	message string
}

func (e *statusError) Error() string {
	if e.path == "" {
		return fmt.Sprintf("qobuz: stream returned %d", e.code)
	}
	return fmt.Sprintf("qobuz: %s returned %d: %s", e.path, e.code, e.message)
}

// worthRepeating reports whether a failed attempt at one file is worth making again.
//
// Stated by what may not be repeated: an answer Qobuz has given, a failure writing the file, and a
// caller that has stopped asking. Everything left is the network or a server having a bad
// minute, which is precisely what another attempt is for, and what an unrepeatable failure
// costs by being retried is two more attempts against a local error.
func worthRepeating(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrNoAuthToken), errors.Is(err, ErrWrongContainer):
		return false
	case errors.As(err, new(*fs.PathError)), errors.As(err, new(*os.LinkError)):
		// The disk, not the network: the same write fails the same way.
		return false
	}

	var status *statusError
	if errors.As(err, &status) {
		return status.code == http.StatusRequestTimeout ||
			status.code == http.StatusTooManyRequests ||
			(status.path == "" && status.code == http.StatusForbidden) ||
			status.code >= http.StatusInternalServerError
	}
	return true
}
