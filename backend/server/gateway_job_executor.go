package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

type gatewayJobService struct {
	mu          sync.Mutex
	coordinator *protocol.JobCoordinator
	cancel      context.CancelFunc
	done        chan struct{}
	isStopped   bool
}

func (s *Server) generationJobs() (*protocol.JobCoordinator, error) {
	s.gatewayJobs.mu.Lock()
	defer s.gatewayJobs.mu.Unlock()
	if s.gatewayJobs.isStopped {
		return nil, fmt.Errorf("generation job service is stopping")
	}
	if s.gatewayJobs.coordinator == nil {
		if s.store == nil {
			return nil, fmt.Errorf("generation jobs require persistent storage")
		}
		coordinator, err := protocol.NewJobCoordinator(s.store, gatewayJobExecutor{s}, gatewayJobSettlements{s})
		if err != nil {
			return nil, err
		}
		s.gatewayJobs.coordinator = coordinator
	}
	return s.gatewayJobs.coordinator, nil
}

func (s *Server) startGatewayJobs() error {
	if s.store == nil {
		return nil
	}
	coordinator, err := s.generationJobs()
	if err != nil {
		return err
	}
	s.gatewayJobs.mu.Lock()
	defer s.gatewayJobs.mu.Unlock()
	if s.gatewayJobs.isStopped {
		return fmt.Errorf("generation jobs are stopping")
	}
	if s.gatewayJobs.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.gatewayJobs.cancel, s.gatewayJobs.done = cancel, make(chan struct{})
	go func() {
		defer close(s.gatewayJobs.done)
		coordinator.Run(ctx, func(err error) { log.Printf("generation job worker: %v", err) })
	}()
	return nil
}

