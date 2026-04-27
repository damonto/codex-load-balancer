package main

import (
	"errors"
	"path/filepath"
	"sync"
	"time"
)

const defaultLimitPoints = 100.0

type WindowUsage struct {
	UsedPercent       float64
	LimitPercent      float64
	Known             bool
	ResetAt           time.Time
	ResetAfterSeconds int
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

type TokenState struct {
	ID            string
	Path          string
	Token         string
	AccountID     string
	Email         string
	PlanType      string
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

func (s *TokenStore) NoteFileMod(path string, modTime time.Time) {
	s.mu.Lock()
	s.fileMod[path] = modTime
	s.mu.Unlock()
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
	if token.AccountID != "" {
		existing.AccountID = token.AccountID
	}
	if token.Email != "" {
		existing.Email = token.Email
	}
	if token.PlanType != "" {
		existing.PlanType = token.PlanType
	}
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

type usageAccountMetadata struct {
	AccountID string
	Email     string
	PlanType  string
}

func (s *TokenStore) UpdateUsageAccountMetadata(id string, metadata usageAccountMetadata) {
	s.mu.Lock()
	token, ok := s.tokens[id]
	if ok {
		if token.AccountID == "" && metadata.AccountID != "" {
			token.AccountID = metadata.AccountID
		}
		if metadata.Email != "" {
			token.Email = metadata.Email
		}
		if metadata.PlanType != "" {
			token.PlanType = metadata.PlanType
		}
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

func (s *TokenStore) RemoveToken(id string) (TokenState, bool) {
	s.mu.Lock()
	token, ok := s.tokens[id]
	if !ok {
		s.mu.Unlock()
		return TokenState{}, false
	}
	removed := *token
	delete(s.tokens, id)
	if removed.Path != "" {
		delete(s.fileMod, removed.Path)
	}
	s.mu.Unlock()

	s.refreshMu.Lock()
	delete(s.refreshes, id)
	s.refreshMu.Unlock()

	s.ClearSessionsForToken(id)
	return removed, true
}

func (s *TokenStore) PruneMissingTokens(dir string, existingPaths map[string]struct{}) []TokenState {
	cleanDir := filepath.Clean(dir)
	removedIDs := make([]string, 0)
	removed := make([]TokenState, 0)

	s.mu.Lock()
	for id, token := range s.tokens {
		if token.Path == "" {
			continue
		}
		cleanPath := filepath.Clean(token.Path)
		if filepath.Dir(cleanPath) != cleanDir {
			continue
		}
		if _, ok := existingPaths[cleanPath]; ok {
			continue
		}

		removed = append(removed, *token)
		removedIDs = append(removedIDs, id)
		delete(s.tokens, id)
		delete(s.fileMod, cleanPath)
	}
	s.mu.Unlock()

	if len(removedIDs) == 0 {
		return nil
	}

	s.refreshMu.Lock()
	for _, id := range removedIDs {
		delete(s.refreshes, id)
	}
	s.refreshMu.Unlock()

	for _, id := range removedIDs {
		s.ClearSessionsForToken(id)
	}
	return removed
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
	Token                   TokenState
	FiveHourRemainingPoints float64
	WeeklyRemainingPoints   float64
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
			Token:                   *token,
			FiveHourRemainingPoints: token.FiveHour.RemainingPoints(),
			WeeklyRemainingPoints:   token.Weekly.RemainingPoints(),
		})
	}
	s.mu.RUnlock()
	return candidates
}

func (s *TokenStore) HasAvailableToken(tried map[string]bool) bool {
	now := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, token := range s.tokens {
		if tried[token.ID] {
			continue
		}
		if token.Available(now) {
			return true
		}
	}
	return false
}

func selectBestCandidate(candidates []TokenCandidate) TokenState {
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if betterTokenCandidate(candidate, best) {
			best = candidate
		}
	}
	return best.Token
}

func betterTokenCandidate(candidate TokenCandidate, current TokenCandidate) bool {
	candidateBottleneck := min(candidate.FiveHourRemainingPoints, candidate.WeeklyRemainingPoints)
	currentBottleneck := min(current.FiveHourRemainingPoints, current.WeeklyRemainingPoints)
	if candidateBottleneck != currentBottleneck {
		return candidateBottleneck > currentBottleneck
	}
	if candidate.FiveHourRemainingPoints != current.FiveHourRemainingPoints {
		return candidate.FiveHourRemainingPoints > current.FiveHourRemainingPoints
	}
	if candidate.WeeklyRemainingPoints != current.WeeklyRemainingPoints {
		return candidate.WeeklyRemainingPoints > current.WeeklyRemainingPoints
	}
	return candidate.Token.ID < current.Token.ID
}

type AccountInfo struct {
	Email    string
	PlanType string
}

func (s *TokenStore) ValidAccountCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{}, len(s.tokens))
	for _, t := range s.tokens {
		if t.Invalid {
			continue
		}
		key := accountKeyFromToken(*t)
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
	}
	return len(seen)
}

