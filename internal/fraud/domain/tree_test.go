package domain

import (
	"encoding/json"
	"errors"
	"testing"
)

func leaf(value float64) Node {
	return Node{Left: -1, Value: value}
}

func TestParseTreeModel(t *testing.T) {
	tests := []struct {
		name    string
		model   TreeModel
		wantErr bool
	}{
		{
			name: "valid model",
			model: TreeModel{
				Version:  "v1",
				Features: 2,
				Baseline: -0.5,
				Trees: []Tree{{
					Nodes: []Node{
						{Feature: 0, Threshold: 100, Left: 1, Right: 2},
						leaf(0.4),
						leaf(-0.2),
					},
				}},
			},
		},
		{
			name:    "zero features",
			model:   TreeModel{Features: 0, Trees: []Tree{{Nodes: []Node{leaf(0)}}}},
			wantErr: true,
		},
		{
			name:    "negative features",
			model:   TreeModel{Features: -1, Trees: []Tree{{Nodes: []Node{leaf(0)}}}},
			wantErr: true,
		},
		{
			name:    "no trees",
			model:   TreeModel{Features: 1},
			wantErr: true,
		},
		{
			name:    "tree without nodes",
			model:   TreeModel{Features: 1, Trees: []Tree{{}}},
			wantErr: true,
		},
		{
			name: "negative feature index",
			model: TreeModel{Features: 1, Trees: []Tree{{Nodes: []Node{
				{Feature: -1, Threshold: 0, Left: 1, Right: 2},
				leaf(0), leaf(0),
			}}}},
			wantErr: true,
		},
		{
			name: "feature index above count",
			model: TreeModel{Features: 1, Trees: []Tree{{Nodes: []Node{
				{Feature: 1, Threshold: 0, Left: 1, Right: 2},
				leaf(0), leaf(0),
			}}}},
			wantErr: true,
		},
		{
			name: "negative left child",
			model: TreeModel{Features: 1, Trees: []Tree{{Nodes: []Node{
				{Feature: 0, Threshold: 0, Left: -2, Right: 2},
				leaf(0), leaf(0),
			}}}},
			wantErr: true,
		},
		{
			name: "left child out of range",
			model: TreeModel{Features: 1, Trees: []Tree{{Nodes: []Node{
				{Feature: 0, Threshold: 0, Left: 3, Right: 1},
				leaf(0), leaf(0),
			}}}},
			wantErr: true,
		},
		{
			name: "negative right child",
			model: TreeModel{Features: 1, Trees: []Tree{{Nodes: []Node{
				{Feature: 0, Threshold: 0, Left: 1, Right: -2},
				leaf(0), leaf(0),
			}}}},
			wantErr: true,
		},
		{
			name: "right child out of range",
			model: TreeModel{Features: 1, Trees: []Tree{{Nodes: []Node{
				{Feature: 0, Threshold: 0, Left: 1, Right: 3},
				leaf(0), leaf(0),
			}}}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.model)
			if err != nil {
				t.Fatalf("marshal model: %v", err)
			}

			got, err := ParseTreeModel(data)
			if tt.wantErr {
				if !errors.Is(err, ErrModelInvalid) {
					t.Fatalf("ParseTreeModel() error = %v, want ErrModelInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTreeModel() error = %v", err)
			}
			if got.Version != tt.model.Version || got.Features != tt.model.Features {
				t.Errorf("ParseTreeModel() = %+v, want %+v", got, &tt.model)
			}
		})
	}
}

func TestParseTreeModelInvalidJSON(t *testing.T) {
	if _, err := ParseTreeModel([]byte("{ not json")); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("ParseTreeModel() error = %v, want ErrModelInvalid", err)
	}
}

func TestTreeModelScore(t *testing.T) {
	tests := []struct {
		name     string
		model    TreeModel
		features []float64
		want     float64
		wantErr  bool
	}{
		{
			name: "zero total scores one half",
			model: TreeModel{
				Features: 1,
				Trees:    []Tree{{Nodes: []Node{leaf(0)}}},
			},
			features: []float64{1},
			want:     0.5,
		},
		{
			name: "positive total squashes through sigmoid",
			model: TreeModel{
				Features: 2,
				Baseline: 1,
				Trees: []Tree{{Nodes: []Node{
					{Feature: 0, Threshold: 100, Left: 1, Right: 2},
					leaf(2),
					leaf(-2),
				}}},
			},
			features: []float64{50, 0},
			want:     0.9525741268224334, // sigmoid(3)
		},
		{
			name: "negative total uses negative sigmoid branch",
			model: TreeModel{
				Features: 1,
				Baseline: -2,
				Trees:    []Tree{{Nodes: []Node{leaf(0)}}},
			},
			features: []float64{1},
			want:     0.11920292202211755, // sigmoid(-2)
		},
		{
			name: "score above exp cutoff returns one",
			model: TreeModel{
				Features: 1,
				Baseline: 31,
				Trees:    []Tree{{Nodes: []Node{leaf(0)}}},
			},
			features: []float64{1},
			want:     1,
		},
		{
			name: "ensemble sums every tree leaf",
			model: TreeModel{
				Features: 1,
				Trees: []Tree{
					{Nodes: []Node{leaf(0.25)}},
					{Nodes: []Node{leaf(0.25)}},
				},
			},
			features: []float64{1},
			want:     0.6224593312018546, // sigmoid(0.5)
		},
		{
			name: "wrong feature count is rejected",
			model: TreeModel{
				Features: 5,
				Trees:    []Tree{{Nodes: []Node{leaf(0)}}},
			},
			features: []float64{1, 2, 3, 4},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.model.Score(tt.features)
			if tt.wantErr {
				if !errors.Is(err, ErrModelInvalid) {
					t.Fatalf("Score() error = %v, want ErrModelInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Score() error = %v", err)
			}
			diff := got - tt.want
			if diff < 0 {
				diff = -diff
			}
			if diff > 1e-9 {
				t.Errorf("Score() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTreeEval(t *testing.T) {
	tree := Tree{Nodes: []Node{
		{Feature: 0, Threshold: 100, Left: 1, Right: 2},
		leaf(0.5),
		leaf(-0.5),
	}}

	tests := []struct {
		name     string
		features []float64
		want     float64
	}{
		{"feature at or below threshold goes left", []float64{100, 0}, 0.5},
		{"feature above threshold goes right", []float64{101, 0}, -0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tree.eval(tt.features); got != tt.want {
				t.Errorf("eval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTreeEvalLeafOnly(t *testing.T) {
	if got := (Tree{Nodes: []Node{leaf(0.75)}}).eval(nil); got != 0.75 {
		t.Errorf("eval() = %v, want 0.75", got)
	}
}
