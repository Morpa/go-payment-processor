package domain

import (
	"slices"
	"testing"
	"time"
)

func TestFeatureVector(t *testing.T) {
	at := func(hour int, country, mcc string) Attempt {
		return Attempt{
			AmountCents:      12345,
			VelocityLastMin:  3,
			OccurredAt:       time.Date(2026, 1, 1, hour, 0, 0, 0, time.UTC),
			CountryCode:      country,
			MerchantCategory: mcc,
		}
	}

	tests := []struct {
		name    string
		attempt Attempt
		want    []float64
	}{
		{
			name:    "all risk flags set",
			attempt: at(2, "US", "gambling"),
			want:    []float64{12345, 3, 1, 1, 1},
		},
		{
			name:    "domestic daytime low risk",
			attempt: at(12, "BR", "groceries"),
			want:    []float64{12345, 3, 0, 0, 0},
		},
		{
			name:    "empty country counts as domestic",
			attempt: at(12, "", "retail"),
			want:    []float64{12345, 3, 0, 0, 0},
		},
		{
			name:    "hour five is night",
			attempt: at(5, "BR", "retail"),
			want:    []float64{12345, 3, 1, 0, 0},
		},
		{
			name:    "hour six is not night",
			attempt: at(6, "BR", "retail"),
			want:    []float64{12345, 3, 0, 0, 0},
		},
		{
			name:    "every high risk category flags",
			attempt: at(12, "BR", "money_transfer"),
			want:    []float64{12345, 3, 0, 0, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FeatureVector(tt.attempt)
			if !slices.Equal(got, tt.want) {
				t.Errorf("FeatureVector() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFeatureVectorHasFeatureCountSlots(t *testing.T) {
	v := FeatureVector(Attempt{})
	if len(v) != FeatureCount {
		t.Errorf("len(FeatureVector()) = %d, want %d", len(v), FeatureCount)
	}
}

func TestVerdictFromScore(t *testing.T) {
	tests := []struct {
		name  string
		score float64
		want  Verdict
	}{
		{"negative score approves", -0.1, VerdictApprove},
		{"just below approve threshold", 0.34, VerdictApprove},
		{"exactly at approve threshold", ApproveBelow, VerdictManualReview},
		{"mid band", 0.5, VerdictManualReview},
		{"exactly at deny threshold", DenyAbove, VerdictManualReview},
		{"just above deny threshold", 0.76, VerdictDeny},
		{"max score denies", 1.0, VerdictDeny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VerdictFromScore(tt.score); got != tt.want {
				t.Errorf("VerdictFromScore(%v) = %v, want %v", tt.score, got, tt.want)
			}
		})
	}
}
