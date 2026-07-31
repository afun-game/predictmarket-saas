package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisOperationTimeout = time.Second

const consumeLaunchScript = `
local payload = redis.call('GET', KEYS[1])
if not payload then return false end
local session_id = redis.call('GET', KEYS[2])
redis.call('DEL', KEYS[1])
redis.call('DEL', KEYS[2])
if session_id then redis.call('DEL', ARGV[1] .. session_id) end
return payload`

const revokeLaunchScript = `
local indexed = redis.call('GET', KEYS[1])
if not indexed then return 0 end
local prefix = ARGV[1] .. '\0'
if string.sub(indexed, 1, string.len(prefix)) ~= prefix then return 0 end
local token = string.sub(indexed, string.len(prefix) + 1)
redis.call('DEL', KEYS[1])
redis.call('DEL', ARGV[2] .. token)
redis.call('DEL', ARGV[3] .. token)
return 1`

// RedisStore provides the production Store implementation.
type RedisStore struct {
	client *redis.Client
}

// NewRedisStore connects a Store to a Redis URL.
func NewRedisStore(redisURL string) (*RedisStore, error) {
	options, err := redis.ParseURL(strings.TrimSpace(redisURL))
	if err != nil {
		return nil, fmt.Errorf("parse session Redis URL: %w", err)
	}
	options.DialTimeout = redisOperationTimeout
	options.ReadTimeout = redisOperationTimeout
	options.WriteTimeout = redisOperationTimeout
	return &RedisStore{client: redis.NewClient(options)}, nil
}

// Close releases Redis client resources.
func (s *RedisStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *RedisStore) CreateLaunch(ctx context.Context, token string, launch Launch, ttl time.Duration) error {
	payload, err := json.Marshal(launch)
	if err != nil {
		return fmt.Errorf("encode launch session: %w", err)
	}
	pipeline := s.client.TxPipeline()
	pipeline.Set(ctx, launchKey(token), payload, ttl)
	pipeline.Set(ctx, launchTokenSessionKey(token), launch.ID, ttl)
	pipeline.Set(ctx, launchSessionKey(launch.ID), launch.MerchantID+"\x00"+token, ttl)
	if _, err := pipeline.Exec(ctx); err != nil {
		return fmt.Errorf("store launch session: %w", err)
	}
	return nil
}

func (s *RedisStore) ConsumeLaunch(ctx context.Context, token string) (Launch, error) {
	result, err := s.client.Eval(
		ctx,
		consumeLaunchScript,
		[]string{launchKey(token), launchTokenSessionKey(token)},
		launchSessionKeyPrefix,
	).Result()
	if errors.Is(err, redis.Nil) {
		return Launch{}, ErrNotFound
	}
	if err != nil {
		return Launch{}, fmt.Errorf("consume launch session: %w", err)
	}
	if result == nil {
		return Launch{}, ErrNotFound
	}
	payload, ok := result.(string)
	if !ok {
		return Launch{}, errors.New("consume launch session: invalid Redis response")
	}
	value := Launch{}
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return Launch{}, fmt.Errorf("decode launch session: %w", err)
	}
	return value, nil
}

func (s *RedisStore) CreateBrowserSession(ctx context.Context, value BrowserSession, ttl time.Duration) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode browser session: %w", err)
	}
	return s.client.Set(ctx, browserSessionKey(value.ID), payload, ttl).Err()
}

func (s *RedisStore) GetBrowserSession(ctx context.Context, sessionID string) (BrowserSession, error) {
	payload, err := s.client.Get(ctx, browserSessionKey(sessionID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return BrowserSession{}, ErrNotFound
	}
	if err != nil {
		return BrowserSession{}, fmt.Errorf("get browser session: %w", err)
	}
	value := BrowserSession{}
	if err := json.Unmarshal(payload, &value); err != nil {
		return BrowserSession{}, fmt.Errorf("decode browser session: %w", err)
	}
	return value, nil
}

func (s *RedisStore) RevokeBrowserSession(ctx context.Context, sessionID string) error {
	if err := s.client.Del(ctx, browserSessionKey(sessionID)).Err(); err != nil {
		return fmt.Errorf("delete browser session: %w", err)
	}
	return nil
}

func (s *RedisStore) RevokeLaunch(ctx context.Context, merchantID, sessionID string) error {
	result, err := s.client.Eval(
		ctx,
		revokeLaunchScript,
		[]string{launchSessionKey(sessionID)},
		merchantID,
		launchKeyPrefix,
		launchTokenSessionKeyPrefix,
	).Result()
	if err != nil {
		return fmt.Errorf("revoke launch session: %w", err)
	}
	deleted, ok := result.(int64)
	if !ok || deleted == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *RedisStore) ReserveNonce(ctx context.Context, merchantID, nonce string, ttl time.Duration) error {
	reserved, err := s.client.SetNX(ctx, nonceKey(merchantID, nonce), "1", ttl).Result()
	if err != nil {
		return fmt.Errorf("reserve request nonce: %w", err)
	}
	if !reserved {
		return ErrReplay
	}
	return nil
}

func launchKey(token string) string {
	return launchKeyPrefix + token
}

const launchKeyPrefix = "predictmarket:v3:launch:"

func launchTokenSessionKey(token string) string {
	return launchTokenSessionKeyPrefix + token
}

const launchTokenSessionKeyPrefix = "predictmarket:v3:launch-token:"

func launchSessionKey(sessionID string) string {
	return launchSessionKeyPrefix + sessionID
}

const launchSessionKeyPrefix = "predictmarket:v3:launch-session:"

func browserSessionKey(sessionID string) string {
	return "predictmarket:v3:session:" + sessionID
}

func nonceKey(merchantID, nonce string) string {
	return "predictmarket:v3:nonce:" + merchantID + ":" + nonce
}
