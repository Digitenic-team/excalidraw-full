package kv

import (
	"context"
	"encoding/json"
	"excalidraw-complete/handlers/auth"
	"excalidraw-complete/middleware"
	"excalidraw-complete/stores"
	"excalidraw-complete/stores/memory"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
)

const testUserID = "user-123"

// newAuthedRequest builds a request carrying the given chi URL params and an
// authenticated claims context for testUserID.
func newAuthedRequest(method, target, body string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))

	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)

	claims := &auth.AppClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: testUserID}}
	ctx = context.WithValue(ctx, middleware.ClaimsContextKey, claims)

	return req.WithContext(ctx)
}

func saveCanvas(t *testing.T, store stores.Store, key, body string) {
	t.Helper()
	req := newAuthedRequest(http.MethodPut, "/api/v2/kv/"+key, body, map[string]string{"key": key})
	rr := httptest.NewRecorder()
	HandleSaveCanvas(store)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("save canvas: expected 200, got %d (%s)", rr.Code, rr.Body.String())
	}
}

func publishCanvas(t *testing.T, store stores.Store, key string) string {
	t.Helper()
	req := newAuthedRequest(http.MethodPost, "/api/v2/kv/"+key+"/publish", "", map[string]string{"key": key})
	rr := httptest.NewRecorder()
	HandlePublishCanvas(store)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("publish canvas: expected 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "shareId") {
		t.Fatalf("publish canvas: response missing shareId: %s", rr.Body.String())
	}
	// crude extraction of the shareId value
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("publish canvas: decode response: %v", err)
	}
	if resp["shareId"] == "" {
		t.Fatal("publish canvas: empty shareId")
	}
	return resp["shareId"]
}

func getPublic(t *testing.T, store stores.Store, shareID string) (int, string) {
	t.Helper()
	req := newAuthedRequest(http.MethodGet, "/api/v2/public/"+shareID, "", map[string]string{"shareId": shareID})
	rr := httptest.NewRecorder()
	HandleGetPublicCanvas(store)(rr, req)
	return rr.Code, rr.Body.String()
}

func TestPublicSharingFlow(t *testing.T) {
	store := memory.NewStore()
	key := "canvas-1"
	bodyV1 := `{"appState":{"name":"My Canvas"},"elements":[{"id":"a"}]}`

	// 1. create + 2. publish
	saveCanvas(t, store, key, bodyV1)
	shareID := publishCanvas(t, store, key)

	// 3. public GET returns the snapshot
	code, got := getPublic(t, store, shareID)
	if code != http.StatusOK {
		t.Fatalf("public GET: expected 200, got %d", code)
	}
	if got != bodyV1 {
		t.Fatalf("public GET: content mismatch.\n got: %s\nwant: %s", got, bodyV1)
	}

	// bogus share id 404s
	if code, _ := getPublic(t, store, "does-not-exist"); code != http.StatusNotFound {
		t.Fatalf("bogus public GET: expected 404, got %d", code)
	}

	// 4. autosave updates the public snapshot (auto-update) ...
	bodyV2 := `{"appState":{"name":"My Canvas"},"elements":[{"id":"a"},{"id":"b"}]}`
	saveCanvas(t, store, key, bodyV2)
	if code, got := getPublic(t, store, shareID); code != http.StatusOK || got != bodyV2 {
		t.Fatalf("public GET after edit: expected 200 with updated content, got %d / %s", code, got)
	}

	// ... and does NOT clear Public/ShareID
	canvas, err := store.Get(context.Background(), testUserID, key)
	if err != nil {
		t.Fatalf("get canvas: %v", err)
	}
	if !canvas.Public || canvas.ShareID != shareID {
		t.Fatalf("autosave wiped share state: public=%v shareID=%q (want true / %q)", canvas.Public, canvas.ShareID, shareID)
	}

	// 5. unpublish -> public link 404s, shareID retained
	req := newAuthedRequest(http.MethodDelete, "/api/v2/kv/"+key+"/publish", "", map[string]string{"key": key})
	rr := httptest.NewRecorder()
	HandleUnpublishCanvas(store)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unpublish: expected 200, got %d", rr.Code)
	}
	if code, _ := getPublic(t, store, shareID); code != http.StatusNotFound {
		t.Fatalf("public GET after unpublish: expected 404, got %d", code)
	}
	canvas, _ = store.Get(context.Background(), testUserID, key)
	if canvas.Public {
		t.Fatal("canvas still public after unpublish")
	}
	if canvas.ShareID != shareID {
		t.Fatalf("shareID not retained after unpublish: %q", canvas.ShareID)
	}

	// re-publish reuses the same share URL
	if reShareID := publishCanvas(t, store, key); reShareID != shareID {
		t.Fatalf("re-publish minted a new shareID: %q (want %q)", reShareID, shareID)
	}
	if code, got := getPublic(t, store, shareID); code != http.StatusOK || got != bodyV2 {
		t.Fatalf("public GET after re-publish: expected 200 with content, got %d / %s", code, got)
	}
}
