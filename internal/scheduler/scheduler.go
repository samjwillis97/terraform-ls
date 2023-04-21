// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"

	"github.com/hashicorp/terraform-ls/internal/job"
	"go.opentelemetry.io/otel"
	// "go.opentelemetry.io/otel"
)

const tracerName = "github.com/hashicorp/terraform-ls/internal/scheduler"

type Scheduler struct {
	logger      *log.Logger
	jobStorage  JobStorage
	parallelism int
	priority    job.JobPriority
	stopFunc    context.CancelFunc
}

type JobStorage interface {
	job.JobStore
	AwaitNextJob(ctx context.Context, priority job.JobPriority) (job.ID, job.Job, error)
	FinishJob(id job.ID, jobErr error, deferredJobIds ...job.ID) error
}

func NewScheduler(jobStorage JobStorage, parallelism int, priority job.JobPriority) *Scheduler {
	discardLogger := log.New(ioutil.Discard, "", 0)

	return &Scheduler{
		logger:      discardLogger,
		jobStorage:  jobStorage,
		parallelism: parallelism,
		priority:    priority,
		stopFunc:    func() {},
	}
}

func (s *Scheduler) SetLogger(logger *log.Logger) {
	s.logger = logger
}

func (s *Scheduler) Start(ctx context.Context) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "scheduler_Start")
	defer span.End()
	
	ctx, cancelFunc := context.WithCancel(ctx)
	s.stopFunc = cancelFunc

	span.AddEvent(fmt.Sprintf("Loading %d processors", s.parallelism))
	for i := 0; i < s.parallelism; i++ {
		ctx, span := otel.Tracer(tracerName).Start(ctx, "scheduler_Start_loop")
		defer span.End()

		s.logger.Printf("launching eval loop %d", i)
		span.AddEvent(fmt.Sprintf("launching eval loop %d", i))
		go s.eval(ctx)
	}
}

func (s *Scheduler) Stop() {
	s.stopFunc()
	s.logger.Print("stopped scheduler")
}

func (s *Scheduler) eval(ctx context.Context) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "scheduler_eval")
	defer span.End()
	
	for {
		ctx, span := otel.Tracer(tracerName).Start(ctx, "scheduler_eval_loop")
		defer span.End()
		
		id, nextJob, err := s.jobStorage.AwaitNextJob(ctx, s.priority)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			s.logger.Printf("failed to obtain next job: %s", err)
			return
		}

		ctx = job.WithIgnoreState(ctx, nextJob.IgnoreState)

		jobErr := nextJob.Func(ctx)

		deferredJobIds := make(job.IDs, 0)
		if nextJob.Defer != nil {
			deferredJobIds, err = nextJob.Defer(ctx, jobErr)
			if err != nil {
				s.logger.Printf("deferred job failed: %s", err)
			}
		}

		err = s.jobStorage.FinishJob(id, jobErr, deferredJobIds...)
		if err != nil {
			s.logger.Printf("failed to finish job: %s", err)
			return
		}

		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}
