package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/elysia-api/backend/storage"
)

const (
	loggingReviewResponseMaxBytes = 6 << 20
	loggingReviewImageWidth       = 512
	loggingReviewImageReserve     = 64 << 10
	loggingReviewSeedFirst        = 0x4c6f674d656d6f72
	loggingReviewSeedSecond       = 0x7952657669657731
	loggingReviewWait             = 60 * time.Second
)

// The PNG is valid and deterministic, with independent noisy RGBA pixels and
// an ancillary padding chunk. Its base64 length exactly matches the old fixture;
// repeated "A" bytes remain available as a separate compressible control.
func loggingReviewImagePayload(sizeMiB int) string {
	if os.Getenv("ELYSIA_LOGGING_IMAGE") != "png" {
		return strings.Repeat("A", sizeMiB<<20)
	}
	target := (sizeMiB << 20) * 3 / 4
	height := (target - loggingReviewImageReserve) / (loggingReviewImageWidth*4 + 1)
	pixels := image.NewNRGBA(image.Rect(0, 0, loggingReviewImageWidth, height))
	random := rand.NewPCG(loggingReviewSeedFirst, loggingReviewSeedSecond)
	fill := func(output []byte) {
		for len(output) >= 8 {
			binary.LittleEndian.PutUint64(output, random.Uint64())
			output = output[8:]
		}
		for index := range output {
			output[index] = byte(random.Uint64())
		}
	}
	fill(pixels.Pix)
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&encoded, pixels); err != nil {
		panic(fmt.Sprintf("synthetic PNG encoding: %v", err))
	}
	paddingLength := target - encoded.Len() - 12
	if paddingLength < 0 {
		panic("synthetic PNG exceeded its reserved container overhead")
	}
	output := make([]byte, target)
	prefix := encoded.Len() - 12 // Insert an ancillary chunk before IEND.
	copy(output, encoded.Bytes()[:prefix])
	chunk := output[prefix : prefix+paddingLength+12]
	binary.BigEndian.PutUint32(chunk, uint32(paddingLength))
	copy(chunk[4:], "raNd")
	fill(chunk[8 : 8+paddingLength])
	binary.BigEndian.PutUint32(chunk[8+paddingLength:], crc32.ChecksumIEEE(chunk[4:8+paddingLength]))
	copy(output[len(output)-12:], encoded.Bytes()[prefix:])
	return base64.StdEncoding.EncodeToString(output)
}

func loggingReviewResponseText() (string, error) {
	raw := os.Getenv("ELYSIA_LOGGING_RESPONSE_BYTES")
	if raw == "" {
		return "ok", nil
	}
	size, err := strconv.Atoi(raw)
	if err != nil || size < 2 || size > loggingReviewResponseMaxBytes {
		return "", fmt.Errorf("invalid ELYSIA_LOGGING_RESPONSE_BYTES: %q", raw)
	}
	return "ok" + strings.Repeat("x", size-2), nil
}

func TestLoggingReviewPNGFixture(t *testing.T) {
	t.Setenv("ELYSIA_LOGGING_IMAGE", "png")
	first := loggingReviewImagePayload(1)
	if len(first) != 1<<20 || first != loggingReviewImagePayload(1) {
		t.Fatal("PNG fixture must have deterministic content and exact base64 length")
	}
	decoded, err := base64.StdEncoding.DecodeString(first)
	if err != nil {
		t.Fatal(err)
	}
	pixels, err := png.Decode(bytes.NewReader(decoded))
	if err != nil || pixels.Bounds().Dx() != loggingReviewImageWidth {
		t.Fatal("invalid PNG fixture", err)
	}
}

type loggingReviewGate struct {
	release chan struct{}
	unblock func()
	held    atomic.Int64
	entered atomic.Int64
}

type loggingReviewFinalizeWriter struct {
	network.ExtWriter
	ctx  context.Context
	gate *loggingReviewGate
}

func (writer *loggingReviewFinalizeWriter) Finalize() error {
	writer.gate.held.Add(1)
	writer.gate.entered.Add(1)
	defer writer.gate.held.Add(-1)
	select {
	case <-writer.gate.release:
		return writer.ExtWriter.Finalize()
	case <-writer.ctx.Done():
		return writer.ctx.Err()
	}
}

type loggingReviewGateway struct {
	server *Server
	mu     sync.Mutex
	gate   *loggingReviewGate
}

type loggingReviewState struct {
	Held, Entered, Written int64
	ActiveRequests         int
	Queued                 int
	QueueBytes             int64
}

