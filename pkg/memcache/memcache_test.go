package memcache_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Morpa/go-payment-processor/pkg/memcache"
)

func TestSetGet(t *testing.T) {
	c := memcache.New[string](time.Minute, time.Minute)
	defer c.Close()

	c.Set("k", "v")

	got, ok := c.Get("k")
	if !ok {
		t.Fatal("Get(k) = _, false, want true")
	}
	if got != "v" {
		t.Fatalf("Get(k) = %q, true, want %q", got, "v")
	}
}

func TestExpiration(t *testing.T) {
	c := memcache.New[int](10*time.Millisecond, time.Hour)
	defer c.Close()

	c.Set("k", 42)
	time.Sleep(30 * time.Millisecond)

	got, ok := c.Get("k")
	if ok {
		t.Fatalf("Get(k) = %d, true, want ok=false after TTL", got)
	}
}

func TestIncrementsAtomic(t *testing.T) {
	c := memcache.New[int](time.Minute, time.Minute)
	defer c.Close()

	add := func(current int, exists bool) int {
		if !exists {
			return 1
		}
		return current + 1
	}

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			c.Increment("card:123", add)
		})
	}
	wg.Wait()

	got, ok := c.Get("card:123")
	if !ok {
		t.Fatal("Get(card:123) = _, false, want true")
	}
	if got != 100 {
		t.Fatalf("Get(card:123) = %d, true, want 100", got)
	}
}

func TestConcurrentAccessAcrossShards(t *testing.T) {
	c := memcache.New[int](time.Minute, time.Minute)
	defer c.Close()

	var wg sync.WaitGroup
	for i := range 200 {
		wg.Go(func() {
			key := fmt.Sprintf("key-%d", i%50)
			c.Set(key, i)
			c.Get(key)
		})
	}
	wg.Wait()
}

func TestGetMiss(t *testing.T) {
	c := memcache.New[string](time.Minute, time.Minute)
	defer c.Close()

	got, ok := c.Get("missing")
	if ok || got != "" {
		t.Fatalf("Get(missing) = %q, %v, want \"\", false", got, ok)
	}
}

func TestCloseIdempotent(t *testing.T) {
	c := memcache.New[int](time.Minute, time.Minute)

	if err := c.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close() = %v, want nil", err)
	}
}

func TestSetOverwritesExistingKey(t *testing.T) {
	c := memcache.New[int](time.Minute, time.Minute)
	defer c.Close()

	c.Set("k", 1)
	c.Set("k", 2)

	got, ok := c.Get("k")
	if !ok || got != 2 {
		t.Fatalf("Get(k) = %d, %v, want 2, true", got, ok)
	}
}

func TestSetTTLOverridesDefault(t *testing.T) {
	c := memcache.New[int](time.Hour, time.Hour)
	defer c.Close()

	c.Set("short", 1)
	c.SetTTL("short", 2, 10*time.Millisecond)

	got, ok := c.Get("short")
	if !ok || got != 2 {
		t.Fatalf("Get(short) = %d, %v before TTL, want 2, true", got, ok)
	}

	time.Sleep(30 * time.Millisecond)

	if got, ok := c.Get("short"); ok {
		t.Fatalf("Get(short) = %d, true after TTL, want ok=false", got)
	}
}

func TestIncrementRestartsAfterExpiration(t *testing.T) {
	c := memcache.New[int](10*time.Millisecond, time.Hour)
	defer c.Close()

	add := func(current int, exists bool) int {
		if !exists {
			return 1
		}
		return current + 1
	}

	if got := c.Increment("k", add); got != 1 {
		t.Fatalf("Increment(k) = %d, want 1 on fresh key", got)
	}
	if got := c.Increment("k", add); got != 2 {
		t.Fatalf("Increment(k) = %d, want 2", got)
	}

	time.Sleep(30 * time.Millisecond)

	// expired entry: the callback gets exists=false and the counter restarts
	if got := c.Increment("k", add); got != 1 {
		t.Fatalf("Increment(k) after TTL = %d, want 1", got)
	}
}

func TestLen(t *testing.T) {
	c := memcache.New[int](time.Minute, time.Minute)
	defer c.Close()

	for _, k := range []string{"a", "b", "c"} {
		c.Set(k, 1)
	}
	c.Set("a", 2) // overwrite doesn't change the count

	if got := c.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3", got)
	}
}

func TestJanitorEvictsExpired(t *testing.T) {
	const (
		ttl        = 50 * time.Millisecond
		sweepEvery = 10 * time.Millisecond
	)

	c := memcache.New[int](ttl, sweepEvery)
	defer c.Close()

	for i := range 200 {
		c.Set(fmt.Sprintf("key-%d", i), i)
	}
	if got := c.Len(); got != 200 {
		t.Fatalf("Len() = %d before sweep, want 200", got)
	}

	// polling instead of a fixed sleep: the test ends once the janitor
	// actually swept everything, with a safety deadline
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()

	for c.Len() != 0 {
		select {
		case <-deadline:
			t.Fatalf("Len() = %d after 2s, want 0 (janitor nunca varreu?)", c.Len())
		case <-tick.C:
		}
	}
}
