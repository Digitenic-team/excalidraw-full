package kv

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"excalidraw-complete/core"
	"excalidraw-complete/handlers/auth"
	"excalidraw-complete/middleware"
	"excalidraw-complete/stores"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/sirupsen/logrus"
)

// generateShareID returns an unguessable, URL-safe public share token.
func generateShareID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func HandleListCanvases(store stores.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(middleware.ClaimsContextKey).(*auth.AppClaims)
		if !ok {
			render.Status(r, http.StatusUnauthorized)
			render.JSON(w, r, map[string]string{"error": "User claims not found"})
			return
		}

		canvases, err := store.List(r.Context(), claims.Subject)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error":  err,
				"userID": claims.Subject,
			}).Error("Failed to list canvases")
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, map[string]string{"error": "Failed to list canvases"})
			return
		}

		// If canvases is nil (e.g., user has no canvases), return an empty slice instead of null.
		if canvases == nil {
			canvases = []*core.Canvas{}
		}

		render.JSON(w, r, canvases)
	}
}

func HandleGetCanvas(store stores.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(middleware.ClaimsContextKey).(*auth.AppClaims)
		if !ok {
			render.Status(r, http.StatusUnauthorized)
			render.JSON(w, r, map[string]string{"error": "User claims not found"})
			return
		}

		key := chi.URLParam(r, "key")
		if key == "" {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, map[string]string{"error": "Canvas key is required"})
			return
		}

		canvas, err := store.Get(r.Context(), claims.Subject, key)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error":  err,
				"userID": claims.Subject,
				"key":    key,
			}).Warn("Failed to get canvas")
			// This could be a not found error or a real server error.
			// For simplicity, we'll return 404, but in a real app, you might want to distinguish.
			render.Status(r, http.StatusNotFound)
			render.JSON(w, r, map[string]string{"error": "Canvas not found"})
			return
		}

		// The canvas data is returned as raw bytes.
		w.Header().Set("Content-Type", "application/json")
		w.Write(canvas.Data)
	}
}

func HandleSaveCanvas(store stores.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(middleware.ClaimsContextKey).(*auth.AppClaims)
		if !ok {
			render.Status(r, http.StatusUnauthorized)
			render.JSON(w, r, map[string]string{"error": "User claims not found"})
			return
		}

		key := chi.URLParam(r, "key")
		if key == "" {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, map[string]string{"error": "Canvas key is required"})
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error": err,
				"key":   key,
			}).Error("Failed to read request body")
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, map[string]string{"error": "Failed to read request body"})
			return
		}
		defer r.Body.Close()

		// For simplicity, we use the key as the name. A more advanced implementation
		// might parse a name from the body or have a separate field.
		var canvasData struct {
			AppState struct {
				Name string `json:"name"`
			} `json:"appState"`
			Thumbnail string `json:"thumbnail"`
		}
		// We make a copy of the body because json.Unmarshal will consume the reader.
		bodyCopy := make([]byte, len(body))
		copy(bodyCopy, body)

		canvasName := key // Default to key
		var canvasThumbnail string
		if err := json.Unmarshal(bodyCopy, &canvasData); err == nil {
			if canvasData.AppState.Name != "" {
				canvasName = canvasData.AppState.Name
			}
			canvasThumbnail = canvasData.Thumbnail
		}

		canvas := &core.Canvas{
			ID:        key,
			UserID:    claims.Subject,
			Name:      canvasName,
			Thumbnail: canvasThumbnail,
			Data:      body,
		}

		// Preserve share state across autosaves. The frontend PUTs the full canvas
		// every few seconds and does not know about ShareID/Public, so we carry the
		// existing values over from the stored record to avoid wiping them.
		if existing, err := store.Get(r.Context(), claims.Subject, key); err == nil && existing != nil {
			canvas.ShareID = existing.ShareID
			canvas.Public = existing.Public
		}

		if err := store.Save(r.Context(), canvas); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":  err,
				"userID": claims.Subject,
				"key":    key,
			}).Error("Failed to save canvas")
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, map[string]string{"error": "Failed to save canvas"})
			return
		}

		// If the canvas is shared publicly, re-push the latest snapshot to the
		// public document store so the share link always reflects current content.
		if canvas.Public && canvas.ShareID != "" {
			if err := store.SaveDocument(r.Context(), canvas.ShareID, &core.Document{Data: *bytes.NewBuffer(body)}); err != nil {
				logrus.WithFields(logrus.Fields{
					"error":   err,
					"userID":  claims.Subject,
					"key":     key,
					"shareID": canvas.ShareID,
				}).Error("Failed to update public share snapshot")
			}
		}

		render.Status(r, http.StatusOK)
	}
}

