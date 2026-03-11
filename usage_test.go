package main

import "testing"

func TestMapUsageSnapshot(t *testing.T) {
	tests := []struct {
		name              string
		payload           rateLimitStatusPayload
		wantUserID        string
		wantAccountID     string
		wantEmail         string
		wantFiveHourKnown bool
		wantWeeklyKnown   bool
		wantFiveHourUsed  float64
		wantWeeklyUsed    float64
		wantPlanType      string
	}{
		{
			name: "dual windows from primary payload",
			payload: rateLimitStatusPayload{
				UserID:    "user-1",
				AccountID: "account-1",
				Email:     "user@example.com",
				PlanType:  "plus",
				RateLimit: &rateLimitStatusDetails{
					PrimaryWindow: &rateLimitWindowSnapshot{
						UsedPercent:        12,
						LimitWindowSeconds: 18000,
					},
					SecondaryWindow: &rateLimitWindowSnapshot{
						UsedPercent:        48,
						LimitWindowSeconds: 604800,
					},
				},
			},
			wantUserID:        "user-1",
			wantAccountID:     "account-1",
			wantEmail:         "user@example.com",
			wantFiveHourKnown: true,
			wantWeeklyKnown:   true,
			wantFiveHourUsed:  12,
			wantWeeklyUsed:    48,
			wantPlanType:      "plus",
		},
		{
			name: "single short window is treated as five hour",
			payload: rateLimitStatusPayload{
				RateLimit: &rateLimitStatusDetails{
					PrimaryWindow: &rateLimitWindowSnapshot{
						UsedPercent:        25,
						LimitWindowSeconds: 3600,
					},
				},
			},
			wantFiveHourKnown: true,
			wantWeeklyKnown:   false,
			wantFiveHourUsed:  25,
		},
		{
			name: "single unknown window is treated as weekly",
			payload: rateLimitStatusPayload{
				RateLimit: &rateLimitStatusDetails{
					PrimaryWindow: &rateLimitWindowSnapshot{
						UsedPercent: 64,
					},
				},
			},
			wantFiveHourKnown: false,
			wantWeeklyKnown:   true,
			wantWeeklyUsed:    64,
		},
		{
			name: "weekly window from additional rate limits",
			payload: rateLimitStatusPayload{
				AdditionalRateLimits: []additionalRateLimitDetails{
					{
						LimitName:      "codex_other",
						MeteredFeature: "codex_other",
						RateLimit: &rateLimitStatusDetails{
							PrimaryWindow: &rateLimitWindowSnapshot{
								UsedPercent:        80,
								LimitWindowSeconds: 604800,
							},
						},
					},
				},
			},
			wantFiveHourKnown: false,
			wantWeeklyKnown:   true,
			wantWeeklyUsed:    80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapUsageSnapshot(tt.payload)
			if got.UserID != tt.wantUserID {
				t.Fatalf("UserID = %q, want %q", got.UserID, tt.wantUserID)
			}
			if got.AccountID != tt.wantAccountID {
				t.Fatalf("AccountID = %q, want %q", got.AccountID, tt.wantAccountID)
			}
			if got.Email != tt.wantEmail {
				t.Fatalf("Email = %q, want %q", got.Email, tt.wantEmail)
			}
			if got.FiveHour.Known != tt.wantFiveHourKnown {
				t.Fatalf("FiveHour.Known = %v, want %v", got.FiveHour.Known, tt.wantFiveHourKnown)
			}
			if got.Weekly.Known != tt.wantWeeklyKnown {
				t.Fatalf("Weekly.Known = %v, want %v", got.Weekly.Known, tt.wantWeeklyKnown)
			}
			if tt.wantFiveHourKnown && got.FiveHour.UsedPercent != tt.wantFiveHourUsed {
				t.Fatalf("FiveHour.UsedPercent = %v, want %v", got.FiveHour.UsedPercent, tt.wantFiveHourUsed)
			}
			if tt.wantWeeklyKnown && got.Weekly.UsedPercent != tt.wantWeeklyUsed {
				t.Fatalf("Weekly.UsedPercent = %v, want %v", got.Weekly.UsedPercent, tt.wantWeeklyUsed)
			}
			if got.PlanType != tt.wantPlanType {
				t.Fatalf("PlanType = %q, want %q", got.PlanType, tt.wantPlanType)
			}
		})
	}
}
