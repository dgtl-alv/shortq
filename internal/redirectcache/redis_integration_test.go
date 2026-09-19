package redirectcache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisCacheRoundTripAndExactTTL(t *testing.T) {
	rawURL := os.Getenv("TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	cache, err := NewRedis(rawURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	ctx := context.Background()
	value, ok := FromLink(testLink())
	if !ok {
		t.Fatal("test link should be cacheable")
	}
	if err := cache.Set(ctx, "ttl.example", "ttl-test", value, DefaultTTL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Delete(ctx, "ttl.example", "ttl-test") })

	got, found, err := cache.Get(ctx, "ttl.example", "ttl-test")
	if err != nil || !found || got.TargetURL != value.TargetURL {
		t.Fatalf("got=%#v found=%v err=%v", got, found, err)
	}

	options, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	remaining, err := client.TTL(ctx, Key("ttl.example", "ttl-test")).Result()
	if err != nil {
		t.Fatal(err)
	}
	if remaining > DefaultTTL || remaining < DefaultTTL-time.Second {
		t.Fatalf("TTL=%s, want Redis TTL initialized to exactly %s", remaining, DefaultTTL)
	}
}
