package qobuz

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"
)

// ErrStalled means a started stream stopped delivering bytes without ending. Bounding the
// gap between two reads is the only bound a file of unknown length can have: an overall
// deadline would abort a long file that is arriving perfectly well.
var ErrStalled = errors.New("qobuz: the stream stopped sending")

// stallReader gives up on a body that goes quiet, by cancelling the request the body belongs
// to: nothing else makes a blocked Read return.
type stallReader struct {
	stream  io.ReadCloser
	cancel  context.CancelFunc
	timer   *time.Timer
	limit   time.Duration
	stalled atomic.Bool
}

// newStallReader prepares the bound. cancel belongs to the stream's own context, so firing it
// ends this request and no other. The timer is armed only while a Read is in flight.
func newStallReader(stream io.ReadCloser, cancel context.CancelFunc, limit time.Duration) *stallReader {
	r := &stallReader{stream: stream, cancel: cancel, limit: limit}
	r.timer = time.AfterFunc(limit, func() {
		r.stalled.Store(true)
		cancel()
	})
	r.timer.Stop()
	return r
}

func (r *stallReader) Read(p []byte) (int, error) {
	if r.stalled.Load() {
		return 0, ErrStalled
	}
	r.timer.Reset(r.limit)
	n, err := r.stream.Read(p)
	r.timer.Stop()
	if err != nil && r.stalled.Load() {
		// The transport reports a cancelled context, which is true and useless: only this
		// knows that what happened is that nothing arrived for limit.
		return n, ErrStalled
	}
	return n, err
}

// Close disarms the bound, closes the body and releases the stream's context.
func (r *stallReader) Close() error {
	r.timer.Stop()
	err := r.stream.Close()
	r.cancel()
	return err
}
