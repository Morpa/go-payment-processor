package usecase_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/Morpa/go-payment-processor/internal/fraud/domain"
	"github.com/Morpa/go-payment-processor/internal/usecase"
	"github.com/Morpa/go-payment-processor/pkg/fsm"
	"github.com/stretchr/testify/mock"
)

type mockGeoResolver struct {
	mock.Mock
}

func (m *mockGeoResolver) CountryCode(ctx context.Context, ip string) (string, error) {
	args := m.Called(ctx, ip)
	return args.String(0), args.Error(1)
}

type mockEmbedder struct {
	mock.Mock
}

func (m *mockEmbedder) Embed(text string) []float64 {
	args := m.Called(text)
	if v, ok := args.Get(0).([]float64); ok {
		return v
	}
	return nil
}

type mockSemanticCache struct {
	mock.Mock
}

func (m *mockSemanticCache) Lookup(vector []float64) (domain.Verdict, float64, bool) {
	args := m.Called(vector)
	var verdict domain.Verdict
	if v, ok := args.Get(0).(domain.Verdict); ok {
		verdict = v
	}
	var similarity float64
	if s, ok := args.Get(1).(float64); ok {
		similarity = s
	}
	return verdict, similarity, args.Bool(2)
}

func (m *mockSemanticCache) Store(vector []float64, verdict domain.Verdict) {
	m.Called(vector, verdict)
}

func newTestModel(rawScore float64) *domain.TreeModel {
	return &domain.TreeModel{
		Version:  "1.0.0",
		Features: domain.FeatureCount,
		Baseline: rawScore,
		Trees: []domain.Tree{
			{
				Nodes: []domain.Node{
					{Left: -1, Value: 0.0},
				},
			},
		},
	}
}

