package redis

import (
	"context"
	"fmt"
	"log"

	"github.com/redis/go-redis/v9"
)

const (
	SettingsKey     = "settings"
	SettingsChannel = "settings"
	SchemaKey       = "settings:schema"

	// OverlayList is the command queue for apply:service and clear:service.
	OverlayList = "settings:overlay"
)

type Client struct {
	client *redis.Client
	pubsub *redis.PubSub
	ctx    context.Context
}

// Subscribe is deliberately deferred until boot hydration is complete.
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

func (c *Client) Subscribe() {
	c.pubsub = c.client.Subscribe(c.ctx, SettingsChannel)
}

func (c *Client) FlushSettings() error {
	return c.client.Del(c.ctx, SettingsKey).Err()
}

func (c *Client) GetAllSettings() (map[string]string, error) {
	return c.client.HGetAll(c.ctx, SettingsKey).Result()
}

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

// ReplaceSettings atomically publishes one field notification per updated value,
// so consumers never observe a partial settings hash.
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

// DeleteSettingsFields also notifies consumers to clear stale transient values.
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

func (c *Client) WatchChannel() <-chan *redis.Message {
	return c.pubsub.Channel()
}

func (c *Client) SetKey(key, value string) error {
	return c.client.Set(c.ctx, key, value, 0).Err()
}

func (c *Client) Close() {
	if err := c.pubsub.Close(); err != nil {
		log.Printf("Error closing Redis pubsub: %v", err)
	}
	if err := c.client.Close(); err != nil {
		log.Printf("Error closing Redis client: %v", err)
	}
}

func (c *Client) BRPopOverlay() (string, error) {
	res, err := c.client.BRPop(c.ctx, 0, OverlayList).Result()
	if err != nil {
		return "", err
	}
	if len(res) < 2 {
		return "", fmt.Errorf("unexpected BRPop response")
	}
	return res[1], nil
}

func (c *Client) GetSettingField(field string) (value string, existed bool, err error) {
	v, err := c.client.HGet(c.ctx, SettingsKey, field).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetSettingField follows the public per-field settings notification contract.
func (c *Client) SetSettingField(field, value string) error {
	_, err := c.client.TxPipelined(c.ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(c.ctx, SettingsKey, field, value)
		pipe.Publish(c.ctx, SettingsChannel, field)
		return nil
	})
	return err
}
