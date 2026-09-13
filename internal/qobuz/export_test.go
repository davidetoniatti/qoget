package qobuz

import (
	"context"
	"io"
	"time"
)

// NewStallReaderForTest exposes stallReader to package qobuz_test.
func NewStallReaderForTest(stream io.ReadCloser, cancel context.CancelFunc, limit time.Duration) io.ReadCloser {
	return newStallReader(stream, cancel, limit)
}
