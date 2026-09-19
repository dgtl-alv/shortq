package redirectcache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const DefaultOperationTimeout = 250 * time.Millisecond

type RedisCache struct {
	client  *redis.Client
	timeout time.Duration
}

func NewRedis(rawURL string, timeout time.Duration) (*RedisCache, error) {
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = DefaultOperationTimeout
	}
	options.DialTimeout = timeout
	options.ReadTimeout = timeout
	options.WriteTimeout = timeout
	options.MaxRetries = 1
	options.MinRetryBackoff = 10 * time.Millisecond
	options.MaxRetryBackoff = 50 * time.Millisecond
	return &RedisCache{client: redis.NewClient(options), timeout: timeout}, nil
}

func (r *RedisCache) Get(ctx context.Context, hostname, slug string) (CachedRedirect, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	data, err := r.client.Get(ctx, Key(hostname, slug)).Bytes()
	if errors.Is(err, redis.Nil) {
		return CachedRedirect{}, false, nil
	}
	if err != nil {
		return CachedRedirect{}, false, err
	}
	var value CachedRedirect
	if err := json.Unmarshal(data, &value); err != nil {
		return CachedRedirect{}, false, err
	}
	if value.Version != SchemaVersion {
		return CachedRedirect{}, false, nil
	}
	return value, true, nil
}

func (r *RedisCache) Set(ctx context.Context, hostname, slug string, value CachedRedirect, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	return r.client.Set(ctx, Key(hostname, slug), data, ttl).Err()
}

func (r *RedisCache) Delete(ctx context.Context, hostname, slug string) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	return r.client.Del(ctx, Key(hostname, slug)).Err()
}

func (r *RedisCache) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	return r.client.Ping(ctx).Err()
}

func (r *RedisCache) Close() error { return r.client.Close() }
