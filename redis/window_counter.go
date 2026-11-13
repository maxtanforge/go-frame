package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	goredis "github.com/redis/go-redis/v9"
)

// CounterOptions isolates counters under Namespace + ":" + key.
type CounterOptions struct {
	// Namespace is 1–128 ASCII letters, digits, dots, underscores or hyphens.
	Namespace string
}

// WindowCount is an exact count and remaining Redis expiration at execution.
type WindowCount struct {
	Count int64
	TTL   time.Duration
}

// WindowCounter borrows a single-primary client; its owner retains lifecycle
// responsibility. It makes no rate-limit or dependency-failure policy decisions.
type WindowCounter struct {
	client    *goredis.Client
	namespace string
}

// NewWindowCounter validates a borrowed client without dialing. Automatic retries
// must be disabled (MaxRetries: -1 for raw go-redis options, or use Open), and
// ContextTimeoutEnabled must be true. Do not mutate the client options afterward.
func NewWindowCounter(client *goredis.Client, options CounterOptions) (*WindowCounter, error) {
	if client == nil {
		return nil, errors.New("redis: window counter requires a client")
	}
	if client.Options().MaxRetries > 0 {
		return nil, errors.New("redis: window counter requires automatic retries disabled")
	}
	if !client.Options().ContextTimeoutEnabled {
		return nil, errors.New("redis: window counter requires ContextTimeoutEnabled")
	}
	if len(options.Namespace) == 0 || len(options.Namespace) > 128 {
		return nil, errors.New("redis: counter namespace must contain 1–128 bytes")
	}
	for _, c := range options.Namespace {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return nil, errors.New("redis: counter namespace must use ASCII letters, digits, dots, underscores or hyphens")
		}
	}
	return &WindowCounter{client: client, namespace: options.Namespace}, nil
}

// Increment starts a fixed window on the first hit, never renews an existing
// expiration, and repairs a valid legacy count with no expiration. key must be
// 1–512 UTF-8 bytes without whitespace or control characters. window must be a
// positive whole number of milliseconds. Existing counts must be canonical
// nonnegative decimal int64 values with room for one increment.
//
// Redis errors are returned unchanged. An error can represent an unknown write
// outcome: callers must not retry blindly. Lua execution is atomic but does not
// roll back prior commands on failure; permission or resource failures can leave
// a repaired expiration even when the increment fails. Socket cancellation may
// wait for the context deadline or configured I/O timeout.
func (c *WindowCounter) Increment(ctx context.Context, key string, window time.Duration) (WindowCount, error) {
	if ctx == nil {
		return WindowCount{}, errors.New("redis: window counter requires a context")
	}
	if err := ctx.Err(); err != nil {
		return WindowCount{}, err
	}
	if c == nil || c.client == nil {
		return WindowCount{}, errors.New("redis: window counter is not initialized")
	}
	if len(key) == 0 || len(key) > 512 || !utf8.ValidString(key) {
		return WindowCount{}, errors.New("redis: counter key must contain 1–512 valid UTF-8 bytes")
	}
	for _, r := range key {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return WindowCount{}, errors.New("redis: counter key must not contain whitespace or control characters")
		}
	}
	if window <= 0 || window%time.Millisecond != 0 {
		return WindowCount{}, errors.New("redis: counter window must be positive whole milliseconds")
	}
	// EVAL avoids an EVALSHA/NOSCRIPT fallback and executes exactly one command.
	result, err := c.client.Eval(ctx, windowCounterLua, []string{c.namespace + ":" + key}, window.Milliseconds()).Slice()
	if err != nil {
		return WindowCount{}, err
	}
	if len(result) != 2 {
		return WindowCount{}, errors.New("redis: invalid window counter reply")
	}
	countText, ok := result[0].(string)
	if !ok {
		return WindowCount{}, errors.New("redis: invalid window counter count reply")
	}
	count, err := strconv.ParseInt(countText, 10, 64)
	if err != nil {
		return WindowCount{}, fmt.Errorf("redis: invalid window counter count: %w", err)
	}
	ttl, ok := result[1].(int64)
	if !ok || ttl < 0 || ttl > int64((time.Duration(1<<63-1))/time.Millisecond) {
		return WindowCount{}, errors.New("redis: invalid window counter TTL reply")
	}
	return WindowCount{Count: count, TTL: time.Duration(ttl) * time.Millisecond}, nil
}

// Compare decimal strings rather than Lua doubles, which lose int64 precision.
// GET also preflights wrong-type errors before any mutation.
const windowCounterLua = `
local value = redis.call('GET', KEYS[1])
if not value then
  redis.call('SET', KEYS[1], '1', 'PX', ARGV[1])
else
  if value ~= '0' and not string.match(value, '^[1-9][0-9]*$') then
    return redis.error_reply('ERR window counter value is not a canonical nonnegative integer')
  end
  if #value > 19 or (#value == 19 and value >= '9223372036854775807') then
    return redis.error_reply('ERR window counter increment would overflow int64')
  end
  if redis.call('PTTL', KEYS[1]) == -1 then
    redis.call('PEXPIRE', KEYS[1], ARGV[1])
  end
  redis.call('INCR', KEYS[1])
end
return {redis.call('GET', KEYS[1]), redis.call('PTTL', KEYS[1])}
`
