package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

type rateLimitStatusPayload struct {
	RateLimit            *rateLimitStatusDetails      `json:"rate_limit"`
	AdditionalRateLimits []additionalRateLimitDetails `json:"additional_rate_limits"`
}

type rateLimitStatusDetails struct {
	Allowed         bool                     `json:"allowed"`
	LimitReached    bool                     `json:"limit_reached"`
	PrimaryWindow   *rateLimitWindowSnapshot `json:"primary_window"`
	SecondaryWindow *rateLimitWindowSnapshot `json:"secondary_window"`
}

type rateLimitWindowSnapshot struct {
	UsedPercent        int `json:"used_percent"`
	LimitWindowSeconds int `json:"limit_window_seconds"`
	ResetAfterSeconds  int `json:"reset_after_seconds"`
	ResetAt            int `json:"reset_at"`
}

type additionalRateLimitDetails struct {
	LimitName      string                  `json:"limit_name"`
	MeteredFeature string                  `json:"metered_feature"`
	RateLimit      *rateLimitStatusDetails `json:"rate_limit"`
}

type UsageSnapshot struct {
	FiveHour WindowUsage
	Weekly   WindowUsage
}

var errUnauthorized = errors.New("unauthorized")

func fetchUsage(ctx context.Context, client *http.Client, ref TokenRef) (UsageSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL(), nil)
	if err != nil {
		return UsageSnapshot{}, fmt.Errorf("build usage request: %w", err)
	}
	req.Header.Set("Authorization", authHeaderValue(ref.Token))
	if ref.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", ref.AccountID)
	}

	resp, err := client.Do(req)
	if err != nil {
		return UsageSnapshot{}, fmt.Errorf("send usage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return UsageSnapshot{}, errUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return UsageSnapshot{}, fmt.Errorf("usage request status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return UsageSnapshot{}, fmt.Errorf("read usage response: %w", err)
	}

	var payload rateLimitStatusPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return UsageSnapshot{}, fmt.Errorf("decode usage response: %w", err)
	}

	return mapUsageSnapshot(payload), nil
}

func usageURL() string {
	return backendEndpoint("/wham/usage")
}

func mapUsageSnapshot(payload rateLimitStatusPayload) UsageSnapshot {
	detailsList := make([]*rateLimitStatusDetails, 0, 1+len(payload.AdditionalRateLimits))
	if payload.RateLimit != nil {
		detailsList = append(detailsList, payload.RateLimit)
	}
	for _, additional := range payload.AdditionalRateLimits {
		if additional.RateLimit == nil {
			continue
		}
		detailsList = append(detailsList, additional.RateLimit)
	}
	if len(detailsList) == 0 {
		return UsageSnapshot{}
	}

	var snapshot UsageSnapshot
	for _, details := range detailsList {
		current := mapUsageSnapshotFromDetails(details)
		if !snapshot.FiveHour.Known && current.FiveHour.Known {
			snapshot.FiveHour = current.FiveHour
		}
		if !snapshot.Weekly.Known && current.Weekly.Known {
			snapshot.Weekly = current.Weekly
		}
		if snapshot.FiveHour.Known && snapshot.Weekly.Known {
			break
		}
	}
	return snapshot
}

func mapUsageSnapshotFromDetails(details *rateLimitStatusDetails) UsageSnapshot {
	if details == nil {
		return UsageSnapshot{}
	}

	windows := make([]*rateLimitWindowSnapshot, 0, 2)
	if details.PrimaryWindow != nil {
		windows = append(windows, details.PrimaryWindow)
	}
	if details.SecondaryWindow != nil {
		windows = append(windows, details.SecondaryWindow)
	}

	fiveHour, hasFiveHour := pickSnapshotWindow(windows, 18000, 14400, 21600)
	weekly, hasWeekly := pickSnapshotWindow(windows, 604800, 518400, 691200)
	if !hasFiveHour && !hasWeekly {
		fiveHour, weekly, hasFiveHour, hasWeekly = fallbackSnapshotWindows(details)
	}
	if !hasFiveHour && !hasWeekly {
		return UsageSnapshot{}
	}
	return UsageSnapshot{
		FiveHour: fiveHour,
		Weekly:   weekly,
	}
}

func fallbackSnapshotWindows(details *rateLimitStatusDetails) (WindowUsage, WindowUsage, bool, bool) {
	primary := details.PrimaryWindow
	secondary := details.SecondaryWindow

	if primary != nil && secondary != nil {
		return windowUsageFromSnapshot(primary), windowUsageFromSnapshot(secondary), true, true
	}
	if secondary != nil {
		return WindowUsage{}, windowUsageFromSnapshot(secondary), false, true
	}
	if primary == nil {
		return WindowUsage{}, WindowUsage{}, false, false
	}

	if isLikelyFiveHourWindow(primary) {
		return windowUsageFromSnapshot(primary), WindowUsage{}, true, false
	}
	return WindowUsage{}, windowUsageFromSnapshot(primary), false, true
}

func isLikelyFiveHourWindow(snapshot *rateLimitWindowSnapshot) bool {
	if snapshot == nil {
		return false
	}
	windowSeconds := snapshot.LimitWindowSeconds
	if windowSeconds <= 0 {
		windowSeconds = snapshot.ResetAfterSeconds
	}
	if windowSeconds <= 0 {
		return false
	}
	return windowSeconds <= 12*60*60
}

func windowUsageFromSnapshot(snapshot *rateLimitWindowSnapshot) WindowUsage {
	if snapshot == nil {
		return WindowUsage{}
	}
	usage := windowUsageFromPercent(float64(snapshot.UsedPercent))
	if snapshot.ResetAt > 0 {
		usage.ResetAt = time.Unix(int64(snapshot.ResetAt), 0).UTC()
	}
	if snapshot.ResetAfterSeconds > 0 {
		usage.ResetAfterSeconds = snapshot.ResetAfterSeconds
	}
	return usage
}

func pickSnapshotWindow(windows []*rateLimitWindowSnapshot, targetSeconds, minSeconds, maxSeconds int) (WindowUsage, bool) {
	bestIdx := -1
	bestDelta := 0
	for i, window := range windows {
		if window.LimitWindowSeconds < minSeconds || window.LimitWindowSeconds > maxSeconds {
			continue
		}
		delta := window.LimitWindowSeconds - targetSeconds
		if delta < 0 {
			delta = -delta
		}
		if bestIdx == -1 || delta < bestDelta {
			bestIdx = i
			bestDelta = delta
		}
	}
	if bestIdx == -1 {
		return WindowUsage{}, false
	}
	return windowUsageFromSnapshot(windows[bestIdx]), true
}

func windowUsageFromPercent(used float64) WindowUsage {
	if math.IsNaN(used) || math.IsInf(used, 0) {
		return WindowUsage{}
	}
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	return WindowUsage{
		UsedPercent:  used,
		LimitPercent: defaultLimitPoints,
		Known:        true,
	}
}
