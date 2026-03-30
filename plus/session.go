package plus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type ChatGPTSession struct {
	User         ChatGPTSessionUser    `json:"user"`
	Account      ChatGPTSessionAccount `json:"account"`
	AccessToken  string                `json:"accessToken"`
	SessionToken string                `json:"sessionToken"`
}

type ChatGPTSessionUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type ChatGPTSessionAccount struct {
	ID       string `json:"id"`
	PlanType string `json:"planType"`
}

func (r *registrationFlow) fetchSession(ctx context.Context) (ChatGPTSession, error) {
	var payload ChatGPTSession
	err := r.client.GetJSON(ctx, chatgptURL+"/api/auth/session", nil, &payload)
	if err != nil {
		return ChatGPTSession{}, fmt.Errorf("get auth session: %w", err)
	}
	return payload, nil
}

func writeCredentialFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credential-*")
	if err != nil {
		return fmt.Errorf("create temp credential file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp credential file: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp credential file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp credential file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp credential file: %w", err)
	}
	return nil
}
