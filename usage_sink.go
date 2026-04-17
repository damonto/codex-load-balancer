package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

var errUsageSinkStopped = errors.New("usage sink stopped")
var errUsageSinkFull = errors.New("usage sink full")

type usageOverflowKey struct {
	AccountKey string
	BucketUnix int64
}

type UsageSink struct {
	db               *UsageDB
	queueCapacity    int
	overflowCapacity int
	runOnce          sync.Once
	stopOnce         sync.Once
	mu               sync.Mutex
	cond             *sync.Cond
	queued           []UsageRecord
	overflow         map[usageOverflowKey]UsageRecord
	stopped          bool
	wg               sync.WaitGroup
}

func NewUsageSink(db *UsageDB, queueSize int) *UsageSink {
	if queueSize <= 0 {
		queueSize = 512
	}
	sink := &UsageSink{
		db:               db,
		queueCapacity:    queueSize,
		overflowCapacity: queueSize,
		queued:           make([]UsageRecord, 0, queueSize),
		overflow:         make(map[usageOverflowKey]UsageRecord, queueSize),
	}
	sink.cond = sync.NewCond(&sink.mu)
	return sink
}

func (s *UsageSink) Run() {
	if s == nil || s.db == nil {
		return
	}
	s.runOnce.Do(func() {
		s.wg.Go(func() {
			for {
				records, ok := s.takeBatch()
				if !ok {
					return
				}
				s.insertUsageBatch(records)
			}
		})
	})
}

func (s *UsageSink) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.cond.Broadcast()
		s.mu.Unlock()
	})
}

func (s *UsageSink) Wait() {
	if s == nil {
		return
	}
	s.wg.Wait()
}

func (s *UsageSink) Record(rec UsageRecord) error {
	if s == nil || s.db == nil {
		return nil
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return errUsageSinkStopped
	}
	if len(s.queued) < s.queueCapacity {
		s.queued = append(s.queued, rec)
		s.cond.Signal()
		s.mu.Unlock()
		return nil
	}
	if err := s.queueOverflow(rec); err != nil {
		s.mu.Unlock()
		return err
	}
	s.cond.Signal()
	s.mu.Unlock()
	return nil
}

func (s *UsageSink) takeBatch() ([]UsageRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for len(s.queued) == 0 && len(s.overflow) == 0 && !s.stopped {
		s.cond.Wait()
	}
	if len(s.queued) == 0 && len(s.overflow) == 0 && s.stopped {
		return nil, false
	}

	batch := make([]UsageRecord, 0, len(s.queued)+len(s.overflow))
	batch = append(batch, s.queued...)
	for _, rec := range s.overflow {
		batch = append(batch, rec)
	}
	s.queued = make([]UsageRecord, 0, s.queueCapacity)
	s.overflow = make(map[usageOverflowKey]UsageRecord, s.overflowCapacity)
	return batch, true
}

func (s *UsageSink) queueOverflow(rec UsageRecord) error {
	key := usageOverflowBucket(rec)
	if existing, ok := s.overflow[key]; ok {
		s.overflow[key] = mergeUsageRecord(existing, rec, key.BucketUnix)
		return nil
	}
	if len(s.overflow) >= s.overflowCapacity {
		return errUsageSinkFull
	}
	rec.CreatedAt = time.Unix(key.BucketUnix, 0).UTC()
	s.overflow[key] = rec
	return nil
}

func usageOverflowBucket(rec UsageRecord) usageOverflowKey {
	createdAt := rec.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	createdAt = createdAt.UTC().Truncate(time.Hour)
	return usageOverflowKey{
		AccountKey: rec.AccountKey,
		BucketUnix: createdAt.Unix(),
	}
}

func mergeUsageRecord(base UsageRecord, next UsageRecord, bucketUnix int64) UsageRecord {
	base.InputTokens += next.InputTokens
	base.CachedTokens += next.CachedTokens
	base.OutputTokens += next.OutputTokens
	base.ReasoningTokens += next.ReasoningTokens
	base.CreatedAt = time.Unix(bucketUnix, 0).UTC()
	return base
}

func (s *UsageSink) insertUsage(rec UsageRecord, message string) {
	if err := s.db.InsertUsage(context.Background(), rec); err != nil {
		slog.Warn(message, "account", rec.AccountKey, "token", rec.TokenID, "err", err)
	}
}

func (s *UsageSink) insertUsageBatch(records []UsageRecord) {
	if len(records) == 0 {
		return
	}
	if len(records) == 1 {
		s.insertUsage(records[0], "record usage")
		return
	}
	err := s.db.InsertUsageBatch(context.Background(), records)
	if err == nil {
		return
	}

	slog.Warn("record usage batch", "count", len(records), "err", err)
	for _, rec := range records {
		s.insertUsage(rec, "record usage fallback")
	}
}
