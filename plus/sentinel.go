package plus

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/google/uuid"
)

const (
	sentinelMaxAttempts = 500000
	sentinelErrorPrefix = "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D"
	sentinelSDKURL      = "https://sentinel.openai.com/sentinel/20260219f9f6/sdk.js"
)

// fnv1a32 implements the FNV-1a 32-bit hash with murmurhash3 finalizer,
// reverse-engineered from the sentinel SDK JavaScript.
func fnv1a32(text string) string {
	h := uint32(2166136261) // FNV offset basis
	for _, ch := range text {
		h ^= uint32(ch)
		h *= 16777619 // FNV prime
	}
	// murmurhash3 finalizer (xorshift mixing)
	h ^= h >> 16
	h *= 2246822507
	h ^= h >> 13
	h *= 3266489909
	h ^= h >> 16
	return fmt.Sprintf("%08x", h)
}

// sentinelBase64Encode mimics the SDK's E() function:
// JSON.stringify → TextEncoder.encode → btoa
func sentinelBase64Encode(data any) (string, error) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("json marshal: %w", err)
	}
	return base64.StdEncoding.EncodeToString(jsonBytes), nil
}

// sentinelConfig builds the 25-element browser fingerprint array that the SDK collects.
// Reverse-engineered from the SDK's getConfig() method — array indices must match exactly.
func sentinelConfig(sid string) []any {
	screenInfo := "1920x1080"
	dateStr := time.Now().UTC().Format("Mon Jan 02 2006 15:04:05 GMT+0000 (Coordinated Universal Time)")
	jsHeapLimit := uint64(4294967296) // performance.memory.jsHeapSizeLimit (2^32)
	navRandom1 := rand.Float64()
	ua := chromeUserAgent
	// SDK picks a random script src; hardcoding the sentinel SDK URL is sufficient.
	scriptSrc := sentinelSDKURL
	// SDK extracts version from script src via regex /c/[^/]*/_/ or falls back to data-build attr.
	scriptVersion := ""
	language := "en-US"
	languages := "en-US,en"
	navRandom2 := rand.Float64()

	// T() in SDK: pick random key from Navigator.prototype, append "−" + navigator[key].toString().
	// We approximate with realistic return values for common navigator properties.
	navPropPairs := []struct{ name, value string }{
		{"vendorSub", ""},
		{"productSub", ""},
		{"vendor", "Google Inc."},
		{"maxTouchPoints", "0"},
		{"userActivation", "[object UserActivation]"},
		{"doNotTrack", "1"},
		{"geolocation", "[object Geolocation]"},
		{"connection", "[object NetworkInformation]"},
		{"plugins", "[object PluginArray]"},
		{"mimeTypes", "[object MimeTypeArray]"},
		{"pdfViewerEnabled", "true"},
		{"webkitTemporaryStorage", "[object StorageManager]"},
		{"webkitPersistentStorage", "[object StorageManager]"},
		{"hardwareConcurrency", "8"},
		{"cookieEnabled", "true"},
		{"credentials", "[object CredentialsContainer]"},
		{"mediaDevices", "[object MediaDevices]"},
		{"permissions", "[object Permissions]"},
		{"locks", "[object LockManager]"},
	}
	navPair := navPropPairs[rand.IntN(len(navPropPairs))]
	navVal := navPair.name + "\u2212" + navPair.value

	// R(Object.keys(document)) / R(Object.keys(window))
	docKeys := []string{"location", "implementation", "URL", "documentURI", "compatMode"}
	winKeys := []string{"Object", "Function", "Array", "Number", "parseFloat", "undefined"}
	docKey := docKeys[rand.IntN(len(docKeys))]
	winKey := winKeys[rand.IntN(len(winKeys))]
	perfNow := rand.Float64()*49000 + 1000 // 1000..50000
	hardwareConcurrency := []int{4, 8, 12, 16}[rand.IntN(4)]
	timeOrigin := float64(time.Now().UnixMilli()) - perfNow

	return []any{
		screenInfo,          // [0]  screen.width + screen.height
		dateStr,             // [1]  "" + new Date
		jsHeapLimit,         // [2]  performance.memory.dump (jsHeapSizeLimit)
		navRandom1,          // [3]  Math.random() → overwritten by nonce
		ua,                  // [4]  navigator.userAgent
		scriptSrc,           // [5]  random script src
		scriptVersion,       // [6]  script version regex match or data-build attr
		language,            // [7]  navigator.language
		languages,           // [8]  navigator.languages.join(",")
		navRandom2,          // [9]  Math.random() → overwritten by elapsed ms
		navVal,              // [10] T(): navProp + "−" + navigator[prop].toString()
		docKey,              // [11] R(Object.keys(document))
		winKey,              // [12] R(Object.keys(window))
		perfNow,             // [13] performance.now()
		sid,                 // [14] this.sid (UUID)
		"",                  // [15] [...URLSearchParams(location.search).keys()].join(",")
		hardwareConcurrency, // [16] navigator.hardwareConcurrency
		timeOrigin,          // [17] performance.timeOrigin
		0,                   // [18] Number("ai" in window)
		0,                   // [19] Number("createPRNG" in window)
		0,                   // [20] Number("cache" in window)
		0,                   // [21] Number("data" in window)
		0,                   // [22] Number("solana" in window)
		0,                   // [23] Number("answers" in window)
		0,                   // [24] Number("InstallTrigger" in window)
	}
}