func (jobs *gatewayJobService) stop() {
	jobs.mu.Lock()
	jobs.isStopped = true
	cancel, done := jobs.cancel, jobs.done
	jobs.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

type gatewayJobExecutor struct{ server *Server }

func jobBaseHash(base string) string {
	sum := sha256.Sum256([]byte(base))
	return hex.EncodeToString(sum[:])
}

// Job recovery searches the already-pinned account; it never advances routing
// key rotation or silently chooses a different credential from the same source.
func jobAccountCandidates(models []config.ModelRef) []config.ModelRef {
	var candidates []config.ModelRef
	for _, model := range models {
		keys := model.APIKeys
		if len(keys) == 0 {
			keys = []string{model.APIKey}
		}
		for _, key := range keys {
			copy := model
			copy.APIKey = key
			candidates = append(candidates, copy)
		}
	}
	return candidates
}

func (executor gatewayJobExecutor) resolve(ctx context.Context, job protocol.GenerationJob) (*protocol.Compiled, config.ModelRef, error) {
	s := executor.server
	service, err := s.protocolService()
	if err != nil {
		return nil, config.ModelRef{}, err
	}
	compiled, err := service.LoadRevision(ctx, job.Task.Protocol.DefinitionID, job.UpstreamRevision)
	if err != nil {
		return nil, config.ModelRef{}, err
	}
	for _, group := range s.getGroups() {
		if group.ID != job.GroupID {
			continue
		}
		for _, model := range jobAccountCandidates(group.Models) {
			if model.ID != job.ModelID || model.Name != job.Model || model.SourceID != job.Task.ModelSourceID || modelProtocolScope(model).Account != job.SourceAccount || jobBaseHash(model.BaseURL) != job.SourceBaseHash {
				continue
			}
			if err := s.validateOutbound(model.BaseURL); err != nil {
				return nil, model, err
			}
			return compiled, model, nil
		}
	}
	return nil, config.ModelRef{}, fmt.Errorf("pinned job model/account is unavailable; job will not switch accounts")
}

func (executor gatewayJobExecutor) Submit(ctx context.Context, job protocol.GenerationJob, request *protocol.Request) (protocol.JobUpdate, error) {
	compiled, model, err := executor.resolve(ctx, job)
	if err != nil {
		return protocol.JobUpdate{}, err
	}
	copy := *request
	copy.Model = protocol.StringValue(job.Model)
	body, err := compiled.EncodeRequest(ctx, &copy, protocol.EvaluationContext{Scope: modelProtocolScope(model)})
	if err != nil {
		return protocol.JobUpdate{}, err
	}
	operation := compiled.Operations()[job.Operation]
	if header := operation.Task.IdempotencyHeader; header != "" {
		if operation.Headers == nil {
			operation.Headers = map[string]string{}
		}
		operation.Headers[header] = job.Task.ID
	}
	response, err := executor.send(ctx, model, operation, body, map[string]string{"model": model.Identifier()})
	if err != nil {
		return protocol.JobUpdate{}, err
	}
	return compiled.DecodeTaskUpdate(ctx, job.Operation, "submit", response)
}

func (executor gatewayJobExecutor) Poll(ctx context.Context, job protocol.GenerationJob) (protocol.JobUpdate, error) {
	return executor.update(ctx, job, "status")
}
func (executor gatewayJobExecutor) Cancel(ctx context.Context, job protocol.GenerationJob) (protocol.JobUpdate, error) {
	return executor.update(ctx, job, "cancel")
}

func (executor gatewayJobExecutor) update(ctx context.Context, job protocol.GenerationJob, purpose string) (protocol.JobUpdate, error) {
	compiled, body, _, err := executor.control(ctx, job, purpose)
	if err != nil {
		return protocol.JobUpdate{}, err
	}
	return compiled.DecodeTaskUpdate(ctx, job.Operation, purpose, body)
}

func (executor gatewayJobExecutor) Result(ctx context.Context, job protocol.GenerationJob) (*protocol.Response, error) {
	compiled, body, model, err := executor.control(ctx, job, "result")
	if err != nil {
		return nil, err
	}
	response, err := compiled.DecodeResponse(ctx, body.Bytes(), protocol.EvaluationContext{Scope: modelProtocolScope(model)})
	if err != nil {
		return nil, err
	}
	if err := protocol.IssuesError(protocol.CheckModelResponse(response, compiled, job.Binding, modelProtocolScope(model))); err != nil {
		return nil, err
	}
	return response, nil
}

func (executor gatewayJobExecutor) control(ctx context.Context, job protocol.GenerationJob, purpose string) (*protocol.Compiled, protocol.Value, config.ModelRef, error) {
	compiled, model, err := executor.resolve(ctx, job)
	if err != nil {
		return nil, protocol.Value{}, model, err
	}
	operations := compiled.Operations()
	flow := operations[job.Operation].Task
	if flow == nil {
		return nil, protocol.Value{}, model, fmt.Errorf("pinned task flow is unavailable")
	}
	name := map[string]string{"status": flow.Status, "result": flow.Result, "cancel": flow.Cancel}[purpose]
	operation, exists := operations[name]
	if !exists {
		return nil, protocol.Value{}, model, fmt.Errorf("task operation is unsupported")
	}
	var id string
	if err := job.Task.UpstreamID.Decode(&id); err != nil {
		return nil, protocol.Value{}, model, err
	}
	control, err := compiled.EncodeTaskControl(ctx, job.Operation, purpose, id)
	if err != nil {
		return nil, protocol.Value{}, model, err
	}
	operation, err = protocol.ApplyTaskControl(operation, control)
	if err != nil {
		return nil, protocol.Value{}, model, err
	}
	body, err := executor.send(ctx, model, operation, control.Body.Bytes(), map[string]string{"taskId": id, "model": model.Name})
	return compiled, body, model, err
}

func (executor gatewayJobExecutor) send(ctx context.Context, model config.ModelRef, operation protocol.Operation, body []byte, parameters map[string]string) (protocol.Value, error) {
	response, err := executor.server.protocolTransport.SendProtocolRequest(ctx, model.BaseURL, model.APIKey, operation, body, parameters)
	if err != nil {
		return protocol.Value{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return protocol.Value{}, fmt.Errorf("upstream task operation returned HTTP %d", response.StatusCode)
	}
	raw, err := protocol.ReadBoundedBody(response.Body, protocol.DefaultLimits().BufferBytes)
	if err != nil {
		return protocol.Value{}, err
	}
	return protocol.ParseValue(raw)
}

type gatewayJobSettlements struct{ server *Server }

func (writer gatewayJobSettlements) SettleJob(ctx context.Context, job protocol.GenerationJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	record := &usageRecord{RequestID: job.Task.SettlementID, StartedAt: job.CreatedAt, EndedAt: job.UpdatedAt, KeyHash: job.OwnerHash, GroupID: job.GroupID, GroupName: job.GroupName, RequestedModelGroup: job.GroupName, ModelName: job.Model, SourceID: job.Task.ModelSourceID, IngressRevision: job.IngressRevision, UpstreamRevision: job.UpstreamRevision, SourceFormat: job.IngressID, TargetFormat: job.Task.Protocol.DefinitionID, ProtocolResponseID: job.Task.ID, RelayMode: "protocol_v2_async", StatusCode: http.StatusOK, ConversionIssues: job.Issues}
	record.DurationMs = job.UpdatedAt.Sub(job.CreatedAt).Milliseconds()
	if job.Task.Status != protocol.TaskCompleted {
		record.StatusCode = http.StatusBadGateway
		record.Error = "generation task " + string(job.Task.Status)
	}
	updateRecordProtocolUsage(record, job.Usage)
	// This synchronous write is idempotent by SettlementID. Only after success
	// may the coordinator acknowledge its durable outbox entry.
	if err := writer.server.saveUsageRecordToStore(record); err != nil {
		return err
	}
	writer.server.usageCache.flush()
	return nil
}

const gatewayJobWaitInterval = 50 * time.Millisecond
