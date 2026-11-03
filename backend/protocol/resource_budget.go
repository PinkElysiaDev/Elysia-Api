package protocol

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

const (
	// DefaultRequestBodyBytes is the request transport default, independent of
	// response frames, definition files and internal semantic representations.
	DefaultRequestBodyBytes      = 32 << 20
	semanticByteMultiplier       = 8
	semanticNodeMultiplier       = 16
	semanticDepthMultiplier      = 4
	plannedRequestEncodeMinBytes = 64 << 10
)

type requestBudgetKey struct{}

// WithRequestBodyLimit pins a validated request limit for the complete call.
// The value is inaccessible to authored mappings and survives nested replays.
func WithRequestBodyLimit(ctx context.Context, bytes int64) (context.Context, error) {
	if _, pinned := ctx.Value(requestBudgetKey{}).(int); pinned {
		return ctx, nil
	}
	if err := ValidateRequestBodyLimit(bytes); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, requestBudgetKey{}, int(bytes)), nil
}

// AnnotateResourceError locates resource failures outside compiled mappings.
func AnnotateResourceError(err error, identity Identity, direction Direction, stage string) error {
	return resourceIssue(err, identity, direction, stage)
}

// CheckRequestBody checks the final wire after authored mappings and carriers.
func (compiled *Compiled) CheckRequestBody(ctx context.Context, value Value) error {
	return resourceIssue(checkResourceValue(value, compiled.RequestLimits(ctx), "request_wire"), compiled.identity, EncodeRequest, "encode.request")
}

// RequestBodyLimit returns the pinned transport limit or the library default.
func RequestBodyLimit(ctx context.Context) int {
	if limit, ok := ctx.Value(requestBudgetKey{}).(int); ok {
		return limit
	}
	return DefaultRequestBodyBytes
}

// ResourceLimitError reports counts only; it never embeds user payloads.
type ResourceLimitError struct {
	Domain   string
	Metric   string
	Limit    int
	Observed int
	Path     string
}

func (failure *ResourceLimitError) Error() string {
	return fmt.Sprintf("%s resource limit exceeded: %s observed %d, limit %d", failure.Domain, failure.Metric, failure.Observed, failure.Limit)
}

func resourceFailure(domain, metric string, limit, observed int) error {
	return &ResourceLimitError{Domain: domain, Metric: metric, Limit: limit, Observed: observed, Path: "/"}
}

func semanticLimits(wire Limits) Limits {
	// Compiler limits are validated against finite engine maxima. Request bytes
	// are checked before entering the context, so these products cannot overflow.
	wire.BufferBytes *= semanticByteMultiplier
	wire.Nodes *= semanticNodeMultiplier
	wire.Depth *= semanticDepthMultiplier
	return wire
}

func requestLimits(ctx context.Context, base Limits, explicit bool) Limits {
	bytes := RequestBodyLimit(ctx)
	if explicit {
		bytes = min(bytes, base.BufferBytes)
	}
	base.BufferBytes = bytes
	return base
}

func isRequestDirection(direction Direction) bool {
	return direction == DecodeRequest || direction == EncodeRequest
}
func isDecodeDirection(direction Direction) bool {
	return direction == DecodeRequest || direction == DecodeResponse || direction == DecodeEvent || direction == DecodeClientEvent
}

func (compiled *Compiled) wireLimits(ctx context.Context, direction Direction) Limits {
	if isRequestDirection(direction) {
		return requestLimits(ctx, compiled.limits, compiled.explicitLimits)
	}
	return compiled.limits
}

// RequestLimits describes the actual request boundary, including an authored cap.
func (compiled *Compiled) RequestLimits(ctx context.Context) Limits {
	return compiled.wireLimits(ctx, DecodeRequest)
}

func (compiled *Compiled) mappingLimits(ctx context.Context, direction Direction) (Limits, Limits, Limits) {
	wire := compiled.wireLimits(ctx, direction)
	work := semanticLimits(wire)
	if direction == EncodeRequest {
		// A smaller target must be allowed to shrink a valid source document.
		work = semanticLimits(requestLimits(ctx, DefaultLimits(), false))
	}
	if isDecodeDirection(direction) {
		return wire, work, work
	}
	return work, wire, work
}

func (c *CompiledConversion) phaseLimits(ctx context.Context, phase ConversionPhase) Limits {
	wire := c.limits
	if phase == ConversionIngress || phase == ConversionRequest {
		wire = requestLimits(ctx, wire, c.Policy.Limits != nil)
	}
	if phase == ConversionWire || phase == ConversionSourceEvent {
		return wire
	}
	return semanticLimits(wire)
}

func checkResourceValue(value Value, limits Limits, domain string) error {
	err := checkValueLimits(value, limits)
	var failure *ResourceLimitError
	if errors.As(err, &failure) {
		copy := *failure
		copy.Domain = domain
		return &copy
	}
	return err
}

// checkSemanticValue bounds the serialization before making a JSON copy.
func checkSemanticValue(value any, limits Limits) error {
	_, err := measureSemanticValue(value, limits)
	return err
}

type semanticSize struct {
	semanticMetrics
	cache       *semanticCache
	maxRawBytes int
	hasMetrics  bool
}

// Only a complete successful generated walk can prove another budget fits.
// Failed or unsupported walks retain their original mapping error path.
func (size semanticSize) fits(limits Limits) bool {
	return size.hasMetrics && size.bytes <= limits.BufferBytes && size.nodes <= limits.Nodes && size.depth <= min(limits.Depth, typedMeterTraversalDepth)
}

