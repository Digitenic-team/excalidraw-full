package stores

import (
	"bytes"
	"context"
	"excalidraw-complete/core"
	"excalidraw-complete/stores/filesystem"
	"excalidraw-complete/stores/memory"
	"testing"
)

// stores under test must satisfy the union Store interface (DocumentStore +
// CanvasStore), including the new SaveDocument upsert.
func storesUnderTest(t *testing.T) map[string]Store {
	t.Helper()
	return map[string]Store{
		"memory":     memory.NewStore(),
		"filesystem": filesystem.NewStore(t.TempDir()),
	}
}

func TestSaveDocumentUpsert(t *testing.T) {
	ctx := context.Background()
	for name, store := range storesUnderTest(t) {
		t.Run(name, func(t *testing.T) {
			id := "share-token-abc"

			// upsert v1
			if err := store.SaveDocument(ctx, id, &core.Document{Data: *bytes.NewBufferString("v1")}); err != nil {
				t.Fatalf("SaveDocument v1: %v", err)
			}
			doc, err := store.FindID(ctx, id)
			if err != nil {
				t.Fatalf("FindID after v1: %v", err)
			}
			if doc.Data.String() != "v1" {
				t.Fatalf("FindID v1: got %q", doc.Data.String())
			}

			// upsert v2 over the same id
			if err := store.SaveDocument(ctx, id, &core.Document{Data: *bytes.NewBufferString("v2")}); err != nil {
				t.Fatalf("SaveDocument v2: %v", err)
			}
			doc, err = store.FindID(ctx, id)
			if err != nil {
				t.Fatalf("FindID after v2: %v", err)
			}
			if doc.Data.String() != "v2" {
				t.Fatalf("FindID v2: got %q (upsert did not overwrite)", doc.Data.String())
			}
		})
	}
}

func TestCanvasShareFieldsPersist(t *testing.T) {
	ctx := context.Background()
	for name, store := range storesUnderTest(t) {
		t.Run(name, func(t *testing.T) {
			canvas := &core.Canvas{
				ID:      "c1",
				UserID:  "u1",
				Name:    "Test",
				Data:    []byte(`{"hello":"world"}`),
				ShareID: "stable-token",
				Public:  true,
			}
			if err := store.Save(ctx, canvas); err != nil {
				t.Fatalf("Save canvas: %v", err)
			}
			got, err := store.Get(ctx, "u1", "c1")
			if err != nil {
				t.Fatalf("Get canvas: %v", err)
			}
			if got.ShareID != "stable-token" || !got.Public {
				t.Fatalf("share fields not persisted: shareID=%q public=%v", got.ShareID, got.Public)
			}
		})
	}
}
