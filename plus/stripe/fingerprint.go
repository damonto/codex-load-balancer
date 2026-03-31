package stripe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	randv2 "math/rand/v2"
	"net/url"
	"strconv"
	"time"
)

func (p *Processor) fetchFingerprint(ctx context.Context) (fingerprint, error) {
	slog.Info("stripe fingerprint sequence started", "checkout_session", shortLogID(p.checkout.SessionID))
	fp, err := newFingerprint()
	if err != nil {
		return fingerprint{}, fmt.Errorf("generate local stripe fingerprint: %w", err)
	}

	fingerprintID, err := randomHexString(16)
	if err != nil {
		return fingerprint{}, fmt.Errorf("generate stripe fingerprint id: %w", err)
	}

	screen := screenProfiles[randv2.N(len(screenProfiles))]
	viewportHeight := screen.Height - (40 + randv2.N(31))
	cpuChoices := []int{4, 8, 12, 16}
	cpu := cpuChoices[randv2.N(len(cpuChoices))]
	canvasFP := canvasFingerprints[randv2.N(len(canvasFingerprints))]
	audioFP := audioFingerprints[randv2.N(len(audioFingerprints))]

	payload, err := buildFullFingerprintPayload(fingerprintID, fp, false, screen, viewportHeight, cpu, canvasFP, audioFP, p.userAgent)
	if err != nil {
		return fingerprint{}, fmt.Errorf("build stripe fingerprint payload #1: %w", err)
	}
	update, err := p.postFingerprint(ctx, payload)
	if err != nil {
		return fingerprint{}, fmt.Errorf("post stripe fingerprint payload #1: %w", err)
	}
	fp = mergeFingerprint(fp, update)
	slog.Info("stripe fingerprint payload posted", "checkout_session", shortLogID(p.checkout.SessionID), "stage", "full-1")

	payload, err = buildFullFingerprintPayload(fingerprintID, fp, true, screen, viewportHeight, cpu, canvasFP, audioFP, p.userAgent)
	if err != nil {
		return fingerprint{}, fmt.Errorf("build stripe fingerprint payload #2: %w", err)
	}
	update, err = p.postFingerprint(ctx, payload)
	if err != nil {
		return fingerprint{}, fmt.Errorf("post stripe fingerprint payload #2: %w", err)
	}
	fp = mergeFingerprint(fp, update)
	slog.Info("stripe fingerprint payload posted", "checkout_session", shortLogID(p.checkout.SessionID), "stage", "full-2")

	payload, err = buildMouseFingerprintPayload(fp, "mouse-timings-10-v2")
	if err != nil {
		return fingerprint{}, fmt.Errorf("build stripe fingerprint mouse payload #1: %w", err)
	}
	if _, err := p.postFingerprint(ctx, payload); err != nil {
		return fingerprint{}, fmt.Errorf("post stripe fingerprint mouse payload #1: %w", err)
	}
	slog.Info("stripe fingerprint payload posted", "checkout_session", shortLogID(p.checkout.SessionID), "stage", "mouse-1")

	payload, err = buildMouseFingerprintPayload(fp, "mouse-timings-10")
	if err != nil {
		return fingerprint{}, fmt.Errorf("build stripe fingerprint mouse payload #2: %w", err)
	}
	if _, err := p.postFingerprint(ctx, payload); err != nil {
		return fingerprint{}, fmt.Errorf("post stripe fingerprint mouse payload #2: %w", err)
	}
	slog.Info("stripe fingerprint payload posted", "checkout_session", shortLogID(p.checkout.SessionID), "stage", "mouse-2")
	return fp, nil
}

func (p *Processor) postFingerprint(ctx context.Context, payload map[string]any) (fingerprint, error) {
	body, err := encodeFingerprintPayload(payload)
	if err != nil {
		return fingerprint{}, fmt.Errorf("encode stripe fingerprint payload: %w", err)
	}

	var fp fingerprint
	if err := p.client.PostRawJSON(ctx, "https://m.stripe.com/6", map[string]string{
		"Accept":  "*/*",
		"Origin":  "https://m.stripe.network",
		"Referer": "https://m.stripe.network/",
	}, body, "text/plain;charset=UTF-8", &fp); err != nil {
		return fingerprint{}, err
	}
	return fp, nil
}

func newFingerprint() (fingerprint, error) {
	guid, err := randomHexString(16)
	if err != nil {
		return fingerprint{}, err
	}
	muid, err := randomHexString(16)
	if err != nil {
		return fingerprint{}, err
	}
	sid, err := randomHexString(16)
	if err != nil {
		return fingerprint{}, err
	}
	return fingerprint{
		GUID: guid,
		MUID: muid,
		SID:  sid,
	}, nil
}

