// Package cache wraps Redis with JSON get/set helpers.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrMiss is returned when the key does not exist.
var ErrMiss = errors.New("cache: miss")

type Cache struct {
	rdb *redis.Client
}

func New(ctx context.Context, addr, password string, db int) (*Cache, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis: ping %s: %w", addr, err)
	}
	return &Cache{rdb: rdb}, nil
}

// GetJSON unmarshals the value at key into dest. Returns ErrMiss when absent.
func (c *Cache) GetJSON(ctx context.Context, key string, dest any) error {
	b, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrMiss
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dest)
}

// SetJSON marshals v and stores it under key with the given TTL.
func (c *Cache) SetJSON(ctx context.Context, key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key, b, ttl).Err()
}

func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	return c.rdb.Del(ctx, keys...).Err()
}

func (c *Cache) Close() error { return c.rdb.Close() }