func measureSemanticValue(value any, limits Limits) (semanticSize, error) {
	return measureSemanticValueWithCache(value, limits, nil)
}

func measureSemanticValueWithCache(value any, limits Limits, cacheLimits *Limits) (semanticSize, error) {
	fast := typedMeter{limits: limits}
	if cacheLimits != nil {
		fast.cacheLimits, fast.shouldCache = *cacheLimits, true
	}
	if ok, supported := fast.visitTyped(value); supported && ok && fast.fits() {
		return semanticSize{semanticMetrics: semanticMetrics{bytes: fast.bytes, nodes: fast.nodes, depth: fast.maxDepth}, cache: fast.cache, maxRawBytes: fast.maxRawBytes, hasMetrics: true}, nil
	}
	// The reflective failure path preserves the previous error precedence and
	// observed counts, including map traversal and unsupported/cyclic values.
	if plan := typedLimitPlans[reflect.TypeOf(value)]; plan != nil {
		meter := typedMeter{limits: limits}
		if meter.visit(plan, reflect.ValueOf(value)) && meter.fits() {
			return semanticSize{semanticMetrics: semanticMetrics{bytes: meter.bytes}, maxRawBytes: meter.maxRawBytes}, nil
		}
		if meter.bytes > limits.BufferBytes {
			return semanticSize{}, resourceFailure("semantic", "bytes", limits.BufferBytes, meter.bytes)
		}
		if meter.nodes > limits.Nodes {
			return semanticSize{}, resourceFailure("semantic", "nodes", limits.Nodes, meter.nodes)
		}
		if meter.depth > min(limits.Depth, typedMeterTraversalDepth) {
			return semanticSize{}, resourceFailure("semantic", "depth", min(limits.Depth, typedMeterTraversalDepth), meter.depth)
		}
	}
	// Invalid/unsupported Go values still use the encoder's precise error.
	encoded, err := EncodeValue(value)
	if err != nil {
		return semanticSize{}, err
	}
	return semanticSize{semanticMetrics: semanticMetrics{bytes: encoded.Len()}, maxRawBytes: encoded.Len()}, checkResourceValue(encoded, limits, "semantic")
}

// EncodeSemantic checks trusted semantic values before allocating their JSON form.
func EncodeSemantic(ctx context.Context, direction Direction, value any) (Value, error) {
	limits := DefaultLimits()
	if isRequestDirection(direction) {
		limits = requestLimits(ctx, limits, false)
	}
	return encodeSemanticWithin(value, semanticLimits(limits))
}

func resourceIssue(err error, identity Identity, direction Direction, stage string) error {
	var failure *ResourceLimitError
	if !errors.As(err, &failure) {
		return err
	}
	var existing *ConversionError
	if errors.As(err, &existing) {
		for _, issue := range existing.Issues {
			if issue.Code == LimitExceeded {
				return err
			}
		}
	}
	return &ConversionError{cause: err, Issues: []ConversionIssue{{Code: LimitExceeded, Severity: SeverityError, Protocol: identity, Direction: direction, Stage: stage, Path: failure.Path, Reason: failure.Error(), Suggestion: "Review the effective resource budget and mapping expansion."}}}
}

func (c *CompiledConversion) ruleLimits(ctx context.Context, phase ConversionPhase, route ConversionContext) Limits {
	if phase == ConversionWire && !route.ResponseWire {
		return requestLimits(ctx, c.limits, c.Policy.Limits != nil)
	}
	return c.phaseLimits(ctx, phase)
}

// PreviewEnvelopeLimit bounds the semantic input plus definition envelope.
func PreviewEnvelopeLimit(ctx context.Context) int {
	return RequestBodyLimit(ctx)*semanticByteMultiplier + DefaultLimits().BufferBytes
}

// nativeLimits validates source snapshots independently from a smaller target.
// The mapped/reconciled wire is checked against the target after edits.
func (compiled *Compiled) nativeLimits(ctx context.Context, direction Direction) Limits {
	if direction == EncodeRequest {
		return requestLimits(ctx, DefaultLimits(), false)
	}
	return compiled.wireLimits(ctx, direction)
}

func encodeSemanticWithin(value any, limits Limits) (Value, error) {
	size, err := measureSemanticValue(value, limits)
	if err != nil {
		return Value{}, err
	}
	if size.bytes < plannedRequestEncodeMinBytes {
		return EncodeValue(value)
	}
	// Build only after validation has bounded traversal and preserved the
	// encoder's errors. Large request snapshots need no intermediate JSON copies.
	var output JSONOutput
	switch request := value.(type) {
	case *Request:
		output, err = readCoreRequestOutput(request, nil, nil)
	case Request:
		output, err = readCoreRequestOutput(&request, nil, nil)
	default:
		return EncodeValue(value)
	}
	if err != nil {
		return Value{}, err
	}
	return output.Encode(context.Background())
}

// ValidateRequestBodyLimit rejects configurations whose derived budgets overflow.
func ValidateRequestBodyLimit(bytes int64) error {
	if bytes <= 0 || bytes > int64((int(^uint(0)>>1)-DefaultLimits().BufferBytes)/semanticByteMultiplier) {
		return fmt.Errorf("request body limit must be positive and allow bounded semantic expansion")
	}
	return nil
}
