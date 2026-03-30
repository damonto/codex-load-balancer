package plus

import (
	cryptoRand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	randv2 "math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var otpPattern = regexp.MustCompile(`\b(\d{6})\b`)

func generateName() string {
	firstNames := []string{
		"Alex", "Chris", "Jordan", "Taylor", "Morgan", "Sam", "Casey",
		"Jamie", "Cameron", "Drew", "Avery", "Riley", "Quinn", "Parker",
		"Reese", "Skyler", "Rowan", "Hayden", "Emerson", "Finley",
		"Logan", "Elliot", "Charlie", "Dakota", "Blake", "Shawn",
		"Jesse", "Robin", "Kendall", "Micah",

		"Adrian", "Aidan", "Aiden", "Alan", "Albert", "Alec", "Allen",
		"Andrew", "Anthony", "Arthur", "Austin", "Barry", "Benjamin",
		"Brandon", "Brian", "Caleb", "Carl", "Carlos", "Chad", "Connor",
		"Daniel", "David", "Dennis", "Derek", "Dominic", "Edward",
		"Ethan", "Evan", "Felix", "Frank", "George", "Gavin", "Grant",
		"Henry", "Howard", "Ian", "Isaac", "Jack", "Jacob", "Jason",
		"Jeff", "Jeremy", "Joel", "John", "Jonathan", "Joseph", "Joshua",
		"Justin", "Kevin", "Kyle", "Larry", "Leon", "Liam", "Louis",
		"Lucas", "Marcus", "Mark", "Martin", "Matthew", "Max", "Nathan",
		"Nathaniel", "Nicholas", "Noah", "Oliver", "Oscar", "Owen",
		"Patrick", "Paul", "Peter", "Philip", "Ray", "Richard", "Robert",
		"Ryan", "Scott", "Sean", "Simon", "Stephen", "Steve", "Steven",
		"Thomas", "Tim", "Timothy", "Tony", "Travis", "Trevor", "Victor",
		"Vincent", "Walter", "Wayne", "William", "Zach", "Zachary",
	}

	lastNames := []string{
		"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller",
		"Davis", "Rodriguez", "Martinez", "Hernandez", "Lopez", "Gonzalez",
		"Wilson", "Anderson", "Thomas", "Taylor", "Moore", "Jackson",
		"Martin", "Lee", "Perez", "Thompson", "White", "Harris",
		"Sanchez", "Clark", "Ramirez", "Lewis", "Robinson",

		"Walker", "Young", "Allen", "King", "Wright", "Scott", "Torres",
		"Nguyen", "Hill", "Flores", "Green", "Adams", "Nelson",
		"Baker", "Hall", "Rivera", "Campbell", "Mitchell", "Carter",
		"Roberts", "Gomez", "Phillips", "Evans", "Turner", "Diaz",
		"Parker", "Cruz", "Edwards", "Collins", "Reyes", "Stewart",
		"Morris", "Morales", "Murphy", "Cook", "Rogers", "Gutierrez",
		"Ortiz", "Morgan", "Cooper", "Peterson", "Bailey", "Reed",
		"Kelly", "Howard", "Ramos", "Kim", "Cox", "Ward", "Richardson",
		"Watson", "Brooks", "Chavez", "Wood", "James", "Bennett",
		"Gray", "Mendoza", "Ruiz", "Hughes", "Price", "Alvarez",
		"Castillo", "Sanders", "Patel", "Myers", "Long", "Ross",
		"Foster", "Jimenez",
	}
	return firstNames[randv2.N(len(firstNames))] + " " + lastNames[randv2.N(len(lastNames))]
}

func generateBirthdate() string {
	year := defaultBirthMinYear + randv2.N(defaultBirthMaxYear-defaultBirthMinYear+1)
	month := 1 + randv2.N(12)
	day := 10 + randv2.N(19)
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

func generatePassword() string {
	const (
		lower = "abcdefghijklmnopqrstuvwxyz"
		upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		digit = "0123456789"
		sym   = "!@#$%^&*"
	)

	parts := []byte{
		randomChar(upper),
		randomChar(lower),
		randomChar(digit),
		randomChar(sym),
	}
	all := lower + upper + digit + sym
	for range 12 {
		parts = append(parts, randomChar(all))
	}
	for i := len(parts) - 1; i > 0; i-- {
		j := randv2.N(i + 1)
		parts[i], parts[j] = parts[j], parts[i]
	}
	return string(parts)
}

func randomChar(chars string) byte {
	return chars[randv2.N(len(chars))]
}

func extractOTP(text string) string {
	if text == "" {
		return ""
	}
	matches := otpPattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

func cookieValue(client *client, u *url.URL, name string) string {
	for _, c := range client.raw.GetCookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func randomString(length int, charset string) (string, error) {
	buf := make([]byte, length)
	if _, err := cryptoRand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, length)
	for i, b := range buf {
		out[i] = charset[int(b)%len(charset)]
	}
	return string(out), nil
}

func randomBase64URL(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := cryptoRand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func randomHexString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := cryptoRand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func extractCodeFromLocation(location string) string {
	u, err := url.Parse(location)
	if err != nil {
		return ""
	}
	return u.Query().Get("code")
}

func extractOAuthCode(locations ...string) string {
	for _, item := range locations {
		if code := extractCodeFromLocation(item); code != "" {
			return code
		}
	}
	return ""
}

func extractOAuthCodeFromBody(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}

	normalized := strings.ReplaceAll(body, `\/`, `/`)
	matches := oauthCallbackPattern.FindAllString(normalized, -1)
	for _, item := range matches {
		candidate := html.UnescapeString(strings.TrimSpace(item))
		if strings.HasPrefix(candidate, "/") {
			candidate = codexRedirectURL + candidate
		}
		if code := extractOAuthCode(candidate); code != "" {
			return code
		}
	}
	return ""
}

func buildOAuthCallbackPattern(redirectURI string) *regexp.Regexp {
	basePath := "/auth/callback"
	host := "localhost:1455"
	parsed, err := url.Parse(strings.TrimSpace(redirectURI))
	if err == nil {
		if parsed.Path != "" {
			basePath = parsed.Path
		}
		if parsed.Host != "" {
			host = parsed.Host
		}
	}

	return regexp.MustCompile(`(?:https?://` + regexp.QuoteMeta(host) + `)?` + regexp.QuoteMeta(basePath) + `\?[^"'<>\s]+`)
}

func buildRedirectURL(redirectURI string) string {
	parsed, err := url.Parse(strings.TrimSpace(redirectURI))
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return parsed.Scheme + "://" + parsed.Host
	}
	return "http://localhost:1455"
}

func resolveLocation(baseURL, location string) (string, error) {
	baseParsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	locParsed, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return baseParsed.ResolveReference(locParsed).String(), nil
}

func isRedirect(status int) bool {
	return status == http.StatusMovedPermanently ||
		status == http.StatusFound ||
		status == http.StatusSeeOther ||
		status == http.StatusTemporaryRedirect ||
		status == http.StatusPermanentRedirect
}

func proxyHostOnly(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func isExpectedCodexOAuthLandingURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	if !strings.EqualFold(parsed.Host, "auth.openai.com") {
		return false
	}

	switch parsed.Path {
	case "/log-in", "/oauth/authorize":
		return true
	default:
		return false
	}
}

func isCodexLoginPageURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	if !strings.EqualFold(parsed.Host, "auth.openai.com") {
		return false
	}
	return parsed.Path == "/log-in"
}
