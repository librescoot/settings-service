package redis

import (
	"context"
	"fmt"

	"github.com/go-redis/redis/v8"
)

const (
	SettingsKey     = "settings"
	SettingsChannel = "settings"
	SchemaKey       = "settings:schema"
)

type Client struct {
	client *redis.Client
	pubsub *redis.PubSub
	ctx    context.Context
}

// NewClient creates a new Redis client. The pubsub subscription is deferred
// until Subscribe() is called explicitly, so the initial bulk load in
// ReplaceSettings can publish per-field notifications without the service's
// own subscriber treating them as user-initiated changes.
func NewClient(ctx context.Context, addr string) (*Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "",
		DB:       0,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &Client{
		client: client,
		ctx:    ctx,
	}, nil
}

// Subscribe opens the pubsub subscription on SettingsChannel. Call this after
// any startup work that publishes on the channel (e.g. ReplaceSettings), so
// the service doesn't receive its own bulk-load notifications.
func (c *Client) Subscribe() {
	c.pubsub = c.client.Subscribe(c.ctx, SettingsChannel)
}

// FlushSettings clears all settings from Redis
func (c *Client) FlushSettings() error {
	return c.client.Del(c.ctx, SettingsKey).Err()
}

// GetAllSettings retrieves all settings from Redis
func (c *Client) GetAllSettings() (map[string]string, error) {
	return c.client.HGetAll(c.ctx, SettingsKey).Result()
}

// SetSettings stores multiple settings in Redis
func (c *Client) SetSettings(fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}

	pipe := c.client.Pipeline()
	for field, value := range fields {
		pipe.HSet(c.ctx, SettingsKey, field, value)
	}

	_, err := pipe.Exec(c.ctx)
	return err
}

// ReplaceSettings writes the given fields to the settings hash inside a
// MULTI/EXEC transaction and publishes one notification per field on
// SettingsChannel. The transaction guarantees that no other client can
// observe a partial hash mid-write, and the per-field publishes let
// subscribers react with the same code path they use for runtime HSETs —
// no special "everything changed" sentinel.
//
// Redis is not persisted on this platform, so the hash starts empty at
// boot and there's nothing to clear before writing.
func (c *Client) ReplaceSettings(fields map[string]interface{}) error {
	_, err := c.client.TxPipelined(c.ctx, func(pipe redis.Pipeliner) error {
		for field, value := range fields {
			pipe.HSet(c.ctx, SettingsKey, field, value)
		}
		for field := range fields {
			pipe.Publish(c.ctx, SettingsChannel, field)
		}
		return nil
	})
	return err
}

// DeleteSettingsFields removes the named fields from the settings hash
// and publishes a notification per field. Used by LoadSettingsFromTOML to
// clear transient keys that survived a service restart (Redis isn't
// restarted alongside settings-service, so the hash can carry stale
// transient values from the previous instance).
func (c *Client) DeleteSettingsFields(fields []string) error {
	if len(fields) == 0 {
		return nil
	}
	_, err := c.client.TxPipelined(c.ctx, func(pipe redis.Pipeliner) error {
		pipe.HDel(c.ctx, SettingsKey, fields...)
		for _, f := range fields {
			pipe.Publish(c.ctx, SettingsChannel, f)
		}
		return nil
	})
	return err
}

// WatchChannel returns the pubsub channel for monitoring updates
func (c *Client) WatchChannel() <-chan *redis.Message {
	return c.pubsub.Channel()
}

// SetKey sets a plain string key in Redis.
func (c *Client) SetKey(key, value string) error {
	return c.client.Set(c.ctx, key, value, 0).Err()
}

// Close cleanly shuts down the Redis connections
func (c *Client) Close() {
	c.pubsub.Close()
	c.client.Close()
}
