package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

// Waiting only reads a task and optionally records cancellation. Embedded nil
// interfaces make unexpected submit, poll, settlement, or repository calls fail.
type waitJobRepository struct {
	protocol.JobRepository
	protocol.JobExecutor
	protocol.JobSettlementWriter
	job       protocol.GenerationJob
	firstRead func(context.Context) (protocol.GenerationJob, error)
	updateErr error
	reads     int
	writes    int
}

func (r *waitJobRepository) ReadGenerationJob(ctx context.Context, id string) (protocol.GenerationJob, error) {
	if id != r.job.Task.ID {
		return protocol.GenerationJob{}, protocol.ErrJobNotFound
	}
	r.reads++
	if r.reads == 1 {
		return r.firstRead(ctx)
	}
	return r.job, ctx.Err()
}

func (r *waitJobRepository) UpdateGenerationJob(ctx context.Context, job protocol.GenerationJob, revision int64) (protocol.GenerationJob, error) {
	if err := ctx.Err(); err != nil {
		return protocol.GenerationJob{}, err
	}
	if r.updateErr != nil {
		return protocol.GenerationJob{}, r.updateErr
	}
	r.writes++
	r.job = job
	return job, nil
}

func TestGatewayJobWaitReadOutcomes(t *testing.T) {
	for _, scenario := range []string{"deadline-during-read", "deadline-between-polls", "caller-canceled", "read-error", "cancel-write-error"} {
		for _, policy := range []string{"cancel", "continue"} {
			t.Run(scenario+"/"+policy, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					job := protocol.GenerationJob{
						Task:      protocol.Task{ID: "wait-job", CanCancel: true, UpstreamID: protocol.StringValue("provider-job")},
						OwnerHash: "owner", Phase: protocol.JobPolling,
					}
					requestContext, cancelRequest := context.WithCancel(t.Context())
					defer cancelRequest()
					repository := &waitJobRepository{job: job}
					repository.firstRead = func(ctx context.Context) (protocol.GenerationJob, error) {
						switch scenario {
						case "read-error":
							return protocol.GenerationJob{}, errors.New("repository unavailable")
						case "deadline-between-polls":
							return job, nil
						case "caller-canceled":
							cancelRequest()
						}
						<-ctx.Done()
						return protocol.GenerationJob{}, ctx.Err()
					}
					if scenario == "cancel-write-error" {
						repository.updateErr = errors.New("cancel persistence unavailable")
					}
					coordinator, err := protocol.NewJobCoordinator(repository, repository, repository)
					if err != nil {
						t.Fatal(err)
					}
					response := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(response)
					c.Request = httptest.NewRequestWithContext(requestContext, http.MethodPost, "/gateway/wait/generate", nil)
					server := &Server{}
					server.waitGatewayJob(c, coordinator, nil, job, protocol.JobWait{TimeoutMillis: 60, OnTimeout: policy}, &usageRecord{})

					status, message := http.StatusGatewayTimeout, "task wait-job follows the declared "+policy+" policy"
					wantsCancel := policy == "cancel"
					switch {
					case scenario == "read-error":
						status, message, wantsCancel = http.StatusBadGateway, "repository unavailable", false
					case scenario == "cancel-write-error" && policy == "cancel":
						status, message, wantsCancel = http.StatusBadGateway, "cancel persistence unavailable", false
					case scenario == "caller-canceled":
						status = statusClientClosedRequest
					}
					if response.Code != status || !strings.Contains(response.Body.String(), message) {
						t.Fatalf("response = %d %s; want %d containing %q", response.Code, response.Body, status, message)
					}
					if repository.reads == 0 {
						t.Fatal("wait never exercised the repository read")
					}
					if (repository.writes == 1) != wantsCancel || repository.job.IsCancelRequested != wantsCancel {
						t.Fatalf("cancel writes=%d requested=%t; want %t", repository.writes, repository.job.IsCancelRequested, wantsCancel)
					}
				})
			})
		}
	}
}
