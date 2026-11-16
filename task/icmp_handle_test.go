package task

import (
	"errors"
	"sync"
	"testing"
)

type trackedICMPHandles struct {
	mu      sync.Mutex
	next    uintptr
	open    map[uintptr]bool
	doubled bool
}

func (tracker *trackedICMPHandles) create() (uintptr, error) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.next++
	tracker.open[tracker.next] = true
	return tracker.next, nil
}

func (tracker *trackedICMPHandles) destroy(handle uintptr) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if !tracker.open[handle] {
		tracker.doubled = true
	}
	delete(tracker.open, handle)
}

func (tracker *trackedICMPHandles) assertClosed(t *testing.T) {
	t.Helper()
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if len(tracker.open) != 0 || tracker.doubled {
		t.Fatalf("未释放=%d，重复释放=%v", len(tracker.open), tracker.doubled)
	}
}

func newTrackedICMPPool() (*icmpHandlePool, *trackedICMPHandles) {
	tracker := &trackedICMPHandles{open: make(map[uintptr]bool)}
	return &icmpHandlePool{create: tracker.create, destroy: tracker.destroy}, tracker
}

func TestICMPHandlePoolReuseAndBound(t *testing.T) {
	pool, tracker := newTrackedICMPPool()
	handle := pool.take()
	pool.release(handle)
	if reused := pool.take(); reused != handle {
		t.Fatalf("句柄未复用: %d != %d", reused, handle)
	}
	handles := []uintptr{handle}
	for index := 0; index < maxICMPHandles+10; index++ {
		handles = append(handles, pool.take())
	}
	for _, handle := range handles {
		pool.release(handle)
	}
	if len(pool.idle) != maxICMPHandles || len(tracker.open) != maxICMPHandles {
		t.Fatalf("闲置句柄池没有遵守上限: idle=%d open=%d", len(pool.idle), len(tracker.open))
	}
	pool.close()
	pool.close()
	if pool.take() != 0 {
		t.Fatal("关闭后仍能取得句柄")
	}
	tracker.assertClosed(t)
}

func TestICMPHandlePoolClosesInFlight(t *testing.T) {
	pool, tracker := newTrackedICMPPool()
	handle := pool.take()
	pool.close()
	pool.release(handle)
	tracker.assertClosed(t)
}

func TestICMPHandlePoolCloseDuringCreation(t *testing.T) {
	pool, tracker := newTrackedICMPPool()
	started, finish := make(chan struct{}), make(chan struct{})
	pool.create = func() (uintptr, error) {
		close(started)
		<-finish
		return tracker.create()
	}
	result := make(chan uintptr, 1)
	go func() { result <- pool.take() }()
	<-started
	pool.close()
	close(finish)
	if handle := <-result; handle != 0 {
		t.Fatalf("关闭期间新建的句柄没有被回收: %d", handle)
	}
	tracker.assertClosed(t)
}

func TestICMPHandlePoolConcurrentCloseAndRelease(t *testing.T) {
	for iteration := 0; iteration < 30; iteration++ {
		pool, tracker := newTrackedICMPPool()
		start := make(chan struct{})
		var workers sync.WaitGroup
		for index := 0; index < 20; index++ {
			handle := pool.take()
			workers.Add(1)
			go func(handle uintptr) {
				defer workers.Done()
				<-start
				pool.release(handle)
			}(handle)
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			pool.close()
		}()
		close(start)
		workers.Wait()
		pool.close()
		tracker.assertClosed(t)
	}
}

func TestICMPHandlePoolCreationFailure(t *testing.T) {
	for _, invalid := range []uintptr{0, ^uintptr(0)} {
		pool := &icmpHandlePool{create: func() (uintptr, error) { return invalid, nil }}
		if pool.take() != 0 {
			t.Fatal("无效句柄被当成成功创建")
		}
		pool.release(invalid)
	}
	pool := &icmpHandlePool{create: func() (uintptr, error) { return 0, errors.New("创建失败") }}
	if pool.take() != 0 {
		t.Fatal("创建失败没有传播")
	}
}
