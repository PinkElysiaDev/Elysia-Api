package server

import (
	"log"
	"sync"
)

// usageQueueCapacity 是异步 usage 写入缓冲区大小。突发流量下若 writer
// 暂时跟不上，最多缓冲这么多条；超出则该条记录降级为同步写入（保证不丢）。
const usageQueueCapacity = 1024

// usageWriterState 守护队列的关闭语义：优雅关停时 Shutdown 只等 5 秒，
// 长流请求可能仍在运行并在之后调用 enqueueUsageRecord——直接 close(ch)
// 会让这些发送 panic。closed 标志 + 互斥锁保证关停后入队安全降级为同步
// 写盘（丢给 WAL，而不是丢给 runtime panic）。
type usageWriterState struct {
	mu     sync.Mutex
	queue  chan *usageRecord
	closed bool
	wg     sync.WaitGroup
}

// startUsageWriter 在 store 模式下启动单个后台 writer goroutine，
// 从队列取记录落库。仅在有 store 时调用一次（ListenAndServe 内）。
func (s *Server) startUsageWriter() {
	if s.store == nil {
		return
	}
	s.usageWriterMu.Lock()
	defer s.usageWriterMu.Unlock()
	if s.usageWriter != nil {
		return
	}
	writer := &usageWriterState{queue: make(chan *usageRecord, usageQueueCapacity)}
	s.usageWriter = writer
	writer.wg.Add(1)
	go func() {
		defer writer.wg.Done()
		for record := range writer.queue {
			s.persistUsageRecord(record)
		}
	}()
}

func (s *Server) usageWriterSnapshot() *usageWriterState {
	s.usageWriterMu.Lock()
	w := s.usageWriter
	s.usageWriterMu.Unlock()
	return w
}

// stopUsageWriter 关闭队列并等待 writer 把剩余记录冲刷落库。幂等；
// 关停后的入队请求由 enqueueUsageRecord 降级为同步写盘。
func (s *Server) stopUsageWriter() {
	w := s.usageWriterSnapshot()
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.queue)
	}
	w.mu.Unlock()
	w.wg.Wait()
	s.usageWriterMu.Lock()
	if s.usageWriter == w {
		s.usageWriter = nil
	}
	s.usageWriterMu.Unlock()
}

// drainUsageQueue 丢弃队列里尚未落库的记录。调用方须持有 usagePersistMu，
// 并已递增 usageWriteGen，使并发 writer 拿到的旧 generation 写入也会被跳过。
func (s *Server) drainUsageQueue() {
	w := s.usageWriterSnapshot()
	if w == nil {
		return
	}
	for {
		select {
		case _, ok := <-w.queue:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

// persistUsageRecord 在 persist 锁下核对 generation 后落库。reset 持同一把锁，
// 避免清库后旧写入写回。
func (s *Server) persistUsageRecord(record *usageRecord) {
	if record == nil || s.store == nil {
		return
	}
	s.usagePersistMu.Lock()
	if record.writeGen != s.usageWriteGen.Load() {
		s.usagePersistMu.Unlock()
		return
	}
	err := s.saveUsageRecordToStore(record)
	s.usagePersistMu.Unlock()
	if err != nil {
		log.Printf("failed to save usage record to sqlite: %v", err)
		return
	}
	// 只读缓存按 TTL 活着，不随写入失效的话，KPI/日志会再吃一整轮旧响应。
	s.usageCache.flush()
	s.usageSeq.Add(1)
}

// enqueueUsageRecord 尝试把记录投递到异步队列。队列已满或已关停时返回
// false，调用方据此降级为同步写入，确保 usage 不丢失。
func (s *Server) enqueueUsageRecord(record *usageRecord) bool {
	w := s.usageWriterSnapshot()
	if w == nil {
		return false
	}
	// 深拷贝：浅拷贝会让 copied 的切片字段与原记录共享底层数组，一旦调用方
	// 在入队后继续 append（RetryEvents 等）即与 writer goroutine 读取竞争。
	// downstream 捕获器的内容已在 recordUsage 物化进 DownstreamResponse，置 nil 断开指针别名。
	copied := *record
	copied.RetryEvents = cloneSlice(record.RetryEvents)
	copied.ConversionChain = cloneSlice(record.ConversionChain)
	copied.RequestWarnings = cloneSlice(record.RequestWarnings)
	copied.downstream = nil
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	select {
	case w.queue <- &copied:
		return true
	default:
		return false // 队列满：降级同步写
	}
}

// cloneSlice 返回切片的浅副本（元素为值类型，足以断开底层数组别名）。nil 仍返回 nil。
func cloneSlice[T any](src []T) []T {
	if src == nil {
		return nil
	}
	dst := make([]T, len(src))
	copy(dst, src)
	return dst
}