// HandlePublishCanvas makes a canvas publicly viewable. It generates a stable
// share token on first publish (reused on subsequent publishes), marks the
// canvas public, and snapshots the current canvas data into the public
// DocumentStore keyed by the share token.
func HandlePublishCanvas(store stores.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(middleware.ClaimsContextKey).(*auth.AppClaims)
		if !ok {
			render.Status(r, http.StatusUnauthorized)
			render.JSON(w, r, map[string]string{"error": "User claims not found"})
			return
		}

		key := chi.URLParam(r, "key")
		if key == "" {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, map[string]string{"error": "Canvas key is required"})
			return
		}

		canvas, err := store.Get(r.Context(), claims.Subject, key)
		if err != nil {
			render.Status(r, http.StatusNotFound)
			render.JSON(w, r, map[string]string{"error": "Canvas not found"})
			return
		}

		if canvas.ShareID == "" {
			shareID, err := generateShareID()
			if err != nil {
				logrus.WithError(err).Error("Failed to generate share id")
				render.Status(r, http.StatusInternalServerError)
				render.JSON(w, r, map[string]string{"error": "Failed to generate share id"})
				return
			}
			canvas.ShareID = shareID
		}
		canvas.Public = true

		if err := store.Save(r.Context(), canvas); err != nil {
			logrus.WithError(err).Error("Failed to save canvas while publishing")
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, map[string]string{"error": "Failed to publish canvas"})
			return
		}

		if err := store.SaveDocument(r.Context(), canvas.ShareID, &core.Document{Data: *bytes.NewBuffer(canvas.Data)}); err != nil {
			logrus.WithError(err).Error("Failed to push public share snapshot")
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, map[string]string{"error": "Failed to publish canvas"})
			return
		}

		render.JSON(w, r, map[string]string{"shareId": canvas.ShareID})
	}
}

// HandleUnpublishCanvas stops public sharing. The share token is retained so a
// later re-publish reuses the same URL, while the public snapshot is deleted so
// the link 404s in the meantime.
func HandleUnpublishCanvas(store stores.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(middleware.ClaimsContextKey).(*auth.AppClaims)
		if !ok {
			render.Status(r, http.StatusUnauthorized)
			render.JSON(w, r, map[string]string{"error": "User claims not found"})
			return
		}

		key := chi.URLParam(r, "key")
		if key == "" {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, map[string]string{"error": "Canvas key is required"})
			return
		}

		canvas, err := store.Get(r.Context(), claims.Subject, key)
		if err != nil {
			render.Status(r, http.StatusNotFound)
			render.JSON(w, r, map[string]string{"error": "Canvas not found"})
			return
		}

		canvas.Public = false
		if err := store.Save(r.Context(), canvas); err != nil {
			logrus.WithError(err).Error("Failed to save canvas while unpublishing")
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, map[string]string{"error": "Failed to unpublish canvas"})
			return
		}

		// Replace the public snapshot with an empty document so the link no longer
		// serves content. (DocumentStore has no delete primitive; an empty blob is
		// served as 404 by HandleGetPublicCanvas.)
		if canvas.ShareID != "" {
			if err := store.SaveDocument(r.Context(), canvas.ShareID, &core.Document{}); err != nil {
				logrus.WithError(err).Warn("Failed to clear public share snapshot")
			}
		}

		render.Status(r, http.StatusOK)
	}
}

// HandleGetPublicCanvas serves a publicly shared canvas snapshot by its share
// token. It is unauthenticated: no claims or userID are involved.
func HandleGetPublicCanvas(store stores.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shareID := chi.URLParam(r, "shareId")
		if shareID == "" {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, map[string]string{"error": "Share id is required"})
			return
		}

		doc, err := store.FindID(r.Context(), shareID)
		if err != nil || doc == nil || doc.Data.Len() == 0 {
			render.Status(r, http.StatusNotFound)
			render.JSON(w, r, map[string]string{"error": "Shared canvas not found"})
			return
		}

		// Never cache: the public link must always reflect the latest snapshot,
		// otherwise a soft reload in the viewer can serve stale content.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Write(doc.Data.Bytes())
	}
}

func HandleDeleteCanvas(store stores.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(middleware.ClaimsContextKey).(*auth.AppClaims)
		if !ok {
			render.Status(r, http.StatusUnauthorized)
			render.JSON(w, r, map[string]string{"error": "User claims not found"})
			return
		}

		key := chi.URLParam(r, "key")
		if key == "" {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, map[string]string{"error": "Canvas key is required"})
			return
		}

		if err := store.Delete(r.Context(), claims.Subject, key); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":  err,
				"userID": claims.Subject,
				"key":    key,
			}).Error("Failed to delete canvas")
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, map[string]string{"error": "Failed to delete canvas"})
			return
		}

		render.Status(r, http.StatusOK)
	}
}
