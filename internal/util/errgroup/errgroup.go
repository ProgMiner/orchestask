package errgroup

import (
	"context"
	"errors"
	"sync"
)

// Group is similar to golang.org/x/sync/errgroup.Group, but it collects
// all returned errors instead of stopping on the first one.
//
// Wait returns:
//   - nil if no goroutine returned an error
//   - errors.Join(allErrors...) otherwise
//
// Note: the context returned by WithContext is NOT cancelled on the first error.
// It is only cancelled when:
//   - the parent context is cancelled, or
//   - Wait() completes (to release resources / unblock ctx users).
type Group struct {
	wg sync.WaitGroup

	mu   sync.Mutex
	errs []error

	cancel func()
}

// WithContext returns a new Group and a derived context.
//
// Unlike golang.org/x/sync/errgroup.WithContext, this implementation does not
// cancel the derived context on the first error (so other goroutines can finish
// and their errors can be collected).
func WithContext(parent context.Context) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(parent)
	return &Group{cancel: cancel}, ctx
}

// Go runs the given function in a new goroutine and collects a returned error.
func (g *Group) Go(fn func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()

		if err := fn(); err != nil {
			g.mu.Lock()
			g.errs = append(g.errs, err)
			g.mu.Unlock()
		}
	}()
}

// Wait blocks until all goroutines finish and returns joined errors (if any).
func (g *Group) Wait() error {
	g.wg.Wait()

	if g.cancel != nil {
		g.cancel()
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.errs) == 0 {
		return nil
	}

	return errors.Join(g.errs...)
}

