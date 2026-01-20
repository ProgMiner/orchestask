package errgroup

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func waitDone(t *testing.T, ctx context.Context, d time.Duration) {
	t.Helper()
	select {
	case <-ctx.Done():
		return
	case <-time.After(d):
		t.Fatalf("timeout waiting for ctx.Done() after %s", d)
	}
}

func TestWaitReturnsNilAndCancelsCtx(t *testing.T) {
	g, ctx := WithContext(context.Background())

	for range 3 {
		g.Go(func() error { return nil })
	}

	if err := g.Wait(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Contract: Wait() cancels derived ctx (to release resources / unblock users).
	waitDone(t, ctx, 500*time.Millisecond)
}

func TestWaitJoinsAllErrors(t *testing.T) {
	g, _ := WithContext(context.Background())

	err1 := errors.New("err1")
	err2 := errors.New("err2")

	g.Go(func() error { return err1 })
	g.Go(func() error { return err2 })

	err := g.Wait()
	if err == nil {
		t.Fatalf("expected non-nil error")
	}
	if !errors.Is(err, err1) {
		t.Fatalf("expected joined error to contain err1; got %v", err)
	}
	if !errors.Is(err, err2) {
		t.Fatalf("expected joined error to contain err2; got %v", err)
	}
}

func TestContextNotCanceledOnFirstError(t *testing.T) {
	g, ctx := WithContext(context.Background())

	firstDone := make(chan struct{})
	unblockSecond := make(chan struct{})

	err1 := errors.New("boom")

	g.Go(func() error {
		defer close(firstDone)
		return err1
	})

	g.Go(func() error {
		<-unblockSecond
		return nil
	})

	<-firstDone

	// Contract: ctx is NOT cancelled on first error (unlike x/sync/errgroup).
	select {
	case <-ctx.Done():
		t.Fatalf("ctx should not be cancelled on first error")
	default:
	}

	close(unblockSecond)

	err := g.Wait()
	if err == nil || !errors.Is(err, err1) {
		t.Fatalf("expected joined error to contain err1; got %v", err)
	}

	// Contract: Wait() cancels ctx after all goroutines finish.
	waitDone(t, ctx, 500*time.Millisecond)
}

func TestParentCancelPropagatesToDerivedContext(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, ctx := WithContext(parent)
	cancel()

	waitDone(t, ctx, 500*time.Millisecond)
}

func TestCollectErrorsConcurrently(t *testing.T) {
	g, _ := WithContext(context.Background())

	const n = 200
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		errs[i] = fmt.Errorf("e%d", i)
	}

	for i := 0; i < n; i++ {
		err := errs[i]
		g.Go(func() error { return err })
	}

	got := g.Wait()
	if got == nil {
		t.Fatalf("expected non-nil error")
	}
	for _, want := range errs {
		if !errors.Is(got, want) {
			t.Fatalf("expected joined error to contain all collected errors; missing %v", want)
		}
	}
}

