package main

import (
	"context"
	"log/slog"
	"sync"
)

type UsageSink struct {
	db *UsageDB
	ch chan UsageRecord
	wg sync.WaitGroup
}

func NewUsageSink(db *UsageDB, queueSize int) *UsageSink {
	if queueSize <= 0 {
		queueSize = 512
	}
	return &UsageSink{
		db: db,
		ch: make(chan UsageRecord, queueSize),
	}
}

func (s *UsageSink) Run(ctx context.Context) {
	if s == nil || s.db == nil {
		return
	}
	s.wg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				s.drain()
				return
			case rec := <-s.ch:
				s.insertUsage(rec, "record usage")
			}
		}
	})
}

func (s *UsageSink) Wait() {
	if s == nil {
		return
	}
	s.wg.Wait()
}

func (s *UsageSink) Record(rec UsageRecord) {
	if s == nil || s.db == nil {
		return
	}
	select {
	case s.ch <- rec:
	default:
		slog.Warn("usage queue full; dropping record", "account", rec.AccountKey, "token", rec.TokenID)
	}
}

func (s *UsageSink) drain() {
	for {
		select {
		case rec := <-s.ch:
			s.insertUsage(rec, "record usage during drain")
		default:
			return
		}
	}
}

func (s *UsageSink) insertUsage(rec UsageRecord, message string) {
	if err := s.db.InsertUsage(context.Background(), rec); err != nil {
		slog.Warn(message, "account", rec.AccountKey, "token", rec.TokenID, "err", err)
	}
}
