package filesystem

import (
	"bytes"
	"context"
	"encoding/json"
	"excalidraw-complete/core"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/sirupsen/logrus"
)

type fsStore struct {
	basePath string
}

// NewStore creates a new filesystem-based store.
func NewStore(basePath string) *fsStore {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		log.Fatalf("failed to create base directory: %v", err)
	}
	return &fsStore{basePath: basePath}
}

// DocumentStore implementation for anonymous sharing
func (s *fsStore) FindID(ctx context.Context, id string) (*core.Document, error) {
	filePath := filepath.Join(s.basePath, id)
	log := logrus.WithField("document_id", id)

	log.WithField("file_path", filePath).Info("Retrieving document by ID")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.WithField("error", "document not found").Warn("Document with specified ID not found")
			return nil, fmt.Errorf("document with id %s not found", id)
		}
		log.WithError(err).Error("Failed to retrieve document")
		return nil, err
	}

	document := core.Document{
		Data: *bytes.NewBuffer(data),
	}

	log.Info("Document retrieved successfully")
	return &document, nil
}

func (s *fsStore) Create(ctx context.Context, document *core.Document) (string, error) {
	id := ulid.Make().String()
	filePath := filepath.Join(s.basePath, id)
	log := logrus.WithFields(logrus.Fields{
		"document_id": id,
		"file_path":   filePath,
	})
	log.Info("Creating new document")

	if err := os.WriteFile(filePath, document.Data.Bytes(), 0644); err != nil {
		log.WithError(err).Error("Failed to create document")
		return "", err
	}

	log.Info("Document created successfully")
	return id, nil
}

// SaveDocument upserts a document under a caller-supplied id. Part of the DocumentStore interface.
func (s *fsStore) SaveDocument(ctx context.Context, id string, document *core.Document) error {
	filePath := filepath.Join(s.basePath, id)
	log := logrus.WithFields(logrus.Fields{
		"document_id": id,
		"file_path":   filePath,
	})

	if err := os.WriteFile(filePath, document.Data.Bytes(), 0644); err != nil {
		log.WithError(err).Error("Failed to save document")
		return err
	}

	log.Info("Document saved successfully")
	return nil
}

// CanvasStore implementation for user-owned canvases
func (s *fsStore) getUserCanvasPath(userID string) string {
	return filepath.Join(s.basePath, userID)
}

func (s *fsStore) List(ctx context.Context, userID string) ([]*core.Canvas, error) {
	userPath := s.getUserCanvasPath(userID)
	log := logrus.WithField("user_id", userID).WithField("path", userPath)

	files, err := os.ReadDir(userPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Info("User directory does not exist, returning empty list.")
			return []*core.Canvas{}, nil
		}
		log.WithError(err).Error("Failed to read user directory")
		return nil, err
	}

	canvases := make([]*core.Canvas, 0, len(files))
	for _, file := range files {
		if !file.IsDir() {
			filePath := filepath.Join(userPath, file.Name())
			fileInfo, err := file.Info()
			if err != nil {
				log.WithError(err).Warnf("Failed to get file info for %s, skipping", file.Name())
				continue
			}

			data, err := os.ReadFile(filePath)
			if err != nil {
				log.WithError(err).Warnf("Failed to read canvas file %s, skipping", file.Name())
				continue
			}

			var canvas core.Canvas
			if err := json.Unmarshal(data, &canvas); err != nil {
				log.WithError(err).Warnf("Failed to unmarshal canvas file %s, skipping", file.Name())
				continue
			}

			// For list view, we don't need the full data blob.
			// Also ensure we populate metadata from the filesystem.
			canvas.Data = nil
			canvas.UpdatedAt = fileInfo.ModTime()
			canvases = append(canvases, &canvas)
		}
	}

	log.Infof("Listed %d canvases", len(canvases))
	return canvases, nil
}

func (s *fsStore) Get(ctx context.Context, userID, id string) (*core.Canvas, error) {
	userPath := s.getUserCanvasPath(userID)
	filePath := filepath.Join(userPath, id)
	log := logrus.WithFields(logrus.Fields{"user_id": userID, "canvas_id": id, "path": filePath})

	// 关键修复：验证路径合法性
	absUserPath, err := filepath.Abs(userPath)
	if err != nil {
		return nil, err // or handle error appropriately
	}
	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, err // or handle error appropriately
	}

	if !strings.HasPrefix(absFilePath, absUserPath) {
		return nil, fmt.Errorf("invalid path: access denied")
	}
	// 修复结束

	data, err := os.ReadFile(absFilePath) // 使用清理过的路径
	if err != nil {
		if os.IsNotExist(err) {
			log.Warn("Canvas file not found")
			return nil, fmt.Errorf("canvas %s not found", id)
		}
		log.WithError(err).Error("Failed to read canvas file")
		return nil, err
	}

	info, err := os.Stat(filePath)
	if err != nil {
		log.WithError(err).Error("Failed to get file stats")
		return nil, err
	}

	var canvas core.Canvas
	if err := json.Unmarshal(data, &canvas); err != nil {
		log.WithError(err).Error("Failed to unmarshal canvas data")
		return nil, err
	}
	canvas.UpdatedAt = info.ModTime()

	log.Info("Canvas retrieved successfully")
	return &canvas, nil
}

func (s *fsStore) Save(ctx context.Context, canvas *core.Canvas) error {
	userPath := s.getUserCanvasPath(canvas.UserID)
	filePath := filepath.Join(userPath, canvas.ID)
	log := logrus.WithFields(logrus.Fields{"user_id": canvas.UserID, "canvas_id": canvas.ID, "path": filePath})

	if err := os.MkdirAll(userPath, 0755); err != nil {
		log.WithError(err).Error("Failed to create user directory")
		return err
	}

	// Set creation/update time before saving
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		canvas.CreatedAt = time.Now()
	} else if err == nil {
		canvas.CreatedAt = info.ModTime() // This is not ideal, but filesystem doesn't store creation time easily.
	}
	canvas.UpdatedAt = time.Now()

	log.Info("Saving canvas")
	data, err := json.Marshal(canvas)
	if err != nil {
		log.WithError(err).Error("Failed to marshal canvas for saving")
		return err
	}

	err = os.WriteFile(filePath, data, 0644)
	if err != nil {
		log.WithError(err).Error("Failed to write canvas file")
		return err
	}

	return nil
}

func (s *fsStore) Delete(ctx context.Context, userID, id string) error {
	userPath := s.getUserCanvasPath(userID)
	filePath := filepath.Join(userPath, id)
	log := logrus.WithFields(logrus.Fields{"user_id": userID, "canvas_id": id, "path": filePath})

	err := os.Remove(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Warn("Canvas file not found for deletion, considered successful.")
			return nil // If it doesn't exist, the goal is achieved.
		}
		log.WithError(err).Error("Failed to delete canvas file")
		return err
	}

	log.Info("Canvas deleted successfully")
	return nil
}
