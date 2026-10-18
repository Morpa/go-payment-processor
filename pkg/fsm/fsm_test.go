package fsm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Morpa/go-payment-processor/pkg/fsm"
)

type payload struct {
	visited []string
	score   float64
}

func record(p *payload, name string) {
	p.visited = append(p.visited, name)
}

func TestRun(t *testing.T) {
	scoring := func() *fsm.Machine[payload] {
		return fsm.New[payload]("start").
			Handle("start", func(ctx context.Context, p *payload) (fsm.State, error) {
				record(p, "start")
				return "score", nil
			}).
			Handle("score", func(ctx context.Context, p *payload) (fsm.State, error) {
				record(p, "score")
				p.score = 0.9
				return "done", nil
			}).
			Terminal("done")
	}

	tests := []struct {
		name      string
		build     func() *fsm.Machine[payload]
		wantScore float64
		wantHops  int
		wantErrIs error
	}{
		{
			name:      "happy path reaches terminal with score",
			build:     scoring,
			wantScore: 0.9,
			wantHops:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p payload
			trace, err := tt.build().Run(context.Background(), &p)

			if !errors.Is(err, tt.wantErrIs) {
				t.Fatalf("Run error = %v, wantErrIs %v", err, tt.wantErrIs)
			}
			if len(trace) != tt.wantHops {
				t.Errorf("trace length = %d, want %d", len(trace), tt.wantHops)
			}
			if p.score != tt.wantScore {
				t.Errorf("score = %f, want %f", p.score, tt.wantScore)
			}
		})
	}
}

func TestFallbackKeepsPipelineAvaliable(t *testing.T) {
	dependencyDown := errors.New("upstream timeout")

	m := fsm.New[payload]("start").
		Handle("start", func(ctx context.Context, p *payload) (fsm.State, error) {
			record(p, "start")
			return "ml", nil
		}).
		Handle("ml", func(ctx context.Context, p *payload) (fsm.State, error) {
			return "", dependencyDown
		}).
		FallbackTo("ml", "rules_only").
		Handle("rules_only", func(ctx context.Context, p *payload) (fsm.State, error) {
			record(p, "rules_only")
			return "done", nil
		}).
		Terminal("done")

	var p payload
	trace, err := m.Run(context.Background(), &p)

	if err != nil {
		t.Fatalf("Run error = %v, want nil (fallback should absorb the failure)", err)
	}
	if len(p.visited) == 0 {
		t.Fatal("visited is empty, want at least the fallback state")
	}
	if got := p.visited[len(p.visited)-1]; got != "rules_only" {
		t.Errorf("last visited = %q, want %q", got, "rules_only")
	}
	if len(trace) < 2 {
		t.Fatalf("trace length = %d, want at least 2", len(trace))
	}
	if trace[1].Err == nil {
		t.Error("trace[1].Err = nil, want the original upstream error preserved")
	}
}

func TestErrorWithoutFallbackAborts(t *testing.T) {
	boom := errors.New("boom")

	m := fsm.New[payload]("start").
		Handle("start", func(ctx context.Context, p *payload) (fsm.State, error) {
			return "", boom
		}).
		Terminal("done")

	var p payload
	_, err := m.Run(context.Background(), &p)

	if !errors.Is(err, boom) {
		t.Fatalf("Run error = %v, want it identifiable as boom via errors.Is", err)
	}
}

func TestRunErrNoTransition(t *testing.T) {
	tests := []struct {
		name      string
		build     func() *fsm.Machine[payload]
		wantHops  int
		wantState fsm.State
	}{
		{
			name: "initial state has no handler",
			build: func() *fsm.Machine[payload] {
				return fsm.New[payload]("start")
			},
			wantHops: 0,
		},
		{
			name: "handler lands on unregistered state",
			build: func() *fsm.Machine[payload] {
				return fsm.New[payload]("start").
					Handle("start", func(ctx context.Context, p *payload) (fsm.State, error) {
						record(p, "start")
						return "ghost", nil
					})
			},
			wantHops: 1,
			// the offending state must survive the wrapping so it can be inspected
			wantState: "ghost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p payload
			trace, err := tt.build().Run(context.Background(), &p)

			if !errors.Is(err, fsm.ErrNoTransition) {
				t.Fatalf("Run error = %v, wantErrIs %v", err, fsm.ErrNoTransition)
			}
			if tt.wantState != "" && !strings.Contains(err.Error(), string(tt.wantState)) {
				t.Errorf("Run error = %v, want it to mention state %q", err, tt.wantState)
			}
			if len(trace) != tt.wantHops {
				t.Errorf("trace length = %d, want %d", len(trace), tt.wantHops)
			}
		})
	}
}

func TestRunExceedsMaxHops(t *testing.T) {
	const maxHops = 3

	m := fsm.New[payload]("start", fsm.WithMaxHops[payload](maxHops)).
		Handle("start", func(ctx context.Context, p *payload) (fsm.State, error) {
			record(p, "start")
			return "start", nil
		})

	var p payload
	trace, err := m.Run(context.Background(), &p)

	if err == nil {
		t.Fatal("Run error = nil, want the hop limit to abort the loop")
	}
	if errors.Is(err, fsm.ErrNoTransition) {
		t.Errorf("Run error = %v, want a hop limit error, not ErrNoTransition", err)
	}
	if len(trace) != maxHops {
		t.Errorf("trace length = %d, want %d", len(trace), maxHops)
	}
	if len(p.visited) != maxHops {
		t.Errorf("visited = %d states, want %d", len(p.visited), maxHops)
	}
}

func TestRunContextCanceled(t *testing.T) {
	tests := []struct {
		name string
		// cancelWhen tells the start handler when to cancel
		cancelWhen bool
		wantHops   int
	}{
		{name: "cancelled before start"},
		{
			name:       "cancelled while running",
			cancelWhen: true,
			wantHops:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if !tt.cancelWhen {
				cancel()
			}
			defer cancel()

			m := fsm.New[payload]("start").
				Handle("start", func(ctx context.Context, p *payload) (fsm.State, error) {
					record(p, "start")
					if tt.cancelWhen {
						cancel()
					}
					return "score", nil
				}).
				Handle("score", func(ctx context.Context, p *payload) (fsm.State, error) {
					record(p, "score")
					return "done", nil
				}).
				Terminal("done")

			var p payload
			trace, err := m.Run(ctx, &p)

			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Run error = %v, wantErrIs %v", err, context.Canceled)
			}
			if len(trace) != tt.wantHops {
				t.Errorf("trace length = %d, want %d", len(trace), tt.wantHops)
			}
			// the machine must not run the remaining handlers after cancellation
			for _, s := range p.visited {
				if s == "score" {
					t.Error("state score was visited, want the loop to stop at the cancel check")
				}
			}
		})
	}
}