func TestPipelineScore_SuccessScenarios(t *testing.T) {
	approveModel := newTestModel(-2.0)
	approveScore, _ := approveModel.Score(make([]float64, domain.FeatureCount))

	denyModel := newTestModel(2.0)
	denyScore, _ := denyModel.Score(make([]float64, domain.FeatureCount))

	ambiguousModel := newTestModel(0.0)
	ambiguousScore, _ := ambiguousModel.Score(make([]float64, domain.FeatureCount))

	tests := []struct {
		name             string
		attempt          domain.Attempt
		setupAttempts    int
		model            *domain.TreeModel
		setupMocks       func(geo *mockGeoResolver, embedder *mockEmbedder, semCache *mockSemanticCache)
		expectedDecision usecase.Decision
		expectedStates   []fsm.State
	}{
		{
			name: "fast rules blocklisted card stops immediately at done",
			attempt: domain.Attempt{
				PaymentID:       "pay_blocked_001",
				CardFingerPrint: "fp_stolen_card_001",
				AmountCents:     15000,
				ClientIP:        "192.168.1.1",
				OccurredAt:      time.Now(),
			},
			setupMocks: func(geo *mockGeoResolver, _ *mockEmbedder, _ *mockSemanticCache) {
				geo.On("CountryCode", mock.Anything, "192.168.1.1").Return("BR", nil).Once()
			},
			expectedDecision: usecase.Decision{
				Verdict:   domain.VerdictDeny,
				RiskScore: 0.0,
				Reasons:   []string{"card_blocklisted"},
				Degraded:  true,
			},
			expectedStates: []fsm.State{
				usecase.StateEnrich,
				usecase.StateFastRules,
				usecase.StateDone,
			},
		},
		{
			name: "fast rules velocity limit exceeded stops immediately at done",
			attempt: domain.Attempt{
				PaymentID:       "pay_velocity_001",
				CardFingerPrint: "fp_velocity_card_001",
				AmountCents:     10000,
				ClientIP:        "192.168.1.2",
				OccurredAt:      time.Now(),
			},
			model:         approveModel,
			setupAttempts: domain.VelocityPerMinLimit,
			setupMocks: func(geo *mockGeoResolver, _ *mockEmbedder, _ *mockSemanticCache) {
				geo.On("CountryCode", mock.Anything, "").Return("BR", nil).Times(domain.VelocityPerMinLimit)
				geo.On("CountryCode", mock.Anything, "192.168.1.2").Return("BR", nil).Once()
			},
			expectedDecision: usecase.Decision{
				Verdict:   domain.VerdictDeny,
				RiskScore: 0.0,
				Reasons:   []string{"velocity_limit_exceeded"},
				Degraded:  true,
			},
			expectedStates: []fsm.State{
				usecase.StateEnrich,
				usecase.StateFastRules,
				usecase.StateDone,
			},
		},
		{
			name: "ml score confident approve",
			attempt: domain.Attempt{
				PaymentID:       "pay_ml_app_001",
				CardFingerPrint: "fp_clean_card_001",
				AmountCents:     5000,
				ClientIP:        "192.168.1.3",
				OccurredAt:      time.Now(),
			},
			model: approveModel,
			setupMocks: func(geo *mockGeoResolver, _ *mockEmbedder, _ *mockSemanticCache) {
				geo.On("CountryCode", mock.Anything, "192.168.1.3").Return("BR", nil).Once()
			},
			expectedDecision: usecase.Decision{
				Verdict:   domain.VerdictApprove,
				RiskScore: approveScore,
				Reasons:   []string{"fast_rules_passed", "ml_score_confident"},
				Degraded:  false,
			},
			expectedStates: []fsm.State{
				usecase.StateEnrich,
				usecase.StateFastRules,
				usecase.StateMLScore,
				usecase.StateFinalize,
				usecase.StateDone,
			},
		},
		{
			name: "ml score confident deny",
			attempt: domain.Attempt{
				PaymentID:       "pay_ml_deny_001",
				CardFingerPrint: "fp_clean_card_002",
				AmountCents:     5000,
				ClientIP:        "192.168.1.4",
				OccurredAt:      time.Now(),
			},
			model: denyModel,
			setupMocks: func(geo *mockGeoResolver, _ *mockEmbedder, _ *mockSemanticCache) {
				geo.On("CountryCode", mock.Anything, "192.168.1.4").Return("BR", nil).Once()
			},
			expectedDecision: usecase.Decision{
				Verdict:   domain.VerdictDeny,
				RiskScore: denyScore,
				Reasons:   []string{"fast_rules_passed", "ml_score_confident"},
				Degraded:  false,
			},
			expectedStates: []fsm.State{
				usecase.StateEnrich,
				usecase.StateFastRules,
				usecase.StateMLScore,
				usecase.StateFinalize,
				usecase.StateDone,
			},
		},
		{
			name: "ambiguous ml score resolved by semantic cache hit",
			attempt: domain.Attempt{
				PaymentID:        "pay_tiebreak_hit_001",
				CardFingerPrint:  "fp_clean_card_003",
				AmountCents:      50000,
				MerchantCategory: "electronics",
				ClientIP:         "192.168.1.5",
				OccurredAt:       time.Now(),
			},
			model: ambiguousModel,
			setupMocks: func(geo *mockGeoResolver, embedder *mockEmbedder, semCache *mockSemanticCache) {
				geo.On("CountryCode", mock.Anything, "192.168.1.5").Return("BR", nil).Once()
				embedder.On("Embed", mock.AnythingOfType("string")).Return([]float64{0.1, 0.2, 0.3}).Once()
				semCache.On("Lookup", mock.AnythingOfType("[]float64")).Return(domain.VerdictApprove, 0.95, true).Once()
				semCache.On("Store", mock.AnythingOfType("[]float64"), domain.VerdictApprove).Return().Once()
			},
			expectedDecision: usecase.Decision{
				Verdict:   domain.VerdictApprove,
				RiskScore: ambiguousScore,
				Reasons:   []string{"fast_rules_passed", "semantic_cache_hit:0.95"},
				Degraded:  false,
			},
			expectedStates: []fsm.State{
				usecase.StateEnrich,
				usecase.StateFastRules,
				usecase.StateMLScore,
				usecase.StateTiebreak,
				usecase.StateFinalize,
				usecase.StateDone,
			},
		},
		{
			name: "ambiguous ml score semantic cache miss defaults to manual review",
			attempt: domain.Attempt{
				PaymentID:        "pay_tiebreak_miss_001",
				CardFingerPrint:  "fp_clean_card_004",
				AmountCents:      50000,
				MerchantCategory: "electronics",
				ClientIP:         "192.168.1.6",
				OccurredAt:       time.Now(),
			},
			model: ambiguousModel,
			setupMocks: func(geo *mockGeoResolver, embedder *mockEmbedder, semCache *mockSemanticCache) {
				geo.On("CountryCode", mock.Anything, "192.168.1.6").Return("BR", nil).Once()
				embedder.On("Embed", mock.AnythingOfType("string")).Return([]float64{0.1, 0.2, 0.3}).Once()
				semCache.On("Lookup", mock.AnythingOfType("[]float64")).Return(domain.Verdict(""), 0.0, false).Once()
				semCache.On("Store", mock.AnythingOfType("[]float64"), domain.VerdictManualReview).Return().Once()
			},
			expectedDecision: usecase.Decision{
				Verdict:   domain.VerdictManualReview,
				RiskScore: ambiguousScore,
				Reasons:   []string{"fast_rules_passed", "ambiguous_score_manual_review"},
				Degraded:  false,
			},
			expectedStates: []fsm.State{
				usecase.StateEnrich,
				usecase.StateFastRules,
				usecase.StateMLScore,
				usecase.StateTiebreak,
				usecase.StateFinalize,
				usecase.StateDone,
			},
		},
		{
			name: "geo lookup failure continues with empty country code and scores via ml",
			attempt: domain.Attempt{
				PaymentID:       "pay_geo_fail_001",
				CardFingerPrint: "fp_clean_card_005",
				AmountCents:     5000,
				ClientIP:        "192.168.1.7",
				OccurredAt:      time.Now(),
			},
			model: approveModel,
			setupMocks: func(geo *mockGeoResolver, _ *mockEmbedder, _ *mockSemanticCache) {
				geo.On("CountryCode", mock.Anything, "192.168.1.7").Return("", errors.New("geo timeout")).Once()
			},
			expectedDecision: usecase.Decision{
				Verdict:   domain.VerdictApprove,
				RiskScore: approveScore,
				Reasons:   []string{"fast_rules_passed", "ml_score_confident"},
				Degraded:  false,
			},
			expectedStates: []fsm.State{
				usecase.StateEnrich,
				usecase.StateFastRules,
				usecase.StateMLScore,
				usecase.StateFinalize,
				usecase.StateDone,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := tt.model
			if model == nil {
				model = newTestModel(0.0)
			}

			geo := new(mockGeoResolver)
			embedder := new(mockEmbedder)
			semCache := new(mockSemanticCache)

			if tt.setupMocks != nil {
				tt.setupMocks(geo, embedder, semCache)
			}

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			pipeline := usecase.NewPipeline(model, geo, embedder, semCache, logger, 1, 10*time.Millisecond)
			defer pipeline.Close()

			if tt.setupAttempts > 0 {
				for i := range tt.setupAttempts {
					_, _, err := pipeline.Score(t.Context(), domain.Attempt{
						CardFingerPrint: tt.attempt.CardFingerPrint,
						AmountCents:     1000,
						OccurredAt:      time.Now(),
					})
					if err != nil {
						t.Fatalf("setup attempt %d failed: %v", i, err)
					}
				}
			}

			gotDecision, trace, err := pipeline.Score(t.Context(), tt.attempt)
			if err != nil {
				t.Fatalf("Score() unexpected error: %v", err)
			}

			if gotDecision.Verdict != tt.expectedDecision.Verdict {
				t.Errorf("Verdict = %q, expected %q", gotDecision.Verdict, tt.expectedDecision.Verdict)
			}
			if math.Abs(gotDecision.RiskScore-tt.expectedDecision.RiskScore) > 1e-4 {
				t.Errorf("RiskScore = %f, expected %f", gotDecision.RiskScore, tt.expectedDecision.RiskScore)
			}
			if !slices.Equal(gotDecision.Reasons, tt.expectedDecision.Reasons) {
				t.Errorf("Reasons = %v, expected %v", gotDecision.Reasons, tt.expectedDecision.Reasons)
			}
			if gotDecision.Degraded != tt.expectedDecision.Degraded {
				t.Errorf("Degraded = %v, expected %v", gotDecision.Degraded, tt.expectedDecision.Degraded)
			}

			gotStates := make([]fsm.State, 0, len(trace)+1)
			if len(trace) > 0 {
				gotStates = append(gotStates, trace[0].From)
				for _, hop := range trace {
					if hop.Err != nil {
						t.Errorf("transition from %q to %q had unexpected error: %v", hop.From, hop.To, hop.Err)
					}
					gotStates = append(gotStates, hop.To)
				}
			}

			if !slices.Equal(gotStates, tt.expectedStates) {
				t.Errorf("transition trace states = %v, expected %v", gotStates, tt.expectedStates)
			}

			geo.AssertExpectations(t)
			embedder.AssertExpectations(t)
			semCache.AssertExpectations(t)
		})
	}
}

