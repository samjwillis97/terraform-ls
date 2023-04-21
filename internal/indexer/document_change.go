// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package indexer

import (
	"context"

	"github.com/hashicorp/terraform-ls/internal/document"
	"github.com/hashicorp/terraform-ls/internal/job"
	"github.com/hashicorp/terraform-ls/internal/schemas"
	"github.com/hashicorp/terraform-ls/internal/terraform/module"
	op "github.com/hashicorp/terraform-ls/internal/terraform/module/operation"
	"go.opentelemetry.io/otel"
)

const tracerName = "github.com/hashicorp/terraform-ls/internal/indexer"

func (idx *Indexer) DocumentChanged(ctx context.Context, modHandle document.DirHandle) (job.IDs, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "DocumentChanged")
	defer span.End()

	currentSpanId := span.SpanContext().SpanID()
	currentTraceId := span.SpanContext().TraceID()

	// propagator = otel.newTraceContextPropagator()
	// context = propagator.extract(headers)

	// context = otel.getCurrentContext()
	// currentSpanId := span.SpanContext().SpanID()
	// ctx.

	// span := trace.SpanFromContext(ctx)
	// defer span.End()

	ids := make(job.IDs, 0)

	span.AddEvent("queuing ParseModuleConfiguration")
	parseId, err := idx.jobStore.EnqueueJob(job.Job{
		Dir: modHandle,
		Func: func(ctx context.Context) error {
			return module.ParseModuleConfiguration(ctx, idx.fs, idx.modStore, modHandle.Path(), currentTraceId, currentSpanId)
		},
		Type:        op.OpTypeParseModuleConfiguration.String(),
		IgnoreState: true,
	})
	if err != nil {
		return ids, err
	}
	ids = append(ids, parseId)

	span.AddEvent("queuing decodeModule")
	modIds, err := idx.decodeModule(ctx, modHandle, job.IDs{parseId}, true)
	if err != nil {
		return ids, err
	}
	ids = append(ids, modIds...)

	span.AddEvent("queuing ParseVariables")
	parseVarsId, err := idx.jobStore.EnqueueJob(job.Job{
		Dir: modHandle,
		Func: func(ctx context.Context) error {
			return module.ParseVariables(ctx, idx.fs, idx.modStore, modHandle.Path(), currentTraceId, currentSpanId)
		},
		Type:        op.OpTypeParseVariables.String(),
		IgnoreState: true,
	})
	if err != nil {
		return ids, err
	}
	ids = append(ids, parseVarsId)

	span.AddEvent("queuing DecodeVarsReferences")
	varsRefsId, err := idx.jobStore.EnqueueJob(job.Job{
		Dir: modHandle,
		Func: func(ctx context.Context) error {
			return module.DecodeVarsReferences(ctx, idx.modStore, idx.schemaStore, modHandle.Path(), currentTraceId, currentSpanId)
		},
		Type:        op.OpTypeDecodeVarsReferences.String(),
		DependsOn:   job.IDs{parseVarsId},
		IgnoreState: true,
	})
	if err != nil {
		return ids, err
	}
	ids = append(ids, varsRefsId)

	return ids, nil
}

func (idx *Indexer) decodeModule(ctx context.Context, modHandle document.DirHandle, dependsOn job.IDs, ignoreState bool) (job.IDs, error) {
	_, span := otel.Tracer(tracerName).Start(ctx, "decodeModule")
	defer span.End()

	currentSpanId := span.SpanContext().SpanID()
	currentTraceId := span.SpanContext().TraceID()

	ids := make(job.IDs, 0)

	span.AddEvent("queuing LoadModuleMetadata")
	metaId, err := idx.jobStore.EnqueueJob(job.Job{
		Dir: modHandle,
		Func: func(ctx context.Context) error {
			return module.LoadModuleMetadata(ctx, idx.modStore, modHandle.Path(), currentTraceId, currentSpanId)
		},
		Type:        op.OpTypeLoadModuleMetadata.String(),
		DependsOn:   dependsOn,
		IgnoreState: ignoreState,
	})
	if err != nil {
		return ids, err
	}
	ids = append(ids, metaId)

	span.AddEvent("queuing PreloadEmbeddedSchema")
	eSchemaId, err := idx.jobStore.EnqueueJob(job.Job{
		Dir: modHandle,
		Func: func(ctx context.Context) error {
			return module.PreloadEmbeddedSchema(ctx, idx.logger, schemas.FS, idx.modStore, idx.schemaStore, modHandle.Path(), currentTraceId, currentSpanId)
		},
		DependsOn:   job.IDs{metaId},
		Type:        op.OpTypePreloadEmbeddedSchema.String(),
		IgnoreState: ignoreState,
	})
	if err != nil {
		return ids, err
	}
	ids = append(ids, eSchemaId)

	span.AddEvent("queuing DecodeReferenceTargets")
	refTargetsId, err := idx.jobStore.EnqueueJob(job.Job{
		Dir: modHandle,
		Func: func(ctx context.Context) error {
			return module.DecodeReferenceTargets(ctx, idx.modStore, idx.schemaStore, modHandle.Path(), currentTraceId, currentSpanId)
		},
		Type:        op.OpTypeDecodeReferenceTargets.String(),
		DependsOn:   job.IDs{metaId},
		IgnoreState: ignoreState,
	})
	if err != nil {
		return ids, err
	}
	ids = append(ids, refTargetsId)

	span.AddEvent("queuing DecodeReferenceOrigins")
	refOriginsId, err := idx.jobStore.EnqueueJob(job.Job{
		Dir: modHandle,
		Func: func(ctx context.Context) error {
			return module.DecodeReferenceOrigins(ctx, idx.modStore, idx.schemaStore, modHandle.Path(), currentTraceId, currentSpanId)
		},
		Type:        op.OpTypeDecodeReferenceOrigins.String(),
		DependsOn:   job.IDs{metaId},
		IgnoreState: ignoreState,
	})
	if err != nil {
		return ids, err
	}
	ids = append(ids, refOriginsId)

	span.AddEvent("queuing GetModuleDataFromRegistry")
	registryId, err := idx.jobStore.EnqueueJob(job.Job{
		Dir: modHandle,
		Func: func(ctx context.Context) error {
			return module.GetModuleDataFromRegistry(ctx, idx.registryClient,
				idx.modStore, idx.registryModStore, modHandle.Path(), currentTraceId, currentSpanId)
		},
		Priority: job.LowPriority,
		Type:     op.OpTypeGetModuleDataFromRegistry.String(),
	})
	if err != nil {
		return ids, err
	}

	ids = append(ids, registryId)

	return ids, nil
}
