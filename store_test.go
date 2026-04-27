package main

import "testing"

func TestSelectBestCandidateRanksByBottleneckThenWindows(t *testing.T) {
	tests := []struct {
		name       string
		candidates []TokenCandidate
		wantID     string
	}{
		{
			name: "avoids weekly bottleneck despite higher five hour remaining",
			candidates: []TokenCandidate{
				{
					Token:                   TokenState{ID: "weekly-nearly-exhausted"},
					FiveHourRemainingPoints: 90,
					WeeklyRemainingPoints:   2,
				},
				{
					Token:                   TokenState{ID: "balanced"},
					FiveHourRemainingPoints: 85,
					WeeklyRemainingPoints:   80,
				},
			},
			wantID: "balanced",
		},
		{
			name: "uses five hour remaining as bottleneck tie breaker",
			candidates: []TokenCandidate{
				{
					Token:                   TokenState{ID: "lower-five-hour"},
					FiveHourRemainingPoints: 60,
					WeeklyRemainingPoints:   90,
				},
				{
					Token:                   TokenState{ID: "higher-five-hour"},
					FiveHourRemainingPoints: 80,
					WeeklyRemainingPoints:   60,
				},
			},
			wantID: "higher-five-hour",
		},
		{
			name: "uses weekly remaining when bottleneck and five hour tie",
			candidates: []TokenCandidate{
				{
					Token:                   TokenState{ID: "lower-weekly"},
					FiveHourRemainingPoints: 80,
					WeeklyRemainingPoints:   90,
				},
				{
					Token:                   TokenState{ID: "higher-weekly"},
					FiveHourRemainingPoints: 80,
					WeeklyRemainingPoints:   100,
				},
			},
			wantID: "higher-weekly",
		},
		{
			name: "uses token id as final stable tie breaker",
			candidates: []TokenCandidate{
				{
					Token:                   TokenState{ID: "b-token"},
					FiveHourRemainingPoints: 80,
					WeeklyRemainingPoints:   90,
				},
				{
					Token:                   TokenState{ID: "a-token"},
					FiveHourRemainingPoints: 80,
					WeeklyRemainingPoints:   90,
				},
			},
			wantID: "a-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectBestCandidate(tt.candidates)
			if got.ID != tt.wantID {
				t.Fatalf("selectBestCandidate() = %q, want %q", got.ID, tt.wantID)
			}
		})
	}
}
