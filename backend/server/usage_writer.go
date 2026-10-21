package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/storage"
)

// usageQueueCapacity 是异步 usage 写入缓冲区大小。突发流量下若 writer
// 暂时跟不上，最多缓冲这么多条；超出则同步写入，失败显式报告。
const usageQueueCapacity = 1024

// Payloads count while queued and while the writer persists them. This bounds
// asynchronous retention, not active request memory or generation concurrency.
const usageQueuePayloadBytes int64 = 128 << 20

// Admission and close share a lock. Shutdown first stops producers, then drains
// the queue with a separate persistence budget before closing the database.
type usageWriterState struct {
	mu            sync.Mutex
	queue         chan *usageRecord
	closed        bool
	wg            sync.WaitGroup
	retainedBytes int64
}

// startUsageWriter 在 store 模式下启动单个后台 writer goroutine，
// 从队列取记录落库。生产环境在 Hertz OnRun 启动阶段调用。
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
	s.usageStatsMu.Lock()
	s.usageStats.Closed = false
	s.usageStatsMu.Unlock()
	writer.wg.Add(1)
	go writer.run(s.persistUsageBatch)
}

// The queue owns batching and payload lifetime; persistence is an application
// callback shared with synchronous writes and job settlement.
func (writer *usageWriterState) run(persist func([]*usageRecord)) {
	defer writer.wg.Done()
	var batchStorage [64]*usageRecord
	timer := time.NewTimer(0)
	timer.Stop()
	defer timer.Stop()
	for record := range writer.queue {
		batch := append(batchStorage[:0], record)
		timer.Reset(50 * time.Millisecond)
	collect:
		for len(batch) < len(batchStorage) {
			select {
			case next, ok := <-writer.queue:
				if !ok {
					break collect
				}
				batch = append(batch, next)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		persist(batch)
		writer.mu.Lock()
		for _, item := range batch {
			writer.retainedBytes -= item.queuedPayloadBytes
		}
		writer.mu.Unlock()
		// Release references before waiting, including after persistence failures.
		clear(batch)
	}
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
	s.usageStatsMu.Lock()
	s.usageStats.Closed = true
	s.usageStatsMu.Unlock()
	s.usageWriterMu.Lock()
	if s.usageWriter == w {
		s.usageWriter = nil
	}
	s.usageWriterMu.Unlock()
}

// drainUsageQueueFrom 丢弃队列里尚未落库的记录。
// reset 调用方应已持有 w.mu（若 w 非 nil）和 usagePersistMu，这样 enqueue
// 无法在 drain 与 generation 递增之间把新记录塞进队列。
func (s *Server) drainUsageQueueFrom(w *usageWriterState) {
	if w == nil {
		return
	}
	for {
		select {
		case record, ok := <-w.queue:
			if !ok {
				return
			}
			w.retainedBytes -= record.queuedPayloadBytes
		default:
			return
		}
	}
}

// persistUsageRecord 在 persist 锁下核对 generation 后落库。reset 持同一把锁，
// 避免清库后旧写入写回。
func (s *Server) persistUsageRecord(record *usageRecord) {
	s.persistUsageBatch([]*usageRecord{record})
}

func (s *Server) persistUsageBatch(records []*usageRecord) {
	if s.store == nil {
		return
	}
	s.usagePersistMu.Lock()
	defer s.usagePersistMu.Unlock()
	// The shared commit path reports failures even when no caller can retry.
	_ = s.saveUsageBatchToStore(records)
}

// saveUsageBatchToStore shares preparation, retries and accounting between the
// queue and synchronous job settlements. The caller holds usagePersistMu so
// generation checks, asset writes and commits stay atomic with reset and GC.
func (s *Server) saveUsageBatchToStore(records []*usageRecord) (result error) {
	s.initLifecycle()
	started := time.Now()
	ctx, cancel := context.WithTimeout(s.usageWriteCtx, 5*time.Second)
	defer cancel()
	batch := make([]storage.UsageRecordInput, 0, len(records))
	var canceled int
	for _, record := range records {
		if record == nil || record.writeGen != s.usageWriteGen.Load() {
			continue
		}
		if ctx.Err() != nil {
			canceled++
			continue
		}
		input, err := s.prepareUsageRecord(record)
		if err != nil {
			s.recordUsageFailure(err, 1)
			result = errors.Join(result, err)
			continue
		}
		batch = append(batch, input)
	}
	if canceled > 0 {
		s.recordUsageFailure(ctx.Err(), canceled)
		result = errors.Join(result, ctx.Err())
	}
	if len(batch) == 0 {
		return result
	}
	// Request IDs make retry idempotent, including rollup and asset references.
	var err error
	for remaining := 3; remaining > 0; remaining-- {
		err = s.store.LogStore.SaveUsageBatch(ctx, batch)
		if err == nil || ctx.Err() != nil || remaining == 1 {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	s.usageStatsMu.Lock()
	s.usageStats.LastBatchDurationMS = time.Since(started).Milliseconds()
	if err == nil {
		s.usageStats.Written += uint64(len(batch))
		s.usageStats.LastWriteAt = time.Now().UTC()
	}
	s.usageStatsMu.Unlock()
	if err != nil {
		s.recordUsageFailure(err, len(batch))
		return errors.Join(result, err)
	}
	// 只读缓存按 TTL 活着，不随写入失效的话，KPI/日志会再吃一整轮旧响应。
	s.usageCache.flush()
	s.usageSeq.Add(uint64(len(batch)))
	return result
}

func (s *Server) recordUsageFailure(err error, count int) {
	s.usageStatsMu.Lock()
	s.usageStats.Failures += uint64(count)
	s.usageStats.LastError = err.Error()
	s.usageWriteErr = err
	s.usageStatsMu.Unlock()
	logging.Emergency("usage records could not be committed", err)
}

func (s *Server) usagePersistenceError() error {
	s.usageStatsMu.Lock()
	defer s.usageStatsMu.Unlock()
	return s.usageWriteErr
}

func (s *Server) usageWriterStats() logging.Stats {
	s.usageStatsMu.Lock()
	st := s.usageStats
	s.usageStatsMu.Unlock()
	if w := s.usageWriterSnapshot(); w != nil {
		w.mu.Lock()
		st.Queued, st.RetainedBytes, st.Closed = len(w.queue), w.retainedBytes, w.closed
		w.mu.Unlock()
	}
	return st
}

// enqueueUsageRecord 尝试把记录投递到异步队列。队列已满或已关停时返回
// false，调用方据此降级为同步写入；落盘错误计数并影响退出状态。
func (s *Server) enqueueUsageRecord(record *usageRecord) bool {
	w := s.usageWriterSnapshot()
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	payloadBytes := usageRetainedPayloadBytes(record)
	if w.closed || len(w.queue) == cap(w.queue) || payloadBytes > usageQueuePayloadBytes-w.retainedBytes {
		s.usageStatsMu.Lock()
		s.usageStats.Synchronous++
		s.usageStatsMu.Unlock()
		return false
	}
	// 深拷贝：浅拷贝会让 copied 的切片字段与原记录共享底层数组，一旦调用方
	// 在入队后继续 append（RetryEvents 等）即与 writer goroutine 读取竞争。
	// downstream 捕获器的内容已在 recordUsage 物化进 DownstreamResponse，置 nil 断开指针别名。
	copied := *record
	copied.queuedPayloadBytes = payloadBytes
	copied.RetryEvents = cloneSlice(record.RetryEvents)
	copied.ConversionChain = cloneSlice(record.ConversionChain)
	copied.RequestWarnings = cloneSlice(record.RequestWarnings)
	copied.pendingStreamEvents = cloneSlice(record.pendingStreamEvents)
	// Media is immutable; only its metadata/index containers need a snapshot.
	copied.assets = record.assets.clone()
	copied.downstream = nil
	w.retainedBytes += payloadBytes
	select {
	case w.queue <- &copied:
		return true
	default:
		w.retainedBytes -= payloadBytes
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

// Account payloads without materializing JSON. Counts saturate just above the
// queue budget so exceptionally large records cannot overflow the reservation.
func usageRetainedPayloadBytes(record *usageRecord) int64 {
	var total int64
	add := func(size int) {
		if total <= usageQueuePayloadBytes {
			total += min(int64(size), usageQueuePayloadBytes+1-total)
		}
	}
	for _, body := range []usageBody{record.IncomingBody, record.OutgoingBody, record.ProviderResponse, record.DownstreamResponse} {
		add(len(body.Content))
	}
	for _, event := range record.pendingStreamEvents {
		add(len(event))
	}
	seen := make(map[*mediaPayload]struct{}, len(record.assets.items))
	for _, item := range record.assets.items {
		if _, exists := seen[item.payload]; exists {
			continue
		}
		seen[item.payload] = struct{}{}
		add(item.payload.size())
	}
	return total
}
