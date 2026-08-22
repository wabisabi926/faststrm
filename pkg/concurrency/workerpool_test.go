package concurrency_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/pkg/concurrency"
)

func TestPoolBasicConcurrency(t *testing.T) {
	const workers = 4
	const tasks = 100
	var running int32
	var peak int32
	var done int32

	pool := concurrency.NewPool(workers)
	for i := 0; i < tasks; i++ {
		pool.Submit(func() error {
			cur := atomic.AddInt32(&running, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&running, -1)
			atomic.AddInt32(&done, 1)
			return nil
		})
	}
	pool.Wait()
	if atomic.LoadInt32(&done) != tasks {
		t.Fatalf("expect %d done, got %d", tasks, done)
	}
	if atomic.LoadInt32(&peak) > workers {
		t.Fatalf("peak running %d > workers %d", atomic.LoadInt32(&peak), workers)
	}
	if pool.Completed() != tasks {
		t.Fatalf("pool.Completed=%d want %d", pool.Completed(), tasks)
	}
	if pool.FirstErr() != nil {
		t.Fatalf("expect no err, got %v", pool.FirstErr())
	}
}

func TestPoolFirstErrAndPanic(t *testing.T) {
	pool := concurrency.NewPool(2)
	sentinel := errors.New("boom")
	pool.Submit(func() error { return sentinel })
	pool.Submit(func() error { panic("oh no") })
	pool.Submit(func() error { return nil })
	pool.Wait()
	if pool.FirstErr() == nil {
		t.Fatal("expect err got nil")
	}
}

func TestPoolZeroWorkersFallbackOne(t *testing.T) {
	pool := concurrency.NewPool(0)
	var out int32
	pool.Submit(func() error { atomic.AddInt32(&out, 1); return nil })
	pool.Submit(func() error { atomic.AddInt32(&out, 2); return nil })
	pool.Wait()
	if atomic.LoadInt32(&out) != 3 {
		t.Fatalf("want 3 got %d", out)
	}
}
