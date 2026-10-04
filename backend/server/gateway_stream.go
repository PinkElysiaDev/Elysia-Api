package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

const gatewayStreamErrorTrailer = "X-Elysia-Stream-Error"

func (s *Server) forwardGatewayStream(c *gin.Context, record *usageRecord, plan *gatewayPlan, candidate gatewayCandidate, response *http.Response) error {
	limits := plan.ingress.ResourceLimits()
	options := protocol.EvaluationContext{Scope: candidate.scope, State: protocol.NewEvaluationState(), Diagnostics: &protocol.DiagnosticSink{}}
	defer func() { record.appendConversionIssues(options.Diagnostics.Issues()) }()
	target := protocol.Target{Protocol: plan.ingress.Identity(), Direction: protocol.EncodeEvent, Scope: candidate.scope, Capabilities: plan.ingress.Capabilities(protocol.EncodeEvent)}
	replay, err := protocol.NewEventReplay(target, limits)
	if err != nil {
		return err
	}
	defer func() { updateRecordProtocolUsage(record, replay.Usage()) }()
	c.Header("Content-Type", protocol.TransportContentType(plan.operation.Transport))
	c.Header("Cache-Control", "no-cache")
	c.Header("Trailer", gatewayStreamErrorTrailer)
	emit := func(value protocol.Value) error {
		if record.FirstByteMs == 0 {
			record.FirstByteMs = time.Since(record.StartedAt).Milliseconds()
		}
		if err := protocol.WriteFrame(c.Writer, plan.operation, value); err != nil {
			return err
		}
		c.Writer.Flush()
		return nil
	}
	err = protocol.ReadFrames(c.Request.Context(), response.Body, candidate.operation, candidate.compiled.ResourceLimits().BufferBytes, func(frame protocol.Value, metadata protocol.Object) error {
		record.appendStreamEvent(string(frame.Bytes()))
		options.Values = metadata
		decoded, err := candidate.compiled.DecodeFrame(c.Request.Context(), frame, options)
		if err != nil {
			return err
		}
		if err := observeHostedTools(record, candidate.compiled, frame.Bytes()); err != nil {
			return err
		}
		acceptedEvents := []protocol.Event{}
		for _, event := range decoded.Events {
			updateRecordProtocolUsage(record, event.Usage)
			if event.Response != nil {
				updateRecordProtocolUsage(record, event.Response.Usage)
				if err := protocol.CheckGenerationOutcome(event.Response); err != nil {
					return err
				}
			}
			if err := protocol.IssuesError(protocol.CheckModelEvent(event, candidate.compiled, candidate.binding, candidate.scope)); err != nil {
				return err
			}
			accepted, err := replay.Consume(event)
			if err != nil {
				return err
			}
			if !accepted {
				continue
			}
			acceptedEvents = append(acceptedEvents, event)
		}
		if len(decoded.Events) > 0 && len(acceptedEvents) == 0 {
			return nil
		}
		decoded.Events = acceptedEvents
		frames, err := plan.ingress.EncodeFrame(c.Request.Context(), decoded, options)
		if err != nil {
			return err
		}
		for _, wire := range frames {
			if err := emit(wire); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		err = replay.Finish()
	}
	if err == nil {
		var frames []protocol.Value
		frames, err = plan.ingress.FinishEvents(c.Request.Context(), options)
		if err == nil {
			for _, frame := range frames {
				if writeErr := emit(frame); writeErr != nil {
					err = writeErr
					break
				}
			}
		}
	}
	if err != nil {
		// Never replay a generation after any downstream frame. A trailer also
		// exposes a late mapping failure when the target has no error event.
		if c.Writer.Written() {
			c.Header(gatewayStreamErrorTrailer, "protocol_stream_error")
			errorValue, encodeErr := protocol.EncodeValue(map[string]string{"category": "upstream", "message": err.Error()})
			if encodeErr == nil {
				failure := protocol.Event{SchemaVersion: protocol.SemanticSchemaVersion, Type: protocol.OperationFailed, Error: errorValue}
				if frames, encodeErr := plan.ingress.EncodeFrames(c.Request.Context(), failure, options); encodeErr == nil {
					for _, frame := range frames {
						if writeErr := emit(frame); writeErr != nil {
							return fmt.Errorf("%w; downstream error frame: %v", err, writeErr)
						}
					}
				}
			}
		}
		return err
	}
	if plan.operation.Framing != nil {
		for _, marker := range plan.operation.Framing.Done {
			if plan.operation.Transport == protocol.SSE {
				if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", marker); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintln(c.Writer, marker); err != nil {
				return err
			}
			// Done lists accepted alternatives; output uses the first canonical marker.
			break
		}
	}
	c.Writer.Flush()
	return nil
}
