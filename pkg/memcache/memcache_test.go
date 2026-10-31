package memcache_test

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Morpa/go-payment-processor/pkg/memcache"
)

func TestSetGet(t *testing.T) {
	c := memcache.New[string](time.Minute, time.Minute)
	defer c.Close()

	if err := c.Set("k", "v"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, ok := c.Get("k")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if v != "v" {
		t.Errorf("expected v, got %s", v)
	}
}

func TestExpiration(t *testing.T) {
	c := memcache.New[int](10*time.Millisecond, time.Hour)
	defer c.Close()

	if err := c.Set("k", 42); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	_, ok := c.Get("k")
	if ok {
		t.Error("expected key to be expired")
	}
}

func TestIncrementIsAtomic(t *testing.T) {
	c := memcache.New[int](time.Minute, time.Minute)
	defer c.Close()

	add := func(current int, exists bool) int {
		if !exists {
			return 1
		}
		return current + 1
	}

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := c.Increment("card:123", add)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	v, ok := c.Get("card:123")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if v != goroutines {
		t.Errorf("expected %d, got %d", goroutines, v)
	}
}

func TestConcurrentAccessAcrossShards(t *testing.T) {
	c := memcache.New[int](time.Minute, time.Minute)
	defer c.Close()

	const goroutines = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(val int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", val%50)
			if err := c.Set(key, val); err != nil {
				t.Errorf("unexpected error on set: %v", err)
			}
			_, ok := c.Get(key)
			if !ok {
				t.Errorf("expected key %s to exist", key)
			}
		}(i)
	}
	wg.Wait()
}

func TestCloseError(t *testing.T) {
	c := memcache.New[int](time.Minute, time.Minute)
	if err := c.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := c.Close(); !errors.Is(err, memcache.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
}

func TestInvalidKeyError(t *testing.T) {
	c := memcache.New[int](time.Minute, time.Minute)
	defer c.Close()

	if err := c.Set("", 1); !errors.Is(err, memcache.ErrInvalidKey) {
		t.Errorf("expected ErrInvalidKey on set, got %v", err)
	}
	if _, ok := c.Get(""); ok {
		t.Errorf("expected false on get with empty key")
	}
	if _, err := c.Increment("", func(c int, e bool) int { return 1 }); !errors.Is(err, memcache.ErrInvalidKey) {
		t.Errorf("expected ErrInvalidKey on increment, got %v", err)
	}
}

func TestClosedCacheError(t *testing.T) {
	c := memcache.New[int](time.Minute, time.Minute)
	c.Close()

	if err := c.Set("k", 1); !errors.Is(err, memcache.ErrClosed) {
		t.Errorf("expected ErrClosed on set, got %v", err)
	}
	if _, ok := c.Get("k"); ok {
		t.Errorf("expected false on get when closed")
	}
	if _, err := c.Increment("k", func(c int, e bool) int { return 1 }); !errors.Is(err, memcache.ErrClosed) {
		t.Errorf("expected ErrClosed on increment, got %v", err)
	}
}

func TestJanitorEvictsExpired(t *testing.T) {
	c := memcache.New[int](10*time.Millisecond, 1*time.Millisecond)
	defer c.Close()

	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%d", i)
		if err := c.Set(key, i); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Janitor is asynchronous, poll until evicted or deadline.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if c.Len() == 0 {
			return
		}
		runtime.Gosched()
	}

	t.Errorf("janitor did not evict expired items within deadline, got %d", c.Len())
}
