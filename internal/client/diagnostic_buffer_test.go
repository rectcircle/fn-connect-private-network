package client

import (
	"bytes"
	"sync"
)

type diagnosticBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *diagnosticBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *diagnosticBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
