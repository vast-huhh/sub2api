package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const openAIPAT401CounterPrefix = "openai_pat_401_count:account:"

var openAIPAT401CounterIncrScript = redis.NewScript(`
	local key = KEYS[1]
	local now_ms = tonumber(ARGV[1])
	local window_ms = tonumber(ARGV[2])
	local ttl_ms = tonumber(ARGV[3])
	local member = ARGV[4]

	redis.call('ZREMRANGEBYSCORE', key, '-inf', now_ms - window_ms)
	redis.call('ZADD', key, now_ms, member)
	redis.call('PEXPIRE', key, ttl_ms)

	return redis.call('ZCARD', key)
`)

type openAIPAT401CounterCache struct {
	rdb *redis.Client
}

func NewOpenAIPAT401CounterCache(rdb *redis.Client) service.OpenAIPAT401CounterCache {
	return &openAIPAT401CounterCache{rdb: rdb}
}

func (c *openAIPAT401CounterCache) IncrementOpenAIPAT401Count(ctx context.Context, accountID int64, windowSeconds int) (int64, error) {
	if windowSeconds <= 0 {
		windowSeconds = 120
	}

	key := fmt.Sprintf("%s%d", openAIPAT401CounterPrefix, accountID)
	now := time.Now()
	window := time.Duration(windowSeconds) * time.Second
	member := fmt.Sprintf("%d", now.UnixNano())

	result, err := openAIPAT401CounterIncrScript.Run(ctx, c.rdb, []string{key},
		now.UnixMilli(),
		window.Milliseconds(),
		(window + time.Minute).Milliseconds(),
		member,
	).Int64()
	if err != nil {
		return 0, fmt.Errorf("increment openai pat 401 count: %w", err)
	}
	return result, nil
}

func (c *openAIPAT401CounterCache) ResetOpenAIPAT401Count(ctx context.Context, accountID int64) error {
	key := fmt.Sprintf("%s%d", openAIPAT401CounterPrefix, accountID)
	return c.rdb.Del(ctx, key).Err()
}
