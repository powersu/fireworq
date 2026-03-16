package redisclient

import (
	"context"
	"time"

	"github.com/fireworq/fireworq/config"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// Client is the global Redis client instance. It is nil when the
// dispatch statistics feature is disabled.
var Client *redis.Client

// Init initializes the global Redis client using the "redis_addr"
// configuration value. If the address is empty, the feature is
// disabled and Client remains nil. On connection failure the process
// panics, matching the MySQL driver behaviour.
func Init() {
	addr := config.Get("redis_addr")
	if addr == "" {
		log.Info().Msg("Redis address not configured; dispatch statistics disabled")
		return
	}

	log.Info().Msgf("Connecting to Redis at %s ...", addr)

	Client = redis.NewClient(&redis.Options{
		Addr:            addr,
		MaxRetries:      5,
		MinRetryBackoff: 100 * time.Millisecond,
		MaxRetryBackoff: 3 * time.Second,
		PoolSize:        10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := Client.Ping(ctx).Err(); err != nil {
		log.Panic().Msgf("Failed to connect to Redis at %s: %s", addr, err)
	}

	log.Info().Msgf("Connected to Redis at %s", addr)
}

// Close gracefully closes the Redis client connection.
func Close() {
	if Client != nil {
		if err := Client.Close(); err != nil {
			log.Error().Msgf("Failed to close Redis connection: %s", err)
		}
	}
}

// IsEnabled returns true when the Redis client has been initialized.
func IsEnabled() bool {
	return Client != nil
}
