package db

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

func OpenRedis(ctx context.Context, cfg RedisConfig) (*redis.Client, error) {
	// Redis is infrastructure for future caching/session optimizations. Stock consistency will
	// still be enforced by PostgreSQL in later stages.
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return client, nil
}

type RedisPinger struct {
	Client *redis.Client
}

func (p RedisPinger) Ping(ctx context.Context) error {
	return p.Client.Ping(ctx).Err()
}