func buildFullFingerprintPayload(id string, fp fingerprint, includeIDs bool, screen screenProfile, viewportHeight int, cpu int, canvasFP, audioFP, userAgent string) (map[string]any, error) {
	s1, err := randomBase64URL(12)
	if err != nil {
		return nil, err
	}
	s2, err := randomBase64URL(12)
	if err != nil {
		return nil, err
	}
	s3, err := randomBase64URL(12)
	if err != nil {
		return nil, err
	}
	s4, err := randomBase64URL(12)
	if err != nil {
		return nil, err
	}
	s5, err := randomBase64URL(12)
	if err != nil {
		return nil, err
	}
	pageToken, err := randomBase64URL(12)
	if err != nil {
		return nil, err
	}
	beaconToken, err := randomBase64URL(12)
	if err != nil {
		return nil, err
	}
	stateHash, err := randomHexString(32)
	if err != nil {
		return nil, err
	}
	payloadHash, err := randomHexString(10)
	if err != nil {
		return nil, err
	}

	muid := "NA"
	sid := "NA"
	if includeIDs {
		muid = fp.MUID
		sid = fp.SID
	}

	dpr := strconv.FormatFloat(screen.DPR, 'f', -1, 64)
	return map[string]any{
		"v2":  1 + boolToInt(includeIDs),
		"id":  id,
		"t":   randomFloat(3, 120),
		"tag": fingerprintTag,
		"src": fingerprintTelemetrySource,
		"a": map[string]any{
			"a": map[string]any{"v": "true", "t": 0},
			"b": map[string]any{"v": "true", "t": 0},
			"c": map[string]any{"v": browserLocale, "t": 0},
			"d": map[string]any{"v": fingerprintPlatform, "t": 0},
			"e": map[string]any{"v": fingerprintPlugins, "t": randomFloat(0, 0.5)},
			"f": map[string]any{"v": fmt.Sprintf("%dw_%dh_24d_%sr", screen.Width, viewportHeight, dpr), "t": 0},
			"g": map[string]any{"v": strconv.Itoa(cpu), "t": 0},
			"h": map[string]any{"v": "false", "t": 0},
			"i": map[string]any{"v": fingerprintBrowserState, "t": randomFloat(0.5, 2)},
			"j": map[string]any{"v": canvasFP, "t": randomFloat(5, 120)},
			"k": map[string]any{"v": "", "t": 0},
			"l": map[string]any{"v": userAgent, "t": 0},
			"m": map[string]any{"v": "", "t": 0},
			"n": map[string]any{"v": "false", "t": randomFloat(3, 50)},
			"o": map[string]any{"v": audioFP, "t": randomFloat(20, 30)},
		},
		"b": map[string]any{
			"a": fmt.Sprintf("https://%s.%s.%s/", s1, s2, s3),
			"b": fmt.Sprintf("https://%s.%s/%s/%s/%s", s1, s3, s4, s5, pageToken),
			"c": beaconToken,
			"d": muid,
			"e": sid,
			"f": false,
			"g": true,
			"h": true,
			"i": []string{"location"},
			"j": []string{},
			"n": randomFloat(800, 2000),
			"u": checkoutReferrerHost,
			"v": "auth.openai.com",
			"w": fmt.Sprintf("%d:%s", time.Now().UnixMilli(), stateHash),
		},
		"h": payloadHash,
	}, nil
}

func buildMouseFingerprintPayload(fp fingerprint, source string) (map[string]any, error) {
	host, err := randomBase64URL(8)
	if err != nil {
		return nil, err
	}
	domain, err := randomBase64URL(8)
	if err != nil {
		return nil, err
	}
	pathA, err := randomBase64URL(8)
	if err != nil {
		return nil, err
	}
	pathB, err := randomBase64URL(8)
	if err != nil {
		return nil, err
	}
	pathC, err := randomBase64URL(8)
	if err != nil {
		return nil, err
	}

	data := make([]int, 10)
	for i := range data {
		data[i] = 1 + randv2.N(8)
	}
	return map[string]any{
		"muid":   fp.MUID,
		"sid":    fp.SID,
		"url":    fmt.Sprintf("https://%s.%s/%s/%s/%s", host, domain, pathA, pathB, pathC),
		"source": source,
		"data":   data,
	}, nil
}

func encodeFingerprintPayload(payload map[string]any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(string(raw)))), nil
}

func mergeFingerprint(base, update fingerprint) fingerprint {
	if update.GUID != "" {
		base.GUID = update.GUID
	}
	if update.MUID != "" {
		base.MUID = update.MUID
	}
	if update.SID != "" {
		base.SID = update.SID
	}
	return base
}

func randomFloat(min, max float64) float64 {
	return min + randv2.Float64()*(max-min)
}
