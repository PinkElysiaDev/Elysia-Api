package server

import "log"

// usageQueueCapacity 是异步 usage 写入缓冲区大小。突发流量下若 writer
// 暂时跟不上，最多缓冲这么多条；超出则该条记录降级为同步写入（保证不丢）。
const usageQueueCapacity = 1024

// startUsageWriter 在 store 模式下启动单个后台 writer goroutine，
// 从 usageQueue 取记录落库。仅在有 store 时调用一次（ListenAndServe 内）。
func (s *Server) startUsageWriter() {
	if s.store == nil || s.usageQueue != nil {
		return
	}
	s.usageQueue = make(chan *usageRecord, usageQueueCapacity)
	s.usageWriterWG.Add(1)
	go func() {
		defer s.usageWriterWG.Done()
		for record := range s.usageQueue {
			if err := s.saveUsageRecordToStore(record); err != nil {
				log.Printf("failed to save usage record to sqlite (async): %v", err)
			}
		}
	}()
}

// stopUsageWriter 关闭队列并等待 writer 把剩余记录冲刷落库。用于优雅退出/测试。
func (s *Server) stopUsageWriter() {
	if s.usageQueue == nil {
		return
	}
	close(s.usageQueue)
	s.usageWriterWG.Wait()
	s.usageQueue = nil
}

// enqueueUsageRecord 尝试把记录投递到异步队列。队列已满或未启动时返回 false，
// 调用方据此降级为同步写入，确保 usage 不丢失。
func (s *Server) enqueueUsageRecord(record *usageRecord) bool {
	if s.usageQueue == nil {
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
	select {
	case s.usageQueue <- &copied:
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
