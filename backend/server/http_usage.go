package server

import "github.com/cloudwego/hertz/pkg/app"

// HTTP keeps only its prepared log payload until Hertz has attempted final
// delivery. Quota adjustment, task ownership and route cleanup stay in their
// existing handler scopes; no second SQL update is needed after completion.
func (s *Server) recordHTTPUsage(c *app.RequestContext, record *usageRecord) {
	shouldPersist := s.prepareUsageWrite(record)
	releaseHTTPUsageCapture(c, record)
	if !shouldPersist {
		return
	}
	state := ensureHTTPDeliveryState(c)
	onHTTPComplete(c, func() {
		applyHTTPDelivery(record, state)
		s.writePreparedUsage(record)
	})
}

func releaseHTTPUsageCapture(c *app.RequestContext, record *usageRecord) {
	if record == nil {
		return
	}
	if capture := record.downstream; capture != nil {
		capture.buf, capture.response = nil, nil
		if stream := currentResponseStream(c); stream != nil && stream.capture == capture {
			stream.capture = nil
		}
		record.downstream = nil
	}
	// The immutable media bytes remain available to persistence. Capture-only
	// indexes and raw frames must not outlive the handler on a slow final flush.
	record.assets = record.assets.snapshotForPersistence()
}

func applyHTTPDelivery(record *usageRecord, state *httpDeliveryState) {
	record.Delivery = state.result
	if record.Error != "" || record.StatusCode >= 400 {
		return
	}
	if state.applicationError != nil {
		record.StatusCode, record.ErrorKind = deliveryFailureStatus("server_panic")
		record.Error = state.applicationError.Error()
		if state.bufferedErrorBody != "" && record.bodyOpts.maxBytes > 0 {
			record.DownstreamResponse = truncateUsageBody(state.bufferedErrorBody, record.bodyOpts.maxBytes)
		}
		return
	}
	if state.result.Outcome != deliveryFailed {
		return
	}
	record.StatusCode, record.ErrorKind = deliveryFailureStatus(state.result.Cause)
	record.Error = state.result.Error
}
