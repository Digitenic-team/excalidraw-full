package memory

import (
	"context"
	"excalidraw-complete/core"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/sirupsen/logrus"
)

var (
	savedDocuments = make(map[string]core.Document)
	// savedCanvases is a map where the key is userID, and the value is another map
	// where the key is canvasID and the value is the canvas itself.
	savedCanvases = make(map[string]map[string]*core.Canvas)
	mu            sync.RWMutex
)

// memStore implements both DocumentStore and CanvasStore for in-memory storage.
type memStore struct{}

// NewStore creates a new in-memory store.
func NewStore() *memStore {
	return &memStore{}
}

// FindID retrieves a document by its ID. Part of the DocumentStore interface.
func (s *memStore) FindID(ctx context.Context, id string) (*core.Document, error) {
	mu.RLock()
	defer mu.RUnlock()

	log := logrus.WithField("document_id", id)
	if val, ok := savedDocuments[id]; ok {
		log.Info("Document retrieved successfully")
		return &val, nil
	}
	log.WithField("error", "document not found").Warn("Document with specified ID not found")
	return nil, fmt.Errorf("document with id %s not found", id)
}

// Create stores a new document. Part of the DocumentStore interface.
func (s *memStore) Create(ctx context.Context, document *core.Document) (string, error) {
	mu.Lock()
	defer mu.Unlock()

	id := ulid.Make().String()
	savedDocuments[id] = *document
	log := logrus.WithFields(logrus.Fields{
		"document_id": id,
		"data_length": len(document.Data.Bytes()),
	})
	log.Info("Document created successfully")

	return id, nil
}

// SaveDocument upserts a document under a caller-supplied id. Part of the DocumentStore interface.
func (s *memStore) SaveDocument(ctx context.Context, id string, document *core.Document) error {
	mu.Lock()
	defer mu.Unlock()

	savedDocuments[id] = *document
	logrus.WithFields(logrus.Fields{
		"document_id": id,
		"data_length": len(document.Data.Bytes()),
	}).Info("Document saved successfully")
	return nil
}

// List returns metadata for all canvases owned by a user. Part of the CanvasStore interface.
func (s *memStore) List(ctx context.Context, userID string) ([]*core.Canvas, error) {
	mu.RLock()
	defer mu.RUnlock()

	userCanvases, ok := savedCanvases[userID]
	if !ok {
		return []*core.Canvas{}, nil // No canvases for this user, return empty slice
	}

	canvases := make([]*core.Canvas, 0, len(userCanvases))
	for _, canvas := range userCanvases {
		// Important: create a copy without the large `Data` field for the list view
		listCanvas := &core.Canvas{
			ID:        canvas.ID,
			UserID:    canvas.UserID,
			Name:      canvas.Name,
			Thumbnail: canvas.Thumbnail,
			CreatedAt: canvas.CreatedAt,
			UpdatedAt: canvas.UpdatedAt,
		}
		canvases = append(canvases, listCanvas)
	}

	logrus.WithField("user_id", userID).Infof("Listed %d canvases", len(canvases))
	return canvases, nil
}

// Get returns a single canvas by its ID, ensuring it belongs to the user. Part of the CanvasStore interface.
func (s *memStore) Get(ctx context.Context, userID, id string) (*core.Canvas, error) {
	mu.RLock()
	defer mu.RUnlock()

	log := logrus.WithFields(logrus.Fields{"user_id": userID, "canvas_id": id})

	userCanvases, ok := savedCanvases[userID]
	if !ok {
		log.Warn("User has no canvases")
		return nil, fmt.Errorf("canvas with id %s not found for user %s", id, userID)
	}

	canvas, ok := userCanvases[id]
	if !ok {
		log.Warn("Canvas not found for user")
		return nil, fmt.Errorf("canvas with id %s not found for user %s", id, userID)
	}

	log.Info("Canvas retrieved successfully")
	return canvas, nil
}

// Save creates or updates a canvas for a user. Part of the CanvasStore interface.
func (s *memStore) Save(ctx context.Context, canvas *core.Canvas) error {
	mu.Lock()
	defer mu.Unlock()

	log := logrus.WithFields(logrus.Fields{"user_id": canvas.UserID, "canvas_id": canvas.ID})

	if canvas.UserID == "" {
		return fmt.Errorf("UserID cannot be empty")
	}

	userCanvases, ok := savedCanvases[canvas.UserID]
	if !ok {
		userCanvases = make(map[string]*core.Canvas)
		savedCanvases[canvas.UserID] = userCanvases
	}

	if canvas.ID == "" {
		return fmt.Errorf("Canvas ID cannot be empty for save operation")
	}

	now := time.Now()
	if existingCanvas, exists := userCanvases[canvas.ID]; exists {
		canvas.CreatedAt = existingCanvas.CreatedAt
		canvas.UpdatedAt = now
	} else {
		canvas.CreatedAt = now
		canvas.UpdatedAt = now
	}

	userCanvases[canvas.ID] = canvas
	log.Info("Canvas saved successfully")
	return nil
}

// Delete removes a canvas, ensuring it belongs to the user. Part of the CanvasStore interface.
func (s *memStore) Delete(ctx context.Context, userID, id string) error {
	mu.Lock()
	defer mu.Unlock()

	log := logrus.WithFields(logrus.Fields{"user_id": userID, "canvas_id": id})

	userCanvases, ok := savedCanvases[userID]
	if !ok {
		log.Warn("User has no canvases to delete from")
		return fmt.Errorf("user %s has no canvases", userID)
	}

	if _, ok := userCanvases[id]; !ok {
		log.Warn("Canvas not found for deletion")
		return fmt.Errorf("canvas with id %s not found for user %s", id, userID)
	}

	delete(userCanvases, id)
	log.Info("Canvas deleted successfully")
	return nil
}