func TestPipelineScore_ErrorAndDegradationScenarios(t *testing.T) {
	approveModel := newTestModel(-2.0)
	approveScore, _ := approveModel.Score(make([]float64, domain.FeatureCount))

	// Model with invalid feature count causes model.Score to fail in batcher
	brokenModel := &domain.TreeModel{
		Version:  "1.0.0",
		Features: domain.FeatureCount - 1,
		Baseline: 0.0,
		Trees: []domain.Tree{
			{
				Nodes: []domain.Node{
					{Left: -1, Value: 0.0},
				},
			},
		},
	}

	tests := []struct {
		name             string
		attempt          domain.Attempt
		model            *domain.TreeModel
		setupMocks       func(geo *mockGeoResolver, embedder *mockEmbedder, semCache *mockSemanticCache)
		beforeScore      func(p *usecase.Pipeline)
		ctxFunc          func(t *testing.T) context.Context
		expectedDecision usecase.Decision
		expectedStates   []fsm.State
		expectError      bool
		checkTraceErrors func(t *testing.T, trace []fsm.Transition)
	}{
		{
			name: "geo resolver failure proceeds with empty country code without error",
			attempt: domain.Attempt{
				PaymentID:       "pay_err_geo_001",
				CardFingerPrint: "fp_clean_geo_err",
				AmountCents:     5000,
				ClientIP:        "192.168.1.100",
				OccurredAt:      time.Now(),
			},
			model: approveModel,
			setupMocks: func(geo *mockGeoResolver, _ *mockEmbedder, _ *mockSemanticCache) {
				geo.On("CountryCode", mock.Anything, "192.168.1.100").
					Return("", errors.New("geo resolver connection timeout")).Once()
			},
			expectedDecision: usecase.Decision{
				Verdict:   domain.VerdictApprove,
				RiskScore: approveScore,
				Reasons:   []string{"fast_rules_passed", "ml_score_confident"},
				Degraded:  false,
			},
			expectedStates: []fsm.State{
				usecase.StateEnrich,
				usecase.StateFastRules,
				usecase.StateMLScore,
				usecase.StateFinalize,
				usecase.StateDone,
			},
			expectError: false,
			checkTraceErrors: func(t *testing.T, trace []fsm.Transition) {
				for _, tr := range trace {
					if tr.Err != nil {
						t.Errorf("transition %s -> %s had unexpected error: %v", tr.From, tr.To, tr.Err)
					}
				}
			},
		},
		{
			name: "ml scoring failure falls back to finalize with manual review and degraded flag",
			attempt: domain.Attempt{
				PaymentID:       "pay_err_ml_001",
				CardFingerPrint: "fp_clean_ml_err",
				AmountCents:     7500,
				ClientIP:        "192.168.1.101",
				OccurredAt:      time.Now(),
			},
			model: brokenModel,
			setupMocks: func(geo *mockGeoResolver, _ *mockEmbedder, _ *mockSemanticCache) {
				geo.On("CountryCode", mock.Anything, "192.168.1.101").Return("BR", nil).Once()
			},
			expectedDecision: usecase.Decision{
				Verdict:   domain.VerdictManualReview,
				RiskScore: 0.0,
				Reasons:   []string{"fast_rules_passed", "ml_stage_skipped"},
				Degraded:  true,
			},
			expectedStates: []fsm.State{
				usecase.StateEnrich,
				usecase.StateFastRules,
				usecase.StateMLScore,
				usecase.StateFinalize,
				usecase.StateDone,
			},
			expectError: false,
			checkTraceErrors: func(t *testing.T, trace []fsm.Transition) {
				if len(trace) < 3 {
					t.Fatalf("expected at least 3 transitions, got %d", len(trace))
				}
				mlHop := trace[2]
				if mlHop.From != usecase.StateMLScore {
					t.Errorf("expected transition 2 to be from %q, got %q", usecase.StateMLScore, mlHop.From)
				}
				if mlHop.Err == nil {
					t.Errorf("expected MLScore transition to record an error, got nil")
				}
				finalizeHop := trace[3]
				if finalizeHop.From != usecase.StateFinalize {
					t.Errorf("expected fallback to %q, got %q", usecase.StateFinalize, finalizeHop.From)
				}
			},
		},
		{
			name: "velocity cache failure in enrich returns error and aborts pipeline",
			attempt: domain.Attempt{
				PaymentID:       "pay_err_velocity_001",
				CardFingerPrint: "fp_clean_vel_err",
				AmountCents:     5000,
				ClientIP:        "192.168.1.102",
				OccurredAt:      time.Now(),
			},
			model: approveModel,
			beforeScore: func(p *usecase.Pipeline) {
				p.Close()
			},
			expectedDecision: usecase.Decision{},
			expectError:      true,
			checkTraceErrors: func(t *testing.T, trace []fsm.Transition) {
				if len(trace) != 1 {
					t.Fatalf("expected exactly 1 transition on enrich failure, got %d", len(trace))
				}
				if trace[0].From != usecase.StateEnrich {
					t.Errorf("expected failed transition from %q, got %q", usecase.StateEnrich, trace[0].From)
				}
				if trace[0].Err == nil {
					t.Errorf("expected enrich transition to contain error")
				}
			},
		},
		{
			name: "context cancellation aborts pipeline and returns error",
			attempt: domain.Attempt{
				PaymentID:       "pay_err_ctx_001",
				CardFingerPrint: "fp_clean_ctx_err",
				AmountCents:     5000,
				ClientIP:        "192.168.1.103",
				OccurredAt:      time.Now(),
			},
			model: approveModel,
			ctxFunc: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			expectedDecision: usecase.Decision{},
			expectError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := tt.model
			if model == nil {
				model = newTestModel(0.0)
			}

			geo := new(mockGeoResolver)
			embedder := new(mockEmbedder)
			semCache := new(mockSemanticCache)

			if tt.setupMocks != nil {
				tt.setupMocks(geo, embedder, semCache)
			}

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			pipeline := usecase.NewPipeline(model, geo, embedder, semCache, logger, 1, 10*time.Millisecond)
			defer pipeline.Close()

			if tt.beforeScore != nil {
				tt.beforeScore(pipeline)
			}

			ctx := t.Context()
			if tt.ctxFunc != nil {
				ctx = tt.ctxFunc(t)
			}

			gotDecision, trace, err := pipeline.Score(ctx, tt.attempt)

			if tt.expectError {
				if err == nil {
					t.Fatalf("Score() expected error, got nil")
				}
				if gotDecision.Verdict != "" {
					t.Errorf("expected empty Verdict on error, got %q", gotDecision.Verdict)
				}
				if tt.checkTraceErrors != nil {
					tt.checkTraceErrors(t, trace)
				}
				return
			}

			if err != nil {
				t.Fatalf("Score() unexpected error: %v", err)
			}

			if gotDecision.Verdict == "" {
				t.Errorf("Decision.Verdict must not be empty")
			}
			if gotDecision.Verdict != tt.expectedDecision.Verdict {
				t.Errorf("Verdict = %q, expected %q", gotDecision.Verdict, tt.expectedDecision.Verdict)
			}
			if math.Abs(gotDecision.RiskScore-tt.expectedDecision.RiskScore) > 1e-4 {
				t.Errorf("RiskScore = %f, expected %f", gotDecision.RiskScore, tt.expectedDecision.RiskScore)
			}
			if !slices.Equal(gotDecision.Reasons, tt.expectedDecision.Reasons) {
				t.Errorf("Reasons = %v, expected %v", gotDecision.Reasons, tt.expectedDecision.Reasons)
			}
			if gotDecision.Degraded != tt.expectedDecision.Degraded {
				t.Errorf("Degraded = %v, expected %v", gotDecision.Degraded, tt.expectedDecision.Degraded)
			}

			if len(tt.expectedStates) > 0 {
				gotStates := make([]fsm.State, 0, len(trace)+1)
				if len(trace) > 0 {
					gotStates = append(gotStates, trace[0].From)
					for _, hop := range trace {
						gotStates = append(gotStates, hop.To)
					}
				}
				if tt.checkTraceErrors != nil {
					tt.checkTraceErrors(t, trace)
				}
			}

			geo.AssertExpectations(t)
			embedder.AssertExpectations(t)
			semCache.AssertExpectations(t)
		})
	}
}
