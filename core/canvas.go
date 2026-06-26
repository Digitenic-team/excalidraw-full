package core

import (
	"context"
	"time"
)

type (
	// Canvas represents the metadata and content of a user-saved drawing.
	Canvas struct {
		ID        string    `json:"id"`
		UserID    string    `json:"-"` // Not exposed in JSON responses, used internally.
		Name      string    `json:"name"`
		Thumbnail string    `json:"thumbnail,omitempty"`
		Data      []byte    `json:"data,omitempty"`    // The full canvas data, not included in list views.
		ShareID   string    `json:"shareId,omitempty"` // Stable public share token, generated once on first publish.
		Public    bool      `json:"public,omitempty"`  // Whether the canvas is currently shared publicly (view-only).
		CreatedAt time.Time `json:"createdAt"`
		UpdatedAt time.Time `json:"updatedAt"`
	}

	// CanvasStore defines the persistence layer for user-owned canvases.
	// All operations are scoped to a specific user.
	CanvasStore interface {
		// List returns metadata for all canvases owned by a user.
		// The returned Canvas objects should not contain the `Data` field to keep the response light.
		List(ctx context.Context, userID string) ([]*Canvas, error)

		// Get returns a single canvas by its ID, ensuring it belongs to the user.
		Get(ctx context.Context, userID, id string) (*Canvas, error)

		// Save creates or updates a canvas for a user.
		Save(ctx context.Context, canvas *Canvas) error

		// Delete removes a canvas, ensuring it belongs to the user.
		Delete(ctx context.Context, userID, id string) error
	}
)
