package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"excalidraw-complete/core"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/sirupsen/logrus"
	_ "modernc.org/sqlite"
)

type sqliteStore struct {
	db *sql.DB
}

// NewStore creates a new SQLite-based store.
func NewStore(dataSourceName string) *sqliteStore {
	db, err := sql.Open("sqlite", dataSourceName)
	if err != nil {
		log.Fatalf("failed to open sqlite database: %v", err)
	}

	// Initialize table for anonymous documents
	docTableStmt := `CREATE TABLE IF NOT EXISTS documents (id TEXT PRIMARY KEY, data BLOB);`
	if _, err = db.Exec(docTableStmt); err != nil {
		log.Fatalf("failed to create documents table: %v", err)
	}

	// Initialize table for user-owned canvases
	canvasTableStmt := `
	CREATE TABLE IF NOT EXISTS canvases (
		id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		name TEXT,
		thumbnail TEXT,
		data BLOB,
		share_id TEXT,
		public INTEGER,
		created_at DATETIME,
		updated_at DATETIME,
		PRIMARY KEY (user_id, id)
	);`
	if _, err = db.Exec(canvasTableStmt); err != nil {
		log.Fatalf("failed to create canvases table: %v", err)
	}

	// Idempotently add share columns so pre-existing databases upgrade in place.
	// SQLite returns a "duplicate column name" error if the column already exists;
	// that case is expected and safe to ignore.
	for _, alter := range []string{
		"ALTER TABLE canvases ADD COLUMN share_id TEXT",
		"ALTER TABLE canvases ADD COLUMN public INTEGER",
	} {
		if _, err = db.Exec(alter); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			log.Fatalf("failed to migrate canvases table: %v", err)
		}
	}

	return &sqliteStore{db}
}

// DocumentStore implementation
func (s *sqliteStore) FindID(ctx context.Context, id string) (*core.Document, error) {
	log := logrus.WithField("document_id", id)
	log.Debug("Retrieving document by ID")
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT data FROM documents WHERE id = ?", id).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
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

func (s *sqliteStore) Create(ctx context.Context, document *core.Document) (string, error) {
	id := ulid.Make().String()
	data := document.Data.Bytes()
	log := logrus.WithFields(logrus.Fields{
		"document_id": id,
		"data_length": len(data),
	})

	_, err := s.db.ExecContext(ctx, "INSERT INTO documents (id, data) VALUES (?, ?)", id, data)
	if err != nil {
		log.WithError(err).Error("Failed to create document")
		return "", err
	}
	log.Info("Document created successfully")
	return id, nil
}

// SaveDocument upserts a document under a caller-supplied id. Part of the DocumentStore interface.
func (s *sqliteStore) SaveDocument(ctx context.Context, id string, document *core.Document) error {
	data := document.Data.Bytes()
	log := logrus.WithFields(logrus.Fields{
		"document_id": id,
		"data_length": len(data),
	})

	_, err := s.db.ExecContext(ctx, "INSERT OR REPLACE INTO documents (id, data) VALUES (?, ?)", id, data)
	if err != nil {
		log.WithError(err).Error("Failed to save document")
		return err
	}
	log.Info("Document saved successfully")
	return nil
}

// CanvasStore implementation
func (s *sqliteStore) List(ctx context.Context, userID string) ([]*core.Canvas, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, updated_at, thumbnail, share_id, public FROM canvases WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var canvases []*core.Canvas
	for rows.Next() {
		var canvas core.Canvas
		canvas.UserID = userID
		var shareID sql.NullString
		var public sql.NullBool
		if err := rows.Scan(&canvas.ID, &canvas.Name, &canvas.UpdatedAt, &canvas.Thumbnail, &shareID, &public); err != nil {
			return nil, err
		}
		canvas.ShareID = shareID.String
		canvas.Public = public.Bool
		canvases = append(canvases, &canvas)
	}
	return canvases, nil
}

func (s *sqliteStore) Get(ctx context.Context, userID, id string) (*core.Canvas, error) {
	var canvas core.Canvas
	canvas.UserID = userID
	canvas.ID = id
	var shareID sql.NullString
	var public sql.NullBool
	err := s.db.QueryRowContext(ctx, "SELECT name, data, created_at, updated_at, thumbnail, share_id, public FROM canvases WHERE user_id = ? AND id = ?", userID, id).Scan(&canvas.Name, &canvas.Data, &canvas.CreatedAt, &canvas.UpdatedAt, &canvas.Thumbnail, &shareID, &public)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("canvas not found")
		}
		return nil, err
	}
	canvas.ShareID = shareID.String
	canvas.Public = public.Bool
	return &canvas, nil
}

func (s *sqliteStore) Save(ctx context.Context, canvas *core.Canvas) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Rollback on any error

	var exists bool
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM canvases WHERE user_id = ? AND id = ?", canvas.UserID, canvas.ID).Scan(&exists)

	now := time.Now()
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	if exists {
		// Update
		_, err = tx.ExecContext(ctx, "UPDATE canvases SET name = ?, data = ?, updated_at = ?, thumbnail = ?, share_id = ?, public = ? WHERE user_id = ? AND id = ?", canvas.Name, canvas.Data, now, canvas.Thumbnail, canvas.ShareID, canvas.Public, canvas.UserID, canvas.ID)
	} else {
		// Insert
		_, err = tx.ExecContext(ctx, "INSERT INTO canvases (id, user_id, name, data, created_at, updated_at, thumbnail, share_id, public) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", canvas.ID, canvas.UserID, canvas.Name, canvas.Data, now, now, canvas.Thumbnail, canvas.ShareID, canvas.Public)
	}

	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *sqliteStore) Delete(ctx context.Context, userID, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM canvases WHERE user_id = ? AND id = ?", userID, id)
	return err
}
