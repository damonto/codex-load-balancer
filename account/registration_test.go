package account

import (
	"strings"
	"testing"
)

func TestExtractOTP(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "plain otp",
			text: "Your verification code is 123456.",
			want: "123456",
		},
		{
			name: "html content",
			text: "<html><body><b>Use 654321 to continue</b></body></html>",
			want: "654321",
		},
		{
			name: "no otp",
			text: "Hello world",
			want: "",
		},
		{
			name: "empty",
			text: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOTP(tt.text)
			if got != tt.want {
				t.Fatalf("extractOTP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInjectProxySessionID(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantEqual bool
	}{
		{
			name:      "no placeholder keeps proxy",
			in:        "http://user:pass@host:8080",
			wantEqual: true,
		},
		{
			name:      "placeholder is replaced",
			in:        "http://session-%s:pass@host:8080",
			wantEqual: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := injectProxySessionID(tt.in)
			if err != nil {
				t.Fatalf("injectProxySessionID() error = %v", err)
			}

			if strings.Contains(got, "%s") {
				t.Fatalf("injectProxySessionID() left placeholder in %q", got)
			}

			if tt.wantEqual && got != tt.in {
				t.Fatalf("injectProxySessionID() = %q, want %q", got, tt.in)
			}
			if !tt.wantEqual && got == tt.in {
				t.Fatalf("injectProxySessionID() = %q, want changed value", got)
			}
		})
	}
}

func TestExtractCSRFToken(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "raw cookie token",
			in:   "abc123|hash-part",
			want: "abc123",
		},
		{
			name: "url encoded cookie token",
			in:   "abc%3D123%7Chash-part",
			want: "abc=123",
		},
		{
			name: "empty cookie",
			in:   "",
			want: "",
		},
		{
			name: "no separator",
			in:   "single-token",
			want: "single-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCSRFToken(tt.in)
			if got != tt.want {
				t.Fatalf("extractCSRFToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsExpectedCodexOAuthLandingURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "login page",
			raw:  "https://auth.openai.com/log-in",
			want: true,
		},
		{
			name: "authorize page",
			raw:  "https://auth.openai.com/oauth/authorize?client_id=test",
			want: true,
		},
		{
			name: "wrong host",
			raw:  "https://chatgpt.com/log-in",
			want: false,
		},
		{
			name: "unexpected path",
			raw:  "https://auth.openai.com/challenge",
			want: false,
		},
		{
			name: "invalid url",
			raw:  "://bad-url",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isExpectedCodexOAuthLandingURL(tt.raw)
			if got != tt.want {
				t.Fatalf("isExpectedCodexOAuthLandingURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsCodexLoginPageURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "log-in page",
			raw:  "https://auth.openai.com/log-in",
			want: true,
		},
		{
			name: "authorize page",
			raw:  "https://auth.openai.com/oauth/authorize?client_id=test",
			want: false,
		},
		{
			name: "wrong host",
			raw:  "https://chatgpt.com/log-in",
			want: false,
		},
		{
			name: "invalid url",
			raw:  "://bad-url",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCodexLoginPageURL(tt.raw)
			if got != tt.want {
				t.Fatalf("isCodexLoginPageURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractOAuthCodeFromBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "absolute callback url",
			body: `<html><script>location.href="http://localhost:1455/auth/callback?code=abc123&state=x"</script></html>`,
			want: "abc123",
		},
		{
			name: "relative callback url",
			body: `<script>window.location='/auth/callback?code=xyz789&state=y'</script>`,
			want: "xyz789",
		},
		{
			name: "escaped slash callback url",
			body: `{"continue":"http:\/\/localhost:1455\/auth\/callback?code=code555&state=s"}`,
			want: "code555",
		},
		{
			name: "html escaped ampersand",
			body: `<meta http-equiv="refresh" content="0;url=http://localhost:1455/auth/callback?code=ok1&amp;state=z">`,
			want: "ok1",
		},
		{
			name: "no callback",
			body: `<html><body>continue</body></html>`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOAuthCodeFromBody(tt.body)
			if got != tt.want {
				t.Fatalf("extractOAuthCodeFromBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractOAuthCode(t *testing.T) {
	tests := []struct {
		name      string
		locations []string
		want      string
	}{
		{
			name:      "first has code",
			locations: []string{"https://localhost:1455/auth/callback?code=first&state=s", "https://localhost:1455/auth/callback?code=second"},
			want:      "first",
		},
		{
			name:      "skip empty and invalid",
			locations: []string{"", "://bad", "/auth/callback?code=ok"},
			want:      "ok",
		},
		{
			name:      "no code",
			locations: []string{"https://example.com/path", "https://localhost:1455/auth/callback?state=s"},
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOAuthCode(tt.locations...)
			if got != tt.want {
				t.Fatalf("extractOAuthCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildRedirectBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "valid redirect uri",
			in:   "http://localhost:1455/auth/callback",
			want: "http://localhost:1455",
		},
		{
			name: "invalid uri falls back",
			in:   "://bad",
			want: "http://localhost:1455",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRedirectBaseURL(tt.in)
			if got != tt.want {
				t.Fatalf("buildRedirectBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildOAuthCallbackPattern(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		body     string
		contains bool
	}{
		{
			name:     "matches configured host and path",
			uri:      "http://localhost:1455/auth/callback",
			body:     `window.location="http://localhost:1455/auth/callback?code=abc&state=s"`,
			contains: true,
		},
		{
			name:     "matches relative path",
			uri:      "http://localhost:1455/auth/callback",
			body:     `window.location="/auth/callback?code=abc&state=s"`,
			contains: true,
		},
		{
			name:     "different path does not match",
			uri:      "http://localhost:1455/custom/callback",
			body:     `window.location="/auth/callback?code=abc&state=s"`,
			contains: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := buildOAuthCallbackPattern(tt.uri)
			got := p.FindString(tt.body) != ""
			if got != tt.contains {
				t.Fatalf("buildOAuthCallbackPattern() match = %v, want %v", got, tt.contains)
			}
		})
	}
}

func TestParseAuthPageType(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    authPageType
		wantErr bool
	}{
		{
			name:    "valid response",
			body:    `{"page":{"type":"login_password"}}`,
			want:    authPageTypeLoginPassword,
			wantErr: false,
		},
		{
			name:    "missing page type",
			body:    `{"page":{"type":"   "}}`,
			want:    "",
			wantErr: true,
		},
		{
			name:    "invalid json",
			body:    `{`,
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAuthPageType(strings.NewReader(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseAuthPageType() err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("parseAuthPageType() = %q, want %q", got, tt.want)
			}
		})
	}
}