// This opt-in middleware runs the real gateway route and delays only its final
// writer completion. No synthetic usageRecord replaces Incoming/Outgoing logs.
func configureLoggingReviewGateway(t *testing.T, server *Server) *loggingReviewGateway {
	if os.Getenv("ELYSIA_LOGGING_FINALIZE") != "1" {
		return nil
	}
	review := &loggingReviewGateway{server: server}
	server.engine.Use(func(ctx context.Context, request *app.RequestContext) {
		request.Next(ctx)
		if string(request.Path()) != "/v1/messages" {
			return
		}
		review.mu.Lock()
		gate := review.gate
		review.mu.Unlock()
		if gate == nil {
			return
		}
		stream := currentResponseStream(request)
		if stream == nil {
			t.Error("logging review requires a real gateway SSE response")
			return
		}
		stream.ExtWriter = &loggingReviewFinalizeWriter{ExtWriter: stream.ExtWriter, ctx: ctx, gate: gate}
	})
	t.Cleanup(func() {
		review.mu.Lock()
		defer review.mu.Unlock()
		if review.gate != nil {
			review.gate.unblock()
		}
	})
	return review
}

func (review *loggingReviewGateway) state() loggingReviewState {
	var state loggingReviewState
	review.mu.Lock()
	if review.gate != nil {
		state.Held, state.Entered = review.gate.held.Load(), review.gate.entered.Load()
	}
	review.mu.Unlock()
	review.server.httpRequests.mu.Lock()
	state.ActiveRequests = len(review.server.httpRequests.active)
	review.server.httpRequests.mu.Unlock()
	stats := review.server.usageWriterStats()
	state.Queued, state.QueueBytes, state.Written = stats.Queued, stats.RetainedBytes, int64(stats.Written)
	return state
}

func (review *loggingReviewGateway) handleMetrics(writer http.ResponseWriter, request *http.Request) bool {
	action := request.URL.Query().Get("action")
	if !strings.HasPrefix(action, "logging-") {
		return false
	}
	switch action {
	case "logging-arm":
		review.mu.Lock()
		if review.gate != nil && review.gate.held.Load() != 0 {
			review.mu.Unlock()
			http.Error(writer, "previous Finalize gate is active", http.StatusConflict)
			return true
		}
		release := make(chan struct{})
		review.gate = &loggingReviewGate{release: release, unblock: sync.OnceFunc(func() { close(release) })}
		review.mu.Unlock()
	case "logging-release":
		review.mu.Lock()
		if review.gate != nil {
			review.gate.unblock()
		}
		review.mu.Unlock()
	case "logging-verify":
		proof, err := review.verifyLogs(request.Context())
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
		} else {
			_ = json.NewEncoder(writer).Encode(proof)
		}
		return true
	case "logging-state":
	default:
		http.Error(writer, "unknown logging review action", http.StatusBadRequest)
		return true
	}
	_ = json.NewEncoder(writer).Encode(review.state())
	return true
}