// sentinelRunCheck performs a single PoW attempt.
// Returns the base64-encoded config + "~S" suffix if the difficulty check passes.
func sentinelRunCheck(startTime time.Time, seed, difficulty string, config []any, nonce int) (string, error) {
	config[3] = nonce
	config[9] = int(time.Since(startTime).Milliseconds())
	data, err := sentinelBase64Encode(config)
	if err != nil {
		return "", fmt.Errorf("base64 encode config: %w", err)
	}
	hashHex := fnv1a32(seed + data)
	if hashHex[:len(difficulty)] <= difficulty {
		return data + "~S", nil
	}
	return "", nil
}

// sentinelGenerateToken runs the full PoW brute-force loop.
// Mirrors Python's SentinelTokenGenerator.generate_token.
func sentinelGenerateToken(seed, difficulty string) (string, error) {
	sid := uuid.New().String()
	config := sentinelConfig(sid)
	startTime := time.Now()

	for i := range sentinelMaxAttempts {
		result, err := sentinelRunCheck(startTime, seed, difficulty, config, i)
		if err != nil {
			return "", fmt.Errorf("pow attempt %d: %w", i, err)
		}
		if result != "" {
			return "gAAAAAB" + result, nil
		}
	}

	// PoW failed — return error token (matches Python fallback)
	nilEncoded, _ := sentinelBase64Encode("null")
	return "gAAAAAB" + sentinelErrorPrefix + nilEncoded, nil
}

// sentinelGenerateRequirementsToken builds a requirements token for the initial challenge request.
// Unlike Python's version, the SDK's getRequirementsToken actually runs PoW with a local random
// seed and difficulty "0" (trivially easy, ~1-16 attempts on average).
// Evidence: HAR shows sentinel/req p = "gAAAAAC...~S" with config[3] = nonce.
func sentinelGenerateRequirementsToken() (string, error) {
	// SDK uses "" + Math.random() as the local requirements seed.
	seed := fmt.Sprintf("%g", rand.Float64())
	sid := uuid.New().String()
	config := sentinelConfig(sid)
	startTime := time.Now()

	for i := range sentinelMaxAttempts {
		result, err := sentinelRunCheck(startTime, seed, "0", config, i)
		if err != nil {
			return "", fmt.Errorf("requirements pow attempt %d: %w", i, err)
		}
		if result != "" {
			return "gAAAAAC" + result, nil
		}
	}

	// Fallback: should essentially never happen with difficulty "0".
	nilEncoded, _ := sentinelBase64Encode("null")
	return "gAAAAAC" + sentinelErrorPrefix + nilEncoded, nil
}

