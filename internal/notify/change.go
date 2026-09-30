package notify

import (
	"context"
	"sync"
)

type Change struct {
	mu         sync.Mutex
	generation uint64
	changed    chan struct{}
}

func New() *Change {
	return &Change{
		generation: 1,
		changed:    make(chan struct{}),
	}
}

func (c *Change) Current() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

func (c *Change) Notify() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	close(c.changed)
	c.changed = make(chan struct{})
	return c.generation
}

func (c *Change) Wait(ctx context.Context, after uint64) (uint64, bool) {
	c.mu.Lock()
	if c.generation != after {
		generation := c.generation
		c.mu.Unlock()
		return generation, true
	}
	changed := c.changed
	c.mu.Unlock()

	select {
	case <-changed:
		return c.Current(), true
	case <-ctx.Done():
		return c.Current(), false
	}
}
