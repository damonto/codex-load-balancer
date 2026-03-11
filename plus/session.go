package plus

import (
	"context"
	"fmt"
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
