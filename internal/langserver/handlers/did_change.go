// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package handlers

import (
	"context"

	"github.com/hashicorp/terraform-ls/internal/document"
	ilsp "github.com/hashicorp/terraform-ls/internal/lsp"
	lsp "github.com/hashicorp/terraform-ls/internal/protocol"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

const tracerName = "github.com/hashicorp/terraform-ls/internal/langserver/handlers"

func (svc *service) TextDocumentDidChange(ctx context.Context, params lsp.DidChangeTextDocumentParams) error {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "TextDocumentDidChange")
	defer span.End()
	
	p := lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: lsp.TextDocumentIdentifier{
				URI: params.TextDocument.URI,
			},
			Version: params.TextDocument.Version,
		},
		ContentChanges: params.ContentChanges,
	}

	dh := ilsp.HandleFromDocumentURI(p.TextDocument.URI)
	doc, err := svc.stateStore.DocumentStore.GetDocument(dh)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	newVersion := int(p.TextDocument.Version)

	// Versions don't have to be consecutive, but they must be increasing
	if newVersion <= doc.Version {
		svc.logger.Printf("Old document version (%d) received, current version is %d. "+
			"Ignoring this update for %s. This is likely a client bug, please report it.",
			newVersion, doc.Version, p.TextDocument.URI)
		return nil
	}
	changes := ilsp.DocumentChanges(params.ContentChanges)
	newText, err := document.ApplyChanges(doc.Text, changes)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	err = svc.stateStore.DocumentStore.UpdateDocument(dh, newText, newVersion)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	// check existence
	_, err = svc.modStore.ModuleByPath(dh.Dir.Path())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	jobIds, err := svc.indexer.DocumentChanged(ctx, dh.Dir)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	return svc.stateStore.JobStore.WaitForJobs(ctx, jobIds...)
}
