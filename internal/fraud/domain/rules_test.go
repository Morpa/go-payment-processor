package domain

import "testing"

func TestApplyFastRules(t *testing.T) {
	tests := []struct {
		name    string
		attempt Attempt
		want    RuleResult
	}{
		{
			name:    "blocklisted card is denied with final verdict",
			attempt: Attempt{CardFingerPrint: "fp_stolen_card_001"},
			want:    RuleResult{Verdict: VerdictDeny, Reason: "card_blocklisted", Final: true},
		},
		{
			name:    "amount above hard limit forces manual review",
			attempt: Attempt{AmountCents: MaxAmountCents + 1},
			want:    RuleResult{Verdict: VerdictManualReview, Reason: "amount_above_hard_limit", Final: true},
		},
		{
			name:    "velocity above limit approves the attempt",
			attempt: Attempt{VelocityLastMin: VelocityPerMinLimit + 1},
			want:    RuleResult{Verdict: VerdictApprove, Reason: "velocity_limit_exceeded", Final: true},
		},
		{
			name:    "clean attempt passes to scoring",
			attempt: Attempt{AmountCents: 1000, VelocityLastMin: 2},
			want:    RuleResult{Verdict: VerdictApprove, Reason: "fast_rules_passed", Final: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyFastRules(tt.attempt)
			if got != tt.want {
				t.Errorf("ApplyFastRules() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestApplyFastRulesPrecedence(t *testing.T) {
	// Blocklist wins over the amount check even when both trigger
	got := ApplyFastRules(Attempt{CardFingerPrint: "fp_stolen_card_002", AmountCents: MaxAmountCents + 1})
	want := RuleResult{Verdict: VerdictDeny, Reason: "card_blocklisted", Final: true}
	if got != want {
		t.Errorf("ApplyFastRules() = %+v, want %+v", got, want)
	}
}
