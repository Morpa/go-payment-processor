package batch_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Morpa/go-payment-processor/pkg/batch"
)

func TestSubmitAfterClose(t *testing.T) {
	echo := func(_ context.Context, items []int) ([]int, error) { return items, nil }
	b := batch.New(2, time.Millisecond, echo)
	b.Close()

	_, err := b.Submit(context.Background(), 1)
	if !errors.Is(err, batch.ErrClosed) {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
}

func TestBatchBySize(t *testing.T) {
	var calls atomic.Int32
	double := func(_ context.Context, items []int) ([]int, error) {
		calls.Add(1)
		outs := make([]int, len(items))
		for i, v := range items {
			outs[i] = v * 2
		}
		return outs, nil
	}

	b := batch.New(4, time.Second, double)
	defer b.Close()

	results := make([]int, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := b.Submit(context.Background(), i)
			if err != nil {
				t.Errorf("Submit(%d): %v", i, err)
				return
			}
			results[i] = out
		}(i)
	}
	wg.Wait()

	for i, got := range results {
		if got != i*2 {
			t.Errorf("results[%d] = %d, want %d", i, got, i*2)
		}
	}
	if got := calls.Load(); got > 4 {
		t.Errorf("flusher called %d times, want at most 4", got)
	}
}

func TestFlushesByTimeWindow(t *testing.T) {
	echo := func(_ context.Context, items []string) ([]string, error) { return items, nil }

	// maxSize alto de propósito: só o timer pode fechar o lote.
	b := batch.New(100, 20*time.Millisecond, echo)
	defer b.Close()

	start := time.Now()
	out, err := b.Submit(context.Background(), "lonely")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out != "lonely" {
		t.Errorf("got %q, want %q", out, "lonely")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("waited %v, want <= 500ms", elapsed)
	}
}

func TestNewClampsMaxSize(t *testing.T) {
	echo := func(_ context.Context, items []int) ([]int, error) { return items, nil }

	b := batch.New(0, time.Millisecond, echo)
	defer b.Close()

	out, err := b.Submit(context.Background(), 7)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out != 7 {
		t.Errorf("got %d, want 7", out)
	}
}

func TestSubmitContextTimeout(t *testing.T) {
	release := make(chan struct{})
	block := func(_ context.Context, items []int) ([]int, error) {
		<-release
		return items, nil
	}

	b := batch.New(10, time.Hour, block)
	defer func() {
		close(release)
		b.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := b.Submit(ctx, 1)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want DeadlineExceeded", err)
	}
}

func TestCloseTwice(t *testing.T) {
	echo := func(_ context.Context, items []int) ([]int, error) { return items, nil }

	b := batch.New(2, time.Millisecond, echo)
	b.Close()
	b.Close()
}

func TestFlusherError(t *testing.T) {
	sentinel := errors.New("flush failed")

	tests := []struct {
		name       string
		flush      batch.Flusher[int, int]
		wantSentin bool
	}{
		{
			name:       "sentinel passes through",
			flush:      func(_ context.Context, items []int) ([]int, error) { return make([]int, len(items)), sentinel },
			wantSentin: true,
		},
		{
			name:  "wrong result count",
			flush: func(_ context.Context, items []int) ([]int, error) { return nil, sentinel },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := batch.New(2, time.Millisecond, tt.flush)
			defer b.Close()

			_, err := b.Submit(context.Background(), 1)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := errors.Is(err, sentinel); got != tt.wantSentin {
				t.Errorf("errors.Is(err, sentinel) = %v, want %v (err: %v)", got, tt.wantSentin, err)
			}
		})
	}
}

// maxWait menor que o dispatch: o timer dispara enquanto o loop está ocupado
// no flush por tamanho, e a abertura da próxima janela drena esse timer vencido.
func TestStaleTimerDrainedOnNewWindow(t *testing.T) {
	slowEcho := func(_ context.Context, items []int) ([]int, error) {
		time.Sleep(10 * time.Millisecond)
		return items, nil
	}

	b := batch.New(2, 2*time.Millisecond, slowEcho)
	defer b.Close()

	const (
		workers   = 8
		perWorker = 5
	)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				v := base*perWorker + i
				out, err := b.Submit(context.Background(), v)
				if err != nil {
					t.Errorf("Submit(%d): %v", v, err)
					return
				}
				if out != v {
					t.Errorf("Submit(%d) = %d, want %d", v, out, v)
				}
			}
		}(w)
	}
	wg.Wait()
}