func (s *TokenStore) AccountInfos() map[string]AccountInfo {
	s.mu.RLock()
	result := make(map[string]AccountInfo, len(s.tokens))
	for _, token := range s.tokens {
		if token.Invalid {
			continue
		}
		key := accountKeyFromToken(*token)
		if key == "" {
			continue
		}
		if _, ok := result[key]; !ok {
			result[key] = AccountInfo{Email: token.Email, PlanType: token.PlanType}
		}
	}
	s.mu.RUnlock()
	return result
}

type TokenRef struct {
	ID        string
	Token     string
	AccountID string
}

type TokenStats struct {
	ID                        string     `json:"id"`
	Status                    string     `json:"status"`
	FiveHourLimit             float64    `json:"five_hour_limit"`
	FiveHourRemaining         float64    `json:"five_hour_remaining"`
	FiveHourResetAt           *time.Time `json:"five_hour_reset_at"`
	FiveHourResetAfterSeconds *int       `json:"five_hour_reset_after_seconds"`
	WeeklyLimit               float64    `json:"weekly_limit"`
	WeeklyRemaining           float64    `json:"weekly_remaining"`
	WeeklyResetAt             *time.Time `json:"weekly_reset_at"`
	WeeklyResetAfterSeconds   *int       `json:"weekly_reset_after_seconds"`
	LastSync                  *time.Time `json:"last_sync"`
}

type AccountQuotaSnapshot struct {
	HasFiveHour               bool
	FiveHourUsed              float64
	FiveHourMax               float64
	FiveHourResetAt           time.Time
	FiveHourResetAfterSeconds int
	HasWeekly                 bool
	WeeklyUsed                float64
	WeeklyMax                 float64
	WeeklyResetAt             time.Time
	WeeklyResetAfterSeconds   int
}

type quotaWindowSnapshot struct {
	hasData           bool
	used              float64
	limit             float64
	resetAt           time.Time
	resetAfterSeconds int
	synced            time.Time
}

func mergeQuotaWindow(target *quotaWindowSnapshot, window WindowUsage, syncedAt time.Time) {
	if !window.Known || window.LimitPercent <= 0 {
		return
	}
	if !target.hasData || syncedAt.After(target.synced) {
		target.hasData = true
		target.used = window.UsedPercent
		target.limit = window.LimitPercent
		target.resetAt = window.ResetAt
		target.resetAfterSeconds = window.ResetAfterSeconds
		target.synced = syncedAt
	}
}

func (s *TokenStore) AccountQuotaSnapshots() map[string]AccountQuotaSnapshot {
	type quotaPair struct {
		fiveHour quotaWindowSnapshot
		weekly   quotaWindowSnapshot
	}

	s.mu.RLock()
	aggregates := make(map[string]*quotaPair)
	for _, token := range s.tokens {
		if token.Invalid {
			continue
		}

		accountKey := accountKeyFromToken(*token)
		if accountKey == "" {
			continue
		}
		pair, ok := aggregates[accountKey]
		if !ok {
			pair = &quotaPair{}
			aggregates[accountKey] = pair
		}

		mergeQuotaWindow(&pair.fiveHour, token.FiveHour, token.LastSync)
		mergeQuotaWindow(&pair.weekly, token.Weekly, token.LastSync)
	}
	s.mu.RUnlock()

	results := make(map[string]AccountQuotaSnapshot, len(aggregates))
	for accountKey, pair := range aggregates {
		snapshot := AccountQuotaSnapshot{}
		if pair.fiveHour.hasData {
			snapshot.HasFiveHour = true
			snapshot.FiveHourUsed = pair.fiveHour.used
			snapshot.FiveHourMax = pair.fiveHour.limit
			snapshot.FiveHourResetAt = pair.fiveHour.resetAt
			snapshot.FiveHourResetAfterSeconds = pair.fiveHour.resetAfterSeconds
			if snapshot.FiveHourResetAt.IsZero() && snapshot.FiveHourResetAfterSeconds > 0 && !pair.fiveHour.synced.IsZero() {
				snapshot.FiveHourResetAt = pair.fiveHour.synced.Add(time.Duration(snapshot.FiveHourResetAfterSeconds) * time.Second)
			}
		}
		if pair.weekly.hasData {
			snapshot.HasWeekly = true
			snapshot.WeeklyUsed = pair.weekly.used
			snapshot.WeeklyMax = pair.weekly.limit
			snapshot.WeeklyResetAt = pair.weekly.resetAt
			snapshot.WeeklyResetAfterSeconds = pair.weekly.resetAfterSeconds
			if snapshot.WeeklyResetAt.IsZero() && snapshot.WeeklyResetAfterSeconds > 0 && !pair.weekly.synced.IsZero() {
				snapshot.WeeklyResetAt = pair.weekly.synced.Add(time.Duration(snapshot.WeeklyResetAfterSeconds) * time.Second)
			}
		}
		results[accountKey] = snapshot
	}
	return results
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
