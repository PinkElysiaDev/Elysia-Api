package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

func (s *Server) submitGatewayJob(c *gin.Context, record *usageRecord, plan *gatewayPlan, candidate gatewayCandidate) bool {
	if !candidate.binding.Capabilities[protocol.AsyncJobsCapability] {
		s.failGateway(c, record, 400, fmt.Errorf("model binding excludes asynchronous tasks"))
		return false
	}
	if plan.operation.Kind != "submit" && candidate.binding.Wait == nil {
		s.failGateway(c, record, 400, fmt.Errorf("synchronous bridging requires an explicit wait policy"))
		return false
	}
	if plan.operation.Task != nil && plan.operation.Task.Cancel != "" && candidate.operation.Task.Cancel == "" {
		s.failGateway(c, record, 400, fmt.Errorf("upstream does not support the ingress cancellation contract"))
		return false
	}
	ingressOperation := ""
	for name, operation := range plan.ingress.Operations() {
		if operation.Kind == plan.operation.Kind && operation.Path == plan.operation.Path && operation.Method == plan.operation.Method {
			ingressOperation = name
		}
	}
	job := protocol.GenerationJob{
		Task:      protocol.Task{ID: record.RequestID, SettlementID: record.RequestID, Protocol: candidate.compiled.Identity(), ModelSourceID: candidate.model.SourceID, CanCancel: candidate.operation.Task.Cancel != ""},
		OwnerHash: record.KeyHash, GroupID: plan.group.ID, GroupName: plan.group.Name, IngressID: plan.ingress.Identity().DefinitionID, IngressRevision: plan.ingress.Hash(), IngressOperation: ingressOperation,
		UpstreamRevision: candidate.compiled.Hash(), SourceAccount: candidate.scope.Account, SourceBaseHash: jobBaseHash(candidate.model.BaseURL), ModelID: candidate.model.ID, Model: candidate.model.Name, Operation: candidate.operationName, Binding: candidate.binding,
	}
	job.DeduplicationHash = protocol.JobDeduplicationHash(job.OwnerHash, job.IngressID, c.GetHeader("Idempotency-Key"), job.Task.ID)
	coordinator, err := s.generationJobs()
	if err != nil {
		s.failGateway(c, record, 503, err)
		return false
	}
	release, err := s.acquireRateLimit(plan.group, s.estimateProtocolTokens(plan.request))
	if err != nil {
		s.failGateway(c, record, 429, err)
		return false
	}
	defer release()
	job, _, err = coordinator.Submit(c.Request.Context(), job, plan.request)
	if errors.Is(err, protocol.ErrIdempotencyConflict) {
		s.failGateway(c, record, 409, err)
		return false
	}
	if job.Revision == 0 {
		s.failGateway(c, record, 502, err)
		return false
	}
	if err := s.startGatewayJobs(); err != nil {
		s.failGateway(c, record, 503, err)
		return true
	}
	c.Header("Location", "/gateway/"+job.IngressID+"/_jobs/"+job.Task.ID)
	if plan.operation.Kind == "submit" {
		s.writeGatewayJobReceipt(c, plan.ingress, job, "submit", http.StatusAccepted)
		return true
	}
	if job.Phase == protocol.JobUncertain {
		s.failGateway(c, record, http.StatusBadGateway, fmt.Errorf("generation submission outcome is uncertain; inspect its Location and do not resubmit"))
		return true
	}
	s.waitGatewayJob(c, coordinator, plan.ingress, job, *candidate.binding.Wait, record)
	return true
}

func (s *Server) waitGatewayJob(c *gin.Context, coordinator *protocol.JobCoordinator, ingress *protocol.Compiled, job protocol.GenerationJob, policy protocol.JobWait, record *usageRecord) {
	ctx, stop := context.WithTimeout(c.Request.Context(), time.Duration(policy.TimeoutMillis)*time.Millisecond)
	defer stop()
	ticker := time.NewTicker(gatewayJobWaitInterval)
	defer ticker.Stop()
	for {
		if job.Phase == protocol.JobTerminal {
			s.writeGatewayJobResult(c, ingress, job)
			return
		}
		select {
		case <-ctx.Done():
			if policy.OnTimeout == "cancel" {
				deadline, cancel := context.WithTimeout(context.WithoutCancel(ctx), protocol.DefaultJobCommitTimeout)
				_, err := coordinator.RequestCancel(deadline, job.Task.ID, job.OwnerHash)
				cancel()
				if err != nil {
					s.failGateway(c, record, 502, err)
					return
				}
			}
			s.failGateway(c, record, http.StatusGatewayTimeout, fmt.Errorf("generation wait ended; task %s follows the declared %s policy", job.Task.ID, policy.OnTimeout))
			return
		case <-ticker.C:
			var err error
			job, err = coordinator.Read(ctx, job.Task.ID, job.OwnerHash)
			if err != nil {
				s.failGateway(c, record, 502, err)
				return
			}
		}
	}
}