// Verification runs after all memory observations. Reading complete persisted
// records must not inflate the held/released gateway heap measurements.
func (review *loggingReviewGateway) verifyLogs(ctx context.Context) (any, error) {
	total, items, err := review.server.store.QueryUsageLogs(ctx, storage.UsageQuery{Limit: 100})
	if err != nil {
		return nil, err
	}
	mode := os.Getenv("ELYSIA_CONVERSION_HTTP_LOG")
	shouldCancel := os.Getenv("ELYSIA_LOGGING_CANCEL") == "1"
	sizeMiB, err := strconv.Atoi(os.Getenv("ELYSIA_CONVERSION_HTTP_MIB"))
	if err != nil || sizeMiB < 1 || sizeMiB > 31 {
		return nil, fmt.Errorf("invalid logging review image size")
	}
	var proofs []map[string]any
	for _, item := range items {
		payload, found, err := review.server.store.GetUsageRecordJSON(ctx, item.RequestID)
		if err != nil || !found {
			return nil, fmt.Errorf("missing usage record %s: %v", item.RequestID, err)
		}
		var record usageRecord
		if err := json.Unmarshal(payload, &record); err != nil {
			return nil, err
		}
		if shouldCancel && (record.Delivery == nil || record.Delivery.Outcome != deliveryFailed || record.Error == "") {
			return nil, fmt.Errorf("Finalize cancellation was not recorded as failed delivery: %s", item.RequestID)
		}
		incoming, outgoing := record.IncomingBody.Content, record.OutgoingBody.Content
		if mode != "off" {
			if record.IncomingBody.Truncated || record.OutgoingBody.Truncated || !strings.Contains(incoming, `"model":"grp"`) || !strings.Contains(outgoing, `"model":"m"`) {
				return nil, fmt.Errorf("full Incoming/Outgoing model proof failed: %s", item.RequestID)
			}
			if mode == "media" && (!strings.Contains(incoming, AssetPlaceholderPrefix) || !strings.Contains(outgoing, AssetPlaceholderPrefix)) {
				return nil, fmt.Errorf("Incoming/Outgoing media proof failed: %s", item.RequestID)
			}
			if mode == "body" && (len(incoming) < sizeMiB<<20 || len(outgoing) < sizeMiB<<20) {
				return nil, fmt.Errorf("Incoming/Outgoing lost the complete image: %s", item.RequestID)
			}
			if mode == "media" {
				placeholder := func(body string) string {
					start := strings.Index(body, AssetPlaceholderPrefix)
					rest := body[start:]
					end := strings.IndexByte(rest, '"')
					if end < 0 {
						return ""
					}
					return rest[:end]
				}
				asset := placeholder(incoming)
				if asset == "" || asset != placeholder(outgoing) {
					return nil, fmt.Errorf("Incoming/Outgoing did not share the same image asset: %s", item.RequestID)
				}
				name := filepath.Base(asset)
				if _, _, ok := parseAssetFileName(name); !ok {
					return nil, fmt.Errorf("invalid image asset placeholder: %s", item.RequestID)
				}
				info, err := os.Stat(filepath.Join(review.server.usageAssetsRoot(), name))
				if err != nil || info.Size() != int64(sizeMiB<<20)*3/4 {
					return nil, fmt.Errorf("persisted image asset has wrong size: %s: %v", item.RequestID, err)
				}
			}
			if record.ProviderResponse.Content == "" || record.DownstreamResponse.Content == "" {
				return nil, fmt.Errorf("response logs missing: %s", item.RequestID)
			}
		} else if incoming != "" || outgoing != "" || record.ProviderResponse.Content != "" || record.DownstreamResponse.Content != "" {
			return nil, fmt.Errorf("disabled body logs were retained: %s", item.RequestID)
		}
		proofs = append(proofs, map[string]any{"requestID": item.RequestID, "incomingBytes": len(incoming), "outgoingBytes": len(outgoing), "providerBytes": len(record.ProviderResponse.Content), "downstreamBytes": len(record.DownstreamResponse.Content), "status": record.StatusCode, "delivery": record.Delivery})
	}
	return map[string]any{"total": total, "records": proofs}, nil
}

func loggingReviewControl(t *testing.T, address, action string, output any) {
	t.Helper()
	response, err := http.Get(address + "?action=" + action)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("logging control %s: HTTP %d: %s", action, response.StatusCode, body)
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		t.Fatal(err)
	}
}

