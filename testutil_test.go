package main

import (
	"encoding/json"
	"os"
	"time"
)

func writeSessionFileForTest(path string, accessToken string, accountID string, email string, planType string) error {
	payload := map[string]any{
		"auth_mode":    "chatgpt",
		"last_refresh": time.Now().UTC().Format(time.RFC3339Nano),
		"created_at":   time.Now().UTC().Format(time.RFC3339Nano),
		"tokens": map[string]string{
			"access_token": accessToken,
			"account_id":   accountID,
		},
	}
	if email != "" || planType != "" {
		payload["session"] = map[string]any{
			"user": map[string]string{
				"email": email,
			},
			"account": map[string]string{
				"planType": planType,
			},
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
