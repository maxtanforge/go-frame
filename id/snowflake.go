// Package id 分布式 ID 生成。
// 雪花算法 64 位布局:1 符号位(恒 0)| 41 时间戳(ms)| 10 节点 | 12 序列。
package id

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrClockBackwards means wall time precedes the last issued ID.
	ErrClockBackwards = errors.New("id: clock moved backwards")
	// ErrTimeRange means wall time is outside the 41-bit timestamp window.
	ErrTimeRange = errors.New("id: timestamp outside epoch range")
)

// epoch 时间戳起点(2026-01-01T00:00:00Z 的毫秒值),41 位可用约 69 年。
const epoch = int64(1767225600000)

const (
	nodeBits  = 10
	seqBits   = 12
	maxNode   = int64(-1) ^ (int64(-1) << nodeBits) // 1023
	maxSeq    = int64(-1) ^ (int64(-1) << seqBits)  // 4095
	timeShift = nodeBits + seqBits                  // 22
	nodeShift = seqBits                             // 12
)

// Snowflake 雪花 ID 生成器,并发安全。
type Snowflake struct {
	mu   sync.Mutex
	node int64
	last int64 // 上次分配的毫秒时间戳(epoch 相对)
	seq  int64
	now  func() time.Time // 可注入时钟,测试用
}

// NewSnowflake node 范围 [0, 1023]。
func NewSnowflake(node int64) (*Snowflake, error) {
	if node < 0 || node > maxNode {
		return nil, fmt.Errorf("id: node must be in [0, %d], got %d", maxNode, node)
	}
	return &Snowflake{node: node, now: time.Now, last: -1}, nil
}

// Next generates an ID, yielding when the millisecond sequence is exhausted.
// Use NextContext when generation must obey a request deadline.
func (s *Snowflake) Next() (int64, error) {
	return s.NextContext(context.Background())
}

// NextContext waits without holding the generator mutex, supports cancellation,
// and rechecks rollback/range on every attempt. A failed call never wraps seq.
func (s *Snowflake) NextContext(ctx context.Context) (int64, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, exhausted, err := s.next()
		if !exhausted {
			return n, err
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Snowflake) next() (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := s.now().UnixMilli() - epoch
	if ts < 0 || ts >= 1<<41 {
		return 0, false, ErrTimeRange
	}
	if ts < s.last {
		return 0, false, fmt.Errorf("%w: last %dms, now %dms", ErrClockBackwards, s.last, ts)
	}
	if ts == s.last {
		if s.seq == maxSeq {
			return 0, true, nil
		}
		s.seq++
	} else {
		s.seq = 0
	}
	s.last = ts
	return ts<<timeShift | s.node<<nodeShift | s.seq, false, nil
}