func (s *Server) serveGatewayJobControl(c *gin.Context, service *protocol.Service, active *protocol.Compiled) bool {
	path := c.Param("path")
	id, purpose := "", ""
	isReserved := strings.HasPrefix(path, "/_jobs/")
	if isReserved {
		parts := strings.Split(strings.TrimPrefix(path, "/_jobs/"), "/")
		if len(parts) == 1 && c.Request.Method == http.MethodGet {
			id, purpose = parts[0], "status"
		}
		if len(parts) == 1 && c.Request.Method == http.MethodDelete {
			id, purpose = parts[0], "cancel"
		}
		if len(parts) == 2 && parts[1] == "result" && c.Request.Method == http.MethodGet {
			id, purpose = parts[0], "result"
		}
	} else {
		for _, operation := range active.Operations() {
			if operation.Kind != "status" && operation.Kind != "result" && operation.Kind != "cancel" {
				continue
			}
			if parameters, matched := protocol.MatchOperationPath(operation.Path, path); matched && operation.Method == c.Request.Method {
				id, purpose = parameters["taskId"], operation.Kind
			}
		}
	}
	if id == "" {
		if isReserved {
			respondFail(c, 404, "task_not_found", "unknown task path")
			return true
		}
		return false
	}
	coordinator, err := s.generationJobs()
	if err != nil {
		respondProtocolError(c, err)
		return true
	}
	job, err := coordinator.Read(c.Request.Context(), id, c.GetString("elysiaKeyHash"))
	if err != nil || job.IngressID != active.Identity().DefinitionID {
		respondFail(c, 404, "task_not_found", "generation job not found")
		return true
	}
	canAccessGroup := false
	for _, group := range s.getGroups() {
		if group.ID == job.GroupID && s.tokenAllowsGroup(c, group.Name) {
			canAccessGroup = true
			break
		}
	}
	if !canAccessGroup {
		respondFail(c, 403, "forbidden", "API key is not allowed to access this job's model group")
		return true
	}
	ingress, err := service.LoadRevision(c.Request.Context(), job.IngressID, job.IngressRevision)
	if err != nil {
		respondProtocolError(c, err)
		return true
	}
	if !isReserved {
		flow := ingress.Operations()[job.IngressOperation].Task
		if flow == nil {
			respondFail(c, 404, "task_not_found", "job has no native control operation")
			return true
		}
		name := map[string]string{"status": flow.Status, "result": flow.Result, "cancel": flow.Cancel}[purpose]
		operation := ingress.Operations()[name]
		if _, matched := protocol.MatchOperationPath(operation.Path, path); !matched || operation.Method != c.Request.Method {
			respondFail(c, 409, "task_revision_mismatch", "use the job Location endpoint for its pinned revision")
			return true
		}
	}
	if purpose == "cancel" {
		job, err = coordinator.RequestCancel(c.Request.Context(), job.Task.ID, job.OwnerHash)
		if err != nil {
			respondProtocolError(c, err)
			return true
		}
	}
	if purpose == "result" {
		s.writeGatewayJobResult(c, ingress, job)
	} else {
		s.writeGatewayJobReceipt(c, ingress, job, purpose, http.StatusOK)
	}
	return true
}

func (s *Server) writeGatewayJobReceipt(c *gin.Context, ingress *protocol.Compiled, job protocol.GenerationJob, purpose string, status int) {
	receipt := protocol.JobUpdate{UpstreamID: protocol.StringValue(job.Task.ID), Status: job.Task.Status, Usage: job.Usage, Error: job.ProviderError}
	var wire protocol.Value
	var err error
	if ingress.Operations()[job.IngressOperation].Kind == "submit" {
		wire, err = ingress.EncodeTaskReceipt(c.Request.Context(), job.IngressOperation, purpose, receipt)
	} else {
		wire, err = protocol.EncodeValue(receipt)
	}
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	c.Header("Location", "/gateway/"+job.IngressID+"/_jobs/"+job.Task.ID)
	c.Data(status, "application/json", wire.Bytes())
}

func (s *Server) writeGatewayJobResult(c *gin.Context, ingress *protocol.Compiled, job protocol.GenerationJob) {
	if job.Phase != protocol.JobTerminal {
		s.writeGatewayJobReceipt(c, ingress, job, "result", http.StatusAccepted)
		return
	}
	if job.Task.Status != protocol.TaskCompleted {
		s.writeGatewayJobReceipt(c, ingress, job, "result", http.StatusConflict)
		return
	}
	response := *job.Task.Result
	if !response.Model.IsZero() {
		response.Model = protocol.StringValue(job.GroupName)
	}
	body, err := ingress.EncodeResponse(c.Request.Context(), &response, protocol.EvaluationContext{Scope: protocol.Scope{Provider: job.Task.ModelSourceID, Account: job.SourceAccount, Model: job.Model}})
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", body)
}