// sentinelChallengeResponse is the JSON shape returned by the sentinel backend.
type sentinelChallengeResponse struct {
	Token       string `json:"token"`
	ProofOfWork struct {
		Required   bool   `json:"required"`
		Seed       string `json:"seed"`
		Difficulty string `json:"difficulty"`
	} `json:"proofofwork"`
	Turnstile struct {
		Dx string `json:"dx"`
	} `json:"turnstile"`
}

type sentinelTokenPayload struct {
	P    string  `json:"p"`
	T    *string `json:"t"`
	C    string  `json:"c"`
	ID   string  `json:"id"`
	Flow string  `json:"flow"`
}

func marshalSentinelTokenPayload(p string, t *string, c, id, flow string) (string, error) {
	payload := sentinelTokenPayload{
		P:    p,
		T:    t,
		C:    c,
		ID:   id,
		Flow: flow,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode sentinel token header: %w", err)
	}
	return string(body), nil
}

func (r *registrationFlow) buildSentinelTokenHeader(ctx context.Context, action string) (string, error) {
	if action == "" {
		return "", errors.New("sentinel action is empty")
	}
	if r.oaiDID == "" {
		return "", errors.New("oai-did is empty")
	}

	// Step 1: Generate a requirements token for the initial challenge request.
	reqToken, err := sentinelGenerateRequirementsToken()
	if err != nil {
		return "", fmt.Errorf("generate requirements token: %w", err)
	}

	// Step 2: Fetch challenge from sentinel backend.
	reqPayload := map[string]string{
		"p":    reqToken,
		"id":   r.oaiDID,
		"flow": action,
	}
	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("encode sentinel request body: %w", err)
	}

	headers := map[string]string{
		"origin":  "https://sentinel.openai.com",
		"referer": "https://sentinel.openai.com/backend-api/sentinel/frame.html?sv=20260219f9f6",
	}
	resp, err := r.client.Post(ctx, "https://sentinel.openai.com/backend-api/sentinel/req", headers, bytes.NewReader(reqBody), "text/plain;charset=UTF-8")
	if err != nil {
		return "", fmt.Errorf("request sentinel token: %w", err)
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return "", fmt.Errorf("request sentinel token: %w", err)
	}

	var challenge sentinelChallengeResponse
	if err := decodeJSON(resp.Body, &challenge); err != nil {
		return "", fmt.Errorf("decode sentinel response: %w", err)
	}
	if challenge.Token == "" {
		return "", errors.New("sentinel response token is empty")
	}

	// Step 3: Run PoW if required, otherwise use requirements token.
	var pValue string
	if challenge.ProofOfWork.Required && challenge.ProofOfWork.Seed != "" {
		pValue, err = sentinelGenerateToken(challenge.ProofOfWork.Seed, challenge.ProofOfWork.Difficulty)
		if err != nil {
			return "", fmt.Errorf("generate pow token: %w", err)
		}
	} else {
		pValue, err = sentinelGenerateRequirementsToken()
		if err != nil {
			return "", fmt.Errorf("generate fallback requirements token: %w", err)
		}
	}

	// Step 4: Turnstile dx is derived from the original requirements token, not the final p value.
	var tValue *string
	if challenge.Turnstile.Dx != "" {
		resolved, err := sentinelSolveTurnstileToken(ctx, challenge.Turnstile.Dx, reqToken)
		if err != nil {
			return "", fmt.Errorf("solve turnstile dx: %w", err)
		}
		tValue = &resolved
	}

	return marshalSentinelTokenPayload(pValue, tValue, challenge.Token, r.oaiDID, action)
}
