// Package redisx provides Redis connection setup, distributed locks and a
// fixed-window rate limiter.
package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"leadline/pkg/secure"
)

func Connect(ctx context.Context, url string) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(opts)
	var pingErr error
	for i := 0; i < 30; i++ {
		if pingErr = rdb.Ping(ctx).Err(); pingErr == nil {
			return rdb, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("redis not reachable: %w", pingErr)
}

var unlockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`)

// TryLock acquires key for ttl. It returns a release func when acquired.
// Only the holder's token can release the lock.
func TryLock(ctx context.Context, rdb *redis.Client, key string, ttl time.Duration) (release func(), ok bool, err error) {
	token, err := secure.RandomToken(16)
	if err != nil {
		return nil, false, err
	}
	ok, err = rdb.SetNX(ctx, "lock:"+key, token, ttl).Result()
	if err != nil || !ok {
		return nil, false, err
	}
	return func() {
		unlockScript.Run(context.Background(), rdb, []string{"lock:" + key}, token) //nolint:errcheck
	}, true, nil
}

// Limiter is a fixed-window counter: at most Limit hits per Window per key.
type Limiter struct {
	rdb    *redis.Client
	Limit  int64
	Window time.Duration
	Prefix string
}

func NewLimiter(rdb *redis.Client, prefix string, limit int64, window time.Duration) *Limiter {
	return &Limiter{rdb: rdb, Limit: limit, Window: window, Prefix: prefix}
}

// Allow records a hit for key and reports whether it is within the limit.
// On Redis errors it fails open so an outage does not take down the API.
func (l *Limiter) Allow(ctx context.Context, key string) bool {
	bucket := time.Now().UnixNano() / int64(l.Window)
	k := fmt.Sprintf("rl:%s:%s:%d", l.Prefix, key, bucket)
	pipe := l.rdb.Pipeline()
	incr := pipe.Incr(ctx, k)
	pipe.Expire(ctx, k, l.Window+time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return true
	}
	return incr.Val() <= l.Limit
}
