package core

import (
	"bytes"
	"context"
)

type (
	Document struct {
		Data bytes.Buffer
	}

	DocumentStore interface {
		FindID(ctx context.Context, id string) (*Document, error)
		Create(ctx context.Context, document *Document) (string, error)
		// SaveDocument upserts a document under a caller-supplied id. Used for
		// public share snapshots keyed by a canvas's stable share token.
		SaveDocument(ctx context.Context, id string, document *Document) error
	}
)
