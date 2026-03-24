package stats

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// Writer writes dispatch statistics to Redis using a pipeline.
type Writer struct {
	client *redis.Client
}

// NewWriter creates a new Writer backed by the given Redis client.
func NewWriter(client *redis.Client) *Writer {
	return &Writer{client: client}
}

// RecordDispatch records a single dispatch result into the 5-minute
// and 1-hour tumbling window buckets and updates the active
// subscribers sorted set. It is safe to call from multiple goroutines.
//
// Errors are logged but never propagated — dispatch must not be
// blocked by statistics failures.
func (w *Writer) RecordDispatch(targetEnv string, orgID string, isSuccess bool, isPermanentFail bool) {
	if w == nil || w.client == nil || orgID == "" || targetEnv == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	now := time.Now()

	// 5-minute bucket: truncate minute to 5-min boundary (00,05,10,...,55)
	fiveMinBucket := now.Minute() - (now.Minute() % 5)
	fiveMinKey := fmt.Sprintf("webhook:%s:stats:%s:5m:%s%02d",
		targetEnv, orgID, now.Format("2006010215"), fiveMinBucket)

	// 1-hour bucket
	oneHourKey := fmt.Sprintf("webhook:%s:stats:%s:1h:%s",
		targetEnv, orgID, now.Format("2006010215"))

	pipe := w.client.Pipeline()

	// Increment counters for both buckets
	for _, key := range []string{fiveMinKey, oneHourKey} {
		pipe.HIncrBy(ctx, key, "total", 1)
		switch {
		case isSuccess:
			pipe.HIncrBy(ctx, key, "success", 1)
		case isPermanentFail:
			pipe.HIncrBy(ctx, key, "fail", 1)
			pipe.HIncrBy(ctx, key, "permanent_fail", 1)
		default:
			pipe.HIncrBy(ctx, key, "fail", 1)
		}
	}

	// Set TTLs
	pipe.Expire(ctx, fiveMinKey, 2*time.Hour)
	pipe.Expire(ctx, oneHourKey, 25*time.Hour)

	// Update active subscribers sorted set
	activeKey := fmt.Sprintf("webhook:%s:active_subscribes", targetEnv)
	pipe.ZAdd(ctx, activeKey, redis.Z{
		Score:  float64(now.Unix()),
		Member: orgID,
	})

	if _, err := pipe.Exec(ctx); err != nil {
		log.Error().Msgf("Failed to write dispatch stats to Redis for env=%s org_id=%s: %s", targetEnv, orgID, err)
	}
}