func TestLoggingReviewGatewayFinalize(t *testing.T) {
	if os.Getenv("ELYSIA_LOGGING_FINALIZE") != "1" {
		t.Skip("opt-in isolated gateway Finalize ownership diagnostic")
	}
	size := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_MIB", 2, 31)
	workers := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_WORKERS", 1, 4)
	batches := conversionHTTPInt(t, "ELYSIA_LOGGING_BATCHES", 5, 20)
	shouldCancel := os.Getenv("ELYSIA_LOGGING_CANCEL") == "1"
	provider := startConversionHTTPChild(t, "provider", "")
	gateway := startConversionHTTPChild(t, "gateway", provider.URL)
	t.Cleanup(func() {
		client := &http.Client{Timeout: 5 * time.Second}
		response, err := client.Get(gateway.Metrics + "?action=logging-release")
		if err == nil {
			response.Body.Close()
		}
		client.CloseIdleConnections()
	})
	client := &http.Client{Timeout: 5 * time.Minute}
	defer client.CloseIdleConnections()
	input := flameRequestBody(size, true, "image")
	source := flameMatrixProtocol(t, "anthropic-messages")
	var samples []map[string]any
	sample := func(batch int, phase string, forced bool) {
		action := "&timeline=1"
		if forced {
			action = "reset&timeline=1" // Existing diagnostic endpoint performs two collections.
		}
		var state loggingReviewState
		loggingReviewControl(t, gateway.Metrics, "logging-state", &state)
		memory := reviewMetrics(t, gateway.Metrics, action)
		samples = append(samples, map[string]any{"batch": batch, "phase": phase, "forcedGC": forced, "state": state, "metrics": memory})
	}
	for batch := range batches {
		var state loggingReviewState
		loggingReviewControl(t, gateway.Metrics, "logging-arm", &state)
		beforeCalls := reviewMetrics(t, provider.Metrics, "").Calls
		sample(batch, "before", false)
		cancels := make([]context.CancelFunc, workers)
		done := make(chan error, workers)
		consumed := make(chan error, workers)
		for index := range workers {
			ctx, cancel := context.WithCancel(t.Context())
			cancels[index] = cancel
			defer cancel()
			go func() {
				request, _ := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/messages", strings.NewReader(input))
				request.Header.Set("Authorization", "Bearer "+flameToken)
				request.Header.Set("Content-Type", "application/json")
				response, err := client.Do(request)
				if err != nil {
					consumed <- err
					done <- err
					return
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					err = fmt.Errorf("gateway HTTP %d", response.StatusCode)
					consumed <- err
					done <- err
					return
				}
				var output bytes.Buffer
				reader := bufio.NewReader(io.TeeReader(response.Body, &output))
				hasStop := false
				for {
					line, readErr := reader.ReadString('\n')
					hasStop = hasStop || strings.Contains(line, `"type":"message_stop"`)
					if readErr != nil {
						consumed <- readErr
						done <- readErr
						return
					}
					if hasStop && strings.TrimSpace(line) == "" {
						break
					}
				}
				// The semantic stream is complete before cancellation. Only the
				// final HTTP chunk remains gated, so cancellation cannot hide a
				// missing tool/body/usage/terminal event as an expected read error.
				consumed <- validateFlameOutput(t.Context(), source, output.Bytes(), true, "complete", 10, 1)
				_, err = io.Copy(io.Discard, reader)
				done <- err
			}()
		}
		deadline := time.Now().Add(loggingReviewWait)
		for {
			loggingReviewControl(t, gateway.Metrics, "logging-state", &state)
			if state.Held == int64(workers) && state.Entered == int64(workers) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("full gateway Finalize gate not reached", state)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if state.Written != int64(batch*workers) || state.ActiveRequests != workers {
			t.Fatal("held request settled early or lost lifecycle ownership", state)
		}
		for range workers {
			select {
			case err := <-consumed:
				if err != nil {
					t.Fatal("gateway stream failed complete semantic validation", err)
				}
			case <-time.After(loggingReviewWait):
				t.Fatal("gateway stream did not reach message_stop before Finalize")
			}
		}
		sample(batch, "held", false)
		sample(batch, "held", true)
		if batch == 0 {
			reviewProfile(t, gateway, "heap-finalize-held.pprof", "heap")
		}
		if shouldCancel {
			for _, cancel := range cancels {
				cancel()
			}
		} else {
			loggingReviewControl(t, gateway.Metrics, "logging-release", &state)
		}
		var clientReadErrors []string
		for range workers {
			select {
			case err := <-done:
				if !shouldCancel && err != nil {
					t.Fatal(err)
				}
				if err != nil {
					clientReadErrors = append(clientReadErrors, err.Error())
				}
			case <-time.After(loggingReviewWait):
				t.Fatal("gateway client did not complete")
			}
		}
		reviewMetrics(t, gateway.Metrics, "settle")
		deadline = time.Now().Add(loggingReviewWait)
		for {
			loggingReviewControl(t, gateway.Metrics, "logging-state", &state)
			if state.Held == 0 && state.ActiveRequests == 0 && state.QueueBytes == 0 && state.Queued == 0 && state.Written == int64((batch+1)*workers) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("gateway finalization or log ownership did not drain", state)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if calls := reviewMetrics(t, provider.Metrics, "").Calls - beforeCalls; calls != int64(workers) {
			t.Fatal("missing or duplicated upstream generation", calls)
		}
		sample(batch, "released", false)
		samples[len(samples)-1]["clientReadErrors"] = clientReadErrors
		sample(batch, "released", true)
	}
	var proof map[string]any
	loggingReviewControl(t, gateway.Metrics, "logging-verify", &proof)
	if proof["total"] != float64(batches*workers) {
		t.Fatal("persisted log count mismatch", proof["total"])
	}
	result := map[string]any{"scope": "gateway-only child; real Incoming and Outgoing logs; forced-GC ownership diagnostic, not latency benchmark", "sizeMiB": size, "workers": workers, "image": os.Getenv("ELYSIA_LOGGING_IMAGE"), "log": os.Getenv("ELYSIA_CONVERSION_HTTP_LOG"), "cancel": shouldCancel, "samples": samples, "proof": proof}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("ELYSIA_FLAME_OUTPUT"), "gateway-finalize-retention.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}
