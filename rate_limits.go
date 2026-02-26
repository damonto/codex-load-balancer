package main

import (
	"math"
	"net/http"
	"strconv"
	"time"
)

type headerWindow struct {
	usedPercent   float64
	windowMinutes int64 // 0 means not provided by server
	resetAt       int64 // unix seconds; 0 means not provided
}

// usageFromHeaders parses the Codex rate-limit response headers.
// Header format: x-codex-{primary|secondary}-{used-percent|window-minutes|reset-at}
func usageFromHeaders(headers http.Header) (WindowUsage, WindowUsage, bool, bool) {
	primary, hasPrimary := parseHeaderWindow(headers,
		"x-codex-primary-used-percent",
		"x-codex-primary-window-minutes",
		"x-codex-primary-reset-at")
	secondary, hasSecondary := parseHeaderWindow(headers,
		"x-codex-secondary-used-percent",
		"x-codex-secondary-window-minutes",
		"x-codex-secondary-reset-at")

	windows := make([]headerWindow, 0, 2)
	if hasPrimary {
		windows = append(windows, primary)
	}
	if hasSecondary {
		windows = append(windows, secondary)
	}

	fiveHour, hasFiveHour := pickHeaderWindow(windows, 300, 240, 360)
	weekly, hasWeekly := pickHeaderWindow(windows, 10080, 8640, 11520)

	// Positional fallback: when window_minutes is absent the server provides no
	// duration hint. Follow Codex convention: primary → 5-hour, secondary → weekly.
	if !hasFiveHour && hasPrimary && primary.windowMinutes == 0 {
		fiveHour = windowUsageFromHeader(primary)
		hasFiveHour = true
	}
	if !hasWeekly && hasSecondary && secondary.windowMinutes == 0 {
		weekly = windowUsageFromHeader(secondary)
		hasWeekly = true
	}

	return fiveHour, weekly, hasFiveHour, hasWeekly
}

func pickHeaderWindow(windows []headerWindow, targetMinutes, minMinutes, maxMinutes int64) (WindowUsage, bool) {
	bestIdx := -1
	bestDelta := int64(0)
	for i, window := range windows {
		if window.windowMinutes <= 0 || window.windowMinutes < minMinutes || window.windowMinutes > maxMinutes {
			continue
		}
		delta := int64(math.Abs(float64(window.windowMinutes - targetMinutes)))
		if bestIdx == -1 || delta < bestDelta {
			bestIdx = i
			bestDelta = delta
		}
	}
	if bestIdx == -1 {
		return WindowUsage{}, false
	}
	return windowUsageFromHeader(windows[bestIdx]), true
}

func windowUsageFromHeader(w headerWindow) WindowUsage {
	usage := windowUsageFromPercent(w.usedPercent)
	if w.resetAt > 0 {
		usage.ResetAt = time.Unix(w.resetAt, 0).UTC()
	}
	return usage
}

// parseHeaderWindow parses a rate-limit window from response headers.
// Only usedKey is required; missing minutesKey or resetAtKey are treated as zero.
func parseHeaderWindow(headers http.Header, usedKey, minutesKey, resetAtKey string) (headerWindow, bool) {
	usedStr := headers.Get(usedKey)
	if usedStr == "" {
		return headerWindow{}, false
	}
	used, err := strconv.ParseFloat(usedStr, 64)
	if err != nil {
		return headerWindow{}, false
	}
	var minutes int64
	if minutesStr := headers.Get(minutesKey); minutesStr != "" {
		if v, err := strconv.ParseInt(minutesStr, 10, 64); err == nil && v > 0 {
			minutes = v
		}
	}
	var resetAt int64
	if resetAtStr := headers.Get(resetAtKey); resetAtStr != "" {
		resetAt, _ = strconv.ParseInt(resetAtStr, 10, 64)
	}
	return headerWindow{
		usedPercent:   used,
		windowMinutes: minutes,
		resetAt:       resetAt,
	}, true
}
