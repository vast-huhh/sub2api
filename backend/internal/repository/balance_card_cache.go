package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const balanceCardWalletCacheTTL = time.Minute

type balanceCardCache struct {
	rdb *redis.Client
}

func NewBalanceCardCache(rdb *redis.Client) service.BalanceCardCache {
	return &balanceCardCache{rdb: rdb}
}

func balanceCardWalletKey(userID int64) string {
	return fmt.Sprintf("billing:balance-card:%d", userID)
}

func (c *balanceCardCache) Get(ctx context.Context, userID int64) (*service.BalanceCardWalletSnapshot, bool, error) {
	if c == nil || c.rdb == nil {
		return nil, false, nil
	}
	raw, err := c.rdb.Get(ctx, balanceCardWalletKey(userID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if string(raw) == "null" {
		return nil, true, nil
	}
	var snapshot service.BalanceCardWalletSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, false, err
	}
	return &snapshot, true, nil
}

func (c *balanceCardCache) Set(ctx context.Context, userID int64, snapshot *service.BalanceCardWalletSnapshot) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	ttl := balanceCardWalletCacheTTL
	if snapshot != nil {
		untilExpiry := time.Until(snapshot.ExpiresAt)
		if untilExpiry > 0 && untilExpiry < ttl {
			ttl = untilExpiry
		}
	}
	if ttl <= 0 {
		ttl = time.Second
	}
	return c.rdb.Set(ctx, balanceCardWalletKey(userID), raw, ttl).Err()
}

func (c *balanceCardCache) Invalidate(ctx context.Context, userID int64) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Del(ctx, balanceCardWalletKey(userID)).Err()
}
