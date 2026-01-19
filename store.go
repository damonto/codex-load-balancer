package main

import (
	"errors"
	"sort"
	"sync"
	"time"
)

type WindowUsage struct {
	UsedPercent       float64
	LimitPercent      float64
	Known             bool
	ResetAt           time.Time
	ResetAfterSeconds int
}

func (w WindowUsage) RemainingPercent() float64 {
	if !w.Known || w.LimitPercent <= 0 {
		return 1.0
	}
	remaining := w.LimitPercent - w.UsedPercent
	if remaining < 0 {
		return 0
	}
	return remaining / w.LimitPercent
}

func (w WindowUsage) RemainingPoints() float64 {
	if !w.Known || w.LimitPercent <= 0 {
		return defaultLimitPoints
	}
	remaining := w.LimitPercent - w.UsedPercent
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (w WindowUsage) LimitForRanking() float64 {
	if w.Known && w.LimitPercent > 0 {
		return w.LimitPercent
	}
	return defaultLimitPoints
}

type TokenState struct {
	ID            string
	Path          string
	Token         string
	AccountID     string
	RefreshToken  string
	LastRefresh   time.Time
	Invalid       bool
	CooldownUntil time.Time
	FiveHour      WindowUsage
	Weekly        WindowUsage
	LastSync      time.Time
}

func (t TokenState) Available(now time.Time) bool {
	if t.Invalid {
		return false
	}
	if !t.CooldownUntil.IsZero() && now.Before(t.CooldownUntil) {
		return false
	}
	if t.FiveHour.Known && t.FiveHour.RemainingPoints() <= 0 {
		return false
	}
	if t.Weekly.Known && t.Weekly.RemainingPoints() <= 0 {
		return false
	}
	return true
}

type TokenStore struct {
	mu        sync.RWMutex
	tokens    map[string]*TokenState
	fileMod   map[string]time.Time
	sessMu    sync.RWMutex
	sessions  map[string]string
	refreshMu sync.Mutex
	refreshes map[string]*sync.Mutex
}

func NewTokenStore() *TokenStore {
	return &TokenStore{
		tokens:    make(map[string]*TokenState),
		fileMod:   make(map[string]time.Time),
		sessions:  make(map[string]string),
		refreshes: make(map[string]*sync.Mutex),
	}
}

func (s *TokenStore) ShouldReload(path string, modTime time.Time) bool {
	s.mu.RLock()
	last, ok := s.fileMod[path]
	s.mu.RUnlock()
	if !ok {
		return true
	}
	return modTime.After(last)
}

func (s *TokenStore) UpsertToken(token TokenState, modTime time.Time) (added bool, updated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.tokens[token.ID]
	if !ok {
		s.tokens[token.ID] = &token
		s.fileMod[token.Path] = modTime
		return true, false
	}

	existing.Token = token.Token
	existing.AccountID = token.AccountID
	existing.RefreshToken = token.RefreshToken
	existing.Path = token.Path
	if !token.LastRefresh.IsZero() {
		existing.LastRefresh = token.LastRefresh
	}
	existing.Invalid = false
	existing.CooldownUntil = time.Time{}
	s.fileMod[token.Path] = modTime
	return false, true
}

func (s *TokenStore) MarkInvalid(id string) {
	s.mu.Lock()
	token, ok := s.tokens[id]
	if ok {
		token.Invalid = true
	}
	s.mu.Unlock()
}

func (s *TokenStore) MarkCooldown(id string, until time.Time) {
	s.mu.Lock()
	token, ok := s.tokens[id]
	if ok {
		token.CooldownUntil = until
	}
	s.mu.Unlock()
}

func (s *TokenStore) UpdateUsage(id string, fiveHour WindowUsage, weekly WindowUsage, now time.Time) {
	s.mu.Lock()
	token, ok := s.tokens[id]
	if ok {
		token.FiveHour = fiveHour
		token.Weekly = weekly
		token.LastSync = now
	}
	s.mu.Unlock()
}

func (s *TokenStore) UpdateCredentials(id string, accessToken string, refreshToken string) {
	s.mu.Lock()
	token, ok := s.tokens[id]
	if ok {
		token.Token = accessToken
		token.RefreshToken = refreshToken
		token.LastRefresh = time.Now().UTC()
		token.Invalid = false
		token.CooldownUntil = time.Time{}
	}
	s.mu.Unlock()
}

func (s *TokenStore) TokenSnapshot(id string) (TokenState, bool) {
	s.mu.RLock()
	token, ok := s.tokens[id]
	if !ok {
		s.mu.RUnlock()
		return TokenState{}, false
	}
	snapshot := *token
	s.mu.RUnlock()
	return snapshot, true
}

func (s *TokenStore) TokenRefs() []TokenRef {
	s.mu.RLock()
	refs := make([]TokenRef, 0, len(s.tokens))
	for _, token := range s.tokens {
		if token.Invalid {
			continue
		}
		refs = append(refs, TokenRef{
			ID:        token.ID,
			Token:     token.Token,
			AccountID: token.AccountID,
		})
	}
	s.mu.RUnlock()
	return refs
}

func (s *TokenStore) SessionToken(sessionID string) (string, bool) {
	s.sessMu.RLock()
	tokenID, ok := s.sessions[sessionID]
	s.sessMu.RUnlock()
	return tokenID, ok
}

func (s *TokenStore) SetSession(sessionID string, tokenID string) {
	s.sessMu.Lock()
	s.sessions[sessionID] = tokenID
	s.sessMu.Unlock()
}

func (s *TokenStore) ClearSession(sessionID string) {
	s.sessMu.Lock()
	delete(s.sessions, sessionID)
	s.sessMu.Unlock()
}

func (s *TokenStore) ClearSessionsForToken(tokenID string) {
	s.sessMu.Lock()
	for sessionID, boundToken := range s.sessions {
		if boundToken == tokenID {
			delete(s.sessions, sessionID)
		}
	}
	s.sessMu.Unlock()
}

func (s *TokenStore) RefreshLock(id string) *sync.Mutex {
	s.refreshMu.Lock()
	lock, ok := s.refreshes[id]
	if !ok {
		lock = &sync.Mutex{}
		s.refreshes[id] = lock
	}
	s.refreshMu.Unlock()
	return lock
}

type TokenCandidate struct {
	Token                    TokenState
	WeeklyLimitPoints        float64
	FiveHourRemainingPoints  float64
	FiveHourRemainingPercent float64
}

func (s *TokenStore) SelectToken(sessionID string, tried map[string]bool) (TokenState, bool, error) {
	now := time.Now()
	if sessionID != "" {
		if tokenID, ok := s.SessionToken(sessionID); ok && !tried[tokenID] {
			if token, ok := s.TokenSnapshot(tokenID); ok && token.Available(now) {
				return token, true, nil
			}
			s.ClearSession(sessionID)
		}
	}

	candidates := s.availableCandidates(now, tried)
	if len(candidates) == 0 {
		return TokenState{}, false, errors.New("no available tokens")
	}

	return selectBestCandidate(candidates), false, nil
}

func (s *TokenStore) availableCandidates(now time.Time, tried map[string]bool) []TokenCandidate {
	s.mu.RLock()
	candidates := make([]TokenCandidate, 0, len(s.tokens))
	for _, token := range s.tokens {
		if tried[token.ID] {
			continue
		}
		if !token.Available(now) {
			continue
		}
		candidates = append(candidates, TokenCandidate{
			Token:                    *token,
			WeeklyLimitPoints:        token.Weekly.LimitForRanking(),
			FiveHourRemainingPoints:  token.FiveHour.RemainingPoints(),
			FiveHourRemainingPercent: token.FiveHour.RemainingPercent(),
		})
	}
	s.mu.RUnlock()
	return candidates
}

func selectBestCandidate(candidates []TokenCandidate) TokenState {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].WeeklyLimitPoints != candidates[j].WeeklyLimitPoints {
			return candidates[i].WeeklyLimitPoints > candidates[j].WeeklyLimitPoints
		}
		// When weekly limits tie, favor higher 5-hour remaining to keep capacity available.
		return candidates[i].FiveHourRemainingPoints > candidates[j].FiveHourRemainingPoints
	})

	top := candidates[0]
	if top.FiveHourRemainingPercent < 0.3 {
		bestIdx := -1
		for i := 1; i < len(candidates); i++ {
			if candidates[i].WeeklyLimitPoints >= top.WeeklyLimitPoints {
				continue
			}
			if candidates[i].FiveHourRemainingPoints <= top.FiveHourRemainingPoints {
				continue
			}
			if bestIdx == -1 || candidates[i].FiveHourRemainingPoints > candidates[bestIdx].FiveHourRemainingPoints {
				bestIdx = i
			}
		}
		if bestIdx != -1 {
			return candidates[bestIdx].Token
		}
	}
	return top.Token
}

