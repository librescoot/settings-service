package redis

import (
	"context"
	"fmt"

	"github.com/go-redis/redis/v8"
)

const (
	SettingsKey     = "settings"
	SettingsChannel = "settings"
)

type Client struct {
	client *redis.Client
	pubsub *redis.PubSub
	ctx    context.Context
}

// NewClient creates a new Redis client with pubsub subscription
func NewClient(ctx context.Context, addr string) (*Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "",
		DB:       0,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	pubsub := client.Subscribe(ctx, SettingsChannel)

	return &Client{
		client: client,
		pubsub: pubsub,
		ctx:    ctx,
	}, nil
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

// ReplaceSettings atomically deletes all settings, writes new ones, and
// publishes a reload notification so subscribers refresh their state.
func (c *Client) ReplaceSettings(fields map[string]interface{}) error {
	pipe := c.client.Pipeline()
	pipe.Del(c.ctx, SettingsKey)
	for field, value := range fields {
		pipe.HSet(c.ctx, SettingsKey, field, value)
	}
	pipe.Publish(c.ctx, SettingsChannel, "reload")
	_, err := pipe.Exec(c.ctx)
	return err
}

// WatchChannel returns the pubsub channel for monitoring updates
func (c *Client) WatchChannel() <-chan *redis.Message {
	return c.pubsub.Channel()
}

// Close cleanly shuts down the Redis connections
func (c *Client) Close() {
	c.pubsub.Close()
	c.client.Close()
}