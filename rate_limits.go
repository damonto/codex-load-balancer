package main

import (
	"math"
	"net/http"
	"strconv"
)

type headerWindow struct {
	usedPercent   float64
	windowMinutes int64
}

func usageFromHeaders(headers http.Header) (WindowUsage, WindowUsage, bool, bool) {
	windows := make([]headerWindow, 0, 2)
	if window, ok := parseHeaderWindow(headers, "x-codex-primary-used-percent", "x-codex-primary-window-minutes"); ok {
		windows = append(windows, window)
	}
	if window, ok := parseHeaderWindow(headers, "x-codex-secondary-used-percent", "x-codex-secondary-window-minutes"); ok {
		windows = append(windows, window)
	}

	fiveHour, hasFiveHour := pickHeaderWindow(windows, 300, 240, 360)
	weekly, hasWeekly := pickHeaderWindow(windows, 10080, 8640, 11520)
	return fiveHour, weekly, hasFiveHour, hasWeekly
}

func pickHeaderWindow(windows []headerWindow, targetMinutes, minMinutes, maxMinutes int64) (WindowUsage, bool) {
	bestIdx := -1
	bestDelta := int64(0)
	for i, window := range windows {
		if window.windowMinutes < minMinutes || window.windowMinutes > maxMinutes {
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
	return windowUsageFromPercent(windows[bestIdx].usedPercent), true
}

func parseHeaderWindow(headers http.Header, usedKey, minutesKey string) (headerWindow, bool) {
	usedStr := headers.Get(usedKey)
	minutesStr := headers.Get(minutesKey)
	if usedStr == "" || minutesStr == "" {
		return headerWindow{}, false
	}
	used, err := strconv.ParseFloat(usedStr, 64)
	if err != nil {
		return headerWindow{}, false
	}
	minutes, err := strconv.ParseInt(minutesStr, 10, 64)
	if err != nil || minutes <= 0 {
		return headerWindow{}, false
	}
	return headerWindow{
		usedPercent:   used,
		windowMinutes: minutes,
	}, true
}
