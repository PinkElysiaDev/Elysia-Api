package server

// appendRetryEvent retains bounded failure evidence. Attempt zero is the first
// request; later attempts count retries, independently of model/key scheduling.
func (s *Server) appendRetryEvent(record *usageRecord, attempt int, model, errMsg string) {
	if record == nil {
		return
	}
	record.RetryCount = max(record.RetryCount, attempt)
	if len(record.RetryEvents) >= RetryEventsCacheMax {
		record.RetryEvents = append(record.RetryEvents[:0], record.RetryEvents[1:]...)
	}
	record.RetryEvents = append(record.RetryEvents, retryEvent{
		Attempt: attempt, Model: model, Error: truncateRetryError(errMsg),
	})
}

func truncateRetryError(msg string) string {
	if len(msg) > RetryErrorMaxLen {
		return msg[:RetryErrorMaxLen] + "...(truncated)"
	}
	return msg
}
