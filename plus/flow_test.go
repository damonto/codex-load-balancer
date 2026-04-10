package plus

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

func TestRegistrationFlowExecuteReleasesPurchaseTokenOnSignupFailure(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	insertPurchaseTokenForTest(t, store, "fetch-token-1", purchaseTokenStatusAvailable, 1)

	restoreExecuteHooks(t)
	executePrepareAccountProfile = func(_ *registrationFlow, _ context.Context) (RegisterResult, error) {
		return RegisterResult{Email: "user@example.com"}, nil
	}
	executeCompleteRegistrationFlow = func(_ *registrationFlow, _ context.Context) (ChatGPTSession, error) {
		return ChatGPTSession{}, errors.New("signup failed")
	}

	r := &registrationFlow{
		cfg: RegisterOptions{
			Purchase: PurchaseConfig{
				Enabled:             true,
				RevenueCatBearerKey: "goog_test_key",
				Store:               store,
			},
		},
	}

	_, err := r.execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "signup failed") {
		t.Fatalf("execute() error = %v, want signup failed", err)
	}

	row := loadPurchaseTokenRowForTest(t, store, 1)
	if row.status != purchaseTokenStatusAvailable {
		t.Fatalf("status = %q, want %q", row.status, purchaseTokenStatusAvailable)
	}
	if row.attemptCount != 0 {
		t.Fatalf("attempt_count = %d, want 0", row.attemptCount)
	}
}

func TestRegistrationFlowExecuteStopsBeforeOAuthWhenPurchaseFails(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	insertPurchaseTokenForTest(t, store, "fetch-token-2", purchaseTokenStatusAvailable, 1)

	restoreExecuteHooks(t)
	executePrepareAccountProfile = func(_ *registrationFlow, _ context.Context) (RegisterResult, error) {
		return RegisterResult{Email: "user@example.com"}, nil
	}
	executeCompleteRegistrationFlow = func(_ *registrationFlow, _ context.Context) (ChatGPTSession, error) {
		return ChatGPTSession{
			Account: ChatGPTSessionAccount{ID: "account-2"},
		}, nil
	}
	executeNewPurchase = func(_ purchaseHTTPClient, session ChatGPTSession, cfg PurchaseConfig, lease *PurchaseTokenLease) *Purchase {
		return NewPurchase(&fakePurchaseHTTPClient{
			response: &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("server error")),
			},
		}, session, cfg, lease)
	}

	loginCalled := false
	executeCompleteCodexLoginFlow = func(_ *registrationFlow, _ context.Context) (AuthTokens, string, error) {
		loginCalled = true
		return AuthTokens{}, "", nil
	}

	r := &registrationFlow{
		cfg: RegisterOptions{
			Purchase: PurchaseConfig{
				Enabled:             true,
				RevenueCatBearerKey: "goog_test_key",
				Store:               store,
			},
		},
	}

	_, err := r.execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "checkout") {
		t.Fatalf("execute() error = %v, want checkout failure", err)
	}
	if loginCalled {
		t.Fatal("completeCodexLoginFlow() should not run when purchase fails")
	}

	row := loadPurchaseTokenRowForTest(t, store, 1)
	if row.status != purchaseTokenStatusAvailable {
		t.Fatalf("status = %q, want %q", row.status, purchaseTokenStatusAvailable)
	}
	if row.attemptCount != 1 {
		t.Fatalf("attempt_count = %d, want 1", row.attemptCount)
	}
}

func TestRegistrationFlowExecuteContinuesAfterPurchaseSuccess(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	insertPurchaseTokenForTest(t, store, "fetch-token-3", purchaseTokenStatusAvailable, 1)

	restoreExecuteHooks(t)
	executePrepareAccountProfile = func(_ *registrationFlow, _ context.Context) (RegisterResult, error) {
		return RegisterResult{Email: "user@example.com"}, nil
	}
	executeCompleteRegistrationFlow = func(_ *registrationFlow, _ context.Context) (ChatGPTSession, error) {
		return ChatGPTSession{
			Account: ChatGPTSessionAccount{ID: "account-3"},
		}, nil
	}
	executeNewPurchase = func(_ purchaseHTTPClient, session ChatGPTSession, cfg PurchaseConfig, lease *PurchaseTokenLease) *Purchase {
		return NewPurchase(&fakePurchaseHTTPClient{
			response: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"subscriber":{}}`)),
			},
		}, session, cfg, lease)
	}

	loginCalled := false
	saveCalled := false
	executeCompleteCodexLoginFlow = func(_ *registrationFlow, _ context.Context) (AuthTokens, string, error) {
		loginCalled = true
		return AuthTokens{AccessToken: "access-token"}, "account-3", nil
	}
	executeSaveCredentialFile = func(_ *registrationFlow, _ RegisterResult) (string, error) {
		saveCalled = true
		return "/tmp/account-3.json", nil
	}

	r := &registrationFlow{
		cfg: RegisterOptions{
			Purchase: PurchaseConfig{
				Enabled:             true,
				RevenueCatBearerKey: "goog_test_key",
				Store:               store,
			},
		},
	}

	result, err := r.execute(context.Background())
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if !loginCalled {
		t.Fatal("completeCodexLoginFlow() should run after purchase success")
	}
	if !saveCalled {
		t.Fatal("saveCredentialFile() should run after purchase success")
	}
	if result.AccountID != "account-3" {
		t.Fatalf("AccountID = %q, want account-3", result.AccountID)
	}

	row := loadPurchaseTokenRowForTest(t, store, 1)
	if row.status != purchaseTokenStatusConsumed {
		t.Fatalf("status = %q, want %q", row.status, purchaseTokenStatusConsumed)
	}
	if row.attemptCount != 1 {
		t.Fatalf("attempt_count = %d, want 1", row.attemptCount)
	}
}

func restoreExecuteHooks(t *testing.T) {
	t.Helper()

	previousPrepare := executePrepareAccountProfile
	previousCompleteRegistration := executeCompleteRegistrationFlow
	previousNewPurchase := executeNewPurchase
	previousCompleteLogin := executeCompleteCodexLoginFlow
	previousSave := executeSaveCredentialFile

	t.Cleanup(func() {
		executePrepareAccountProfile = previousPrepare
		executeCompleteRegistrationFlow = previousCompleteRegistration
		executeNewPurchase = previousNewPurchase
		executeCompleteCodexLoginFlow = previousCompleteLogin
		executeSaveCredentialFile = previousSave
	})
}
