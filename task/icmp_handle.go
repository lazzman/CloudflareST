package task

import "sync"

const maxICMPHandles = 64

type icmpHandlePool struct {
	mu      sync.Mutex
	closed  bool
	idle    []uintptr
	create  func() (uintptr, error)
	destroy func(uintptr)
}

func (pool *icmpHandlePool) take() uintptr {
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		return 0
	}
	if count := len(pool.idle); count > 0 {
		handle := pool.idle[count-1]
		pool.idle = pool.idle[:count-1]
		pool.mu.Unlock()
		return handle
	}
	pool.mu.Unlock()
	handle, err := pool.create()
	if err != nil || handle == 0 || handle == ^uintptr(0) {
		return 0
	}
	pool.mu.Lock()
	closed := pool.closed
	pool.mu.Unlock()
	if closed {
		pool.destroy(handle)
		return 0
	}
	return handle
}

func (pool *icmpHandlePool) release(handle uintptr) {
	if handle == 0 || handle == ^uintptr(0) {
		return
	}
	pool.mu.Lock()
	if !pool.closed && len(pool.idle) < maxICMPHandles {
		pool.idle = append(pool.idle, handle)
		pool.mu.Unlock()
		return
	}
	pool.mu.Unlock()
	pool.destroy(handle)
}

func (pool *icmpHandlePool) close() {
	pool.mu.Lock()
	pool.closed = true
	idle := pool.idle
	pool.idle = nil
	pool.mu.Unlock()
	for _, handle := range idle {
		pool.destroy(handle)
	}
}