type TokenRef struct {
	ID        string
	Token     string
	AccountID string
}

type TokenStats struct {
	ID                         string     `json:"id"`
	Status                     string     `json:"status"`
	FiveHourLimit              float64    `json:"five_hour_limit"`
	FiveHourRemaining          float64    `json:"five_hour_remaining"`
	FiveHourResetAt            *time.Time `json:"five_hour_reset_at"`
	FiveHourResetAfterSeconds  *int       `json:"five_hour_reset_after_seconds"`
	WeeklyLimit                float64    `json:"weekly_limit"`
	WeeklyRemaining            float64    `json:"weekly_remaining"`
	WeeklyResetAt              *time.Time `json:"weekly_reset_at"`
	WeeklyResetAfterSeconds    *int       `json:"weekly_reset_after_seconds"`
	LastSync                   *time.Time `json:"last_sync"`
}

func (s *TokenStore) Stats() []TokenStats {
	now := time.Now()
	s.mu.RLock()
	stats := make([]TokenStats, 0, len(s.tokens))
	for _, token := range s.tokens {
		status := "active"
		if token.Invalid {
			status = "invalid"
		} else if !token.CooldownUntil.IsZero() && token.CooldownUntil.After(now) {
			status = "cooldown"
		}

		var lastSync *time.Time
		if !token.LastSync.IsZero() {
			ts := token.LastSync
			lastSync = &ts
		}

		fiveHourLimit := 0.0
		fiveHourRemaining := 0.0
		if token.FiveHour.Known {
			fiveHourLimit = token.FiveHour.LimitPercent
			fiveHourRemaining = token.FiveHour.RemainingPoints()
		}

		weeklyLimit := 0.0
		weeklyRemaining := 0.0
		if token.Weekly.Known {
			weeklyLimit = token.Weekly.LimitPercent
			weeklyRemaining = token.Weekly.RemainingPoints()
		}

		var fiveHourResetAt *time.Time
		if !token.FiveHour.ResetAt.IsZero() {
			ts := token.FiveHour.ResetAt
			fiveHourResetAt = &ts
		}
		var fiveHourResetAfter *int
		if token.FiveHour.ResetAfterSeconds > 0 {
			value := token.FiveHour.ResetAfterSeconds
			fiveHourResetAfter = &value
		}

		var weeklyResetAt *time.Time
		if !token.Weekly.ResetAt.IsZero() {
			ts := token.Weekly.ResetAt
			weeklyResetAt = &ts
		}
		var weeklyResetAfter *int
		if token.Weekly.ResetAfterSeconds > 0 {
			value := token.Weekly.ResetAfterSeconds
			weeklyResetAfter = &value
		}

		stats = append(stats, TokenStats{
			ID:                        token.ID,
			Status:                    status,
			FiveHourLimit:             fiveHourLimit,
			FiveHourRemaining:         fiveHourRemaining,
			FiveHourResetAt:           fiveHourResetAt,
			FiveHourResetAfterSeconds: fiveHourResetAfter,
			WeeklyLimit:               weeklyLimit,
			WeeklyRemaining:           weeklyRemaining,
			WeeklyResetAt:             weeklyResetAt,
			WeeklyResetAfterSeconds:   weeklyResetAfter,
			LastSync:                  lastSync,
		})
	}
	s.mu.RUnlock()
	return stats
}
