//go:build verify

// Integration cover for the access rules, the per-device receipt cap and
// deletion — the three things that are not provable from a unit test because
// they are about what the database ends up holding.
//
// Needs a real Postgres and is excluded from the normal build, so it never
// runs in CI:
//
//	createdb splitzies_verify
//	DATABASE_URL=postgres://localhost:5432/splitzies_verify \
//	APP_BASE_URL=https://splitzi.co \
//	  go test -tags verify ./transport/ -run TestVerify -v
package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"splitzies/persistence"
)

func newTestTransport(t *testing.T) (*Transport, *persistence.Client, *http.ServeMux) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := persistence.NewClient(ctx, url)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := db.RunMigrations(ctx, "../migrations"); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	tr := NewTransport(slog.New(slog.NewTextHandler(io.Discard, nil)), db, nil, nil, "")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /receipts/image", tr.UploadReceiptImageHandler)
	mux.HandleFunc("GET /receipts/{receipt_id}", tr.GetReceiptHandler)
	mux.HandleFunc("PATCH /receipts/{receipt_id}", tr.PatchReceiptHandler)
	mux.HandleFunc("POST /receipts/{receipt_id}/users", tr.AddUserToReceiptHandler)
	mux.HandleFunc("POST /receipts/{receipt_id}/users/{user_id}/items", tr.AssignItemsToUserHandler)
	mux.HandleFunc("POST /receipts/{receipt_id}/share", tr.CreateShareLinkHandler)
	mux.HandleFunc("DELETE /receipts/{receipt_id}/share", tr.RevokeShareLinkHandler)
	mux.HandleFunc("POST /join/{token}", tr.JoinShareLinkHandler)
	mux.HandleFunc("DELETE /me/receipts", tr.DeleteAllMyReceiptsHandler)
	mux.HandleFunc("DELETE /me/receipts/{receipt_id}", tr.DeleteMyReceiptHandler)
	mux.HandleFunc("GET /me/receipts", tr.ListMyReceiptsHandler)

	return tr, db, mux
}

type client struct {
	mux     http.Handler
	tr      *Transport
	token   string
	devices string
}

func (c client) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		r.Header.Set(DeviceTokenHeader, c.token)
	}
	w := httptest.NewRecorder()
	// DeviceMiddleware is part of the chain in main.go.
	c.tr.DeviceMiddleware(c.mux).ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func seedReceipt(t *testing.T, db *persistence.Client, deviceID string) string {
	t.Helper()
	items := []persistence.ReceiptItemDB{{Name: "Margarita", Quantity: 1, TotalPrice: 12}}
	r, err := db.SaveReceipt(context.Background(), items, nil, nil, nil, nil, nil, nil, nil, &deviceID)
	if err != nil {
		t.Fatalf("SaveReceipt: %v", err)
	}
	return r.ID
}

func newDevice(t *testing.T, db *persistence.Client) (string, string) {
	t.Helper()
	d, token, err := db.CreateDevice(context.Background())
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	return d.ID, token
}

func TestVerifyAuthorization(t *testing.T) {
	tr, db, mux := newTestTransport(t)

	ownerID, ownerToken := newDevice(t, db)
	_, strangerToken := newDevice(t, db)
	receiptID := seedReceipt(t, db, ownerID)

	owner := client{mux: mux, tr: tr, token: ownerToken}
	stranger := client{mux: mux, tr: tr, token: strangerToken}
	anon := client{mux: mux, tr: tr}

	// Reading stays open: that is how a share link works.
	if code, _ := anon.do(t, "GET", "/receipts/"+receiptID, ""); code != 200 {
		t.Errorf("anonymous GET receipt = %d, want 200", code)
	}

	// The owner can change the bill.
	if code, body := owner.do(t, "PATCH", "/receipts/"+receiptID, `{"tax":2.50}`); code != 200 {
		t.Errorf("owner PATCH = %d, want 200 (%s)", code, body)
	}

	// A device that has never seen the bill cannot, even knowing the ID.
	for _, tc := range []struct{ method, path, body string }{
		{"PATCH", "/receipts/" + receiptID, `{"tax":99}`},
		{"POST", "/receipts/" + receiptID + "/users", `{"name":"Mallory"}`},
		{"POST", "/receipts/" + receiptID + "/share", ""},
		{"DELETE", "/receipts/" + receiptID + "/share", ""},
	} {
		code, body := stranger.do(t, tc.method, tc.path, tc.body)
		if code != 403 {
			t.Errorf("stranger %s %s = %d, want 403 (%s)", tc.method, tc.path, code, body)
		}
	}

	// And neither can a caller with no device token at all.
	if code, _ := anon.do(t, "PATCH", "/receipts/"+receiptID, `{"tax":99}`); code != 401 {
		t.Errorf("anonymous PATCH = %d, want 401", code)
	}

	// The share link is the way in: after joining, the same stranger can edit.
	code, body := owner.do(t, "POST", "/receipts/"+receiptID+"/share", "")
	if code != 201 {
		t.Fatalf("create share link = %d (%s)", code, body)
	}
	var share struct{ Token string }
	if err := json.Unmarshal([]byte(body), &share); err != nil {
		t.Fatalf("share body: %v", err)
	}
	if code, body := stranger.do(t, "POST", "/join/"+share.Token, ""); code != 200 {
		t.Fatalf("join = %d (%s)", code, body)
	}
	if code, body := stranger.do(t, "PATCH", "/receipts/"+receiptID, `{"tax":3}`); code != 200 {
		t.Errorf("joined stranger PATCH = %d, want 200 (%s)", code, body)
	}

	// A participant from another bill can't be driven through this one.
	otherOwnerID, otherOwnerToken := newDevice(t, db)
	otherReceipt := seedReceipt(t, db, otherOwnerID)
	otherOwner := client{mux: mux, tr: tr, token: otherOwnerToken}
	code, body = otherOwner.do(t, "POST", "/receipts/"+otherReceipt+"/users", `{"name":"Bea"}`)
	if code != 201 {
		t.Fatalf("add user to other receipt = %d (%s)", code, body)
	}
	var added struct {
		User struct{ ID string }
	}
	if err := json.Unmarshal([]byte(body), &added); err != nil {
		t.Fatalf("add user body: %v", err)
	}
	if code, body := owner.do(t, "POST", "/receipts/"+receiptID+"/users/"+added.User.ID+"/items", `{"item_ids":["x"]}`); code != 404 {
		t.Errorf("cross-receipt participant = %d, want 404 (%s)", code, body)
	}
}

func TestVerifyReceiptCap(t *testing.T) {
	tr, db, mux := newTestTransport(t)

	deviceID, token := newDevice(t, db)
	c := client{mux: mux, tr: tr, token: token}

	for i := 0; i < maxReceiptsPerDevice; i++ {
		seedReceipt(t, db, deviceID)
	}

	// At the cap, the upload is refused before any paid parsing happens.
	code, body := c.do(t, "POST", "/receipts/image", "")
	if code != 409 || !strings.Contains(body, "receipt_limit_reached") {
		t.Errorf("upload at cap = %d (%s), want 409 receipt_limit_reached", code, body)
	}

	// Deleting one frees a slot: the next upload gets past the cap and fails
	// later, on the missing image, rather than on the limit.
	var list struct {
		Receipts []struct {
			ReceiptID string `json:"receipt_id"`
		} `json:"receipts"`
	}
	_, body = c.do(t, "GET", "/me/receipts", "")
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("history body: %v", err)
	}
	if code, body := c.do(t, "DELETE", "/me/receipts/"+list.Receipts[0].ReceiptID, ""); code != 200 {
		t.Fatalf("delete one = %d (%s)", code, body)
	}
	code, body = c.do(t, "POST", "/receipts/image", "")
	if code == 409 {
		t.Errorf("upload after freeing a slot still = 409 (%s)", body)
	}

	// An upload with no device token at all is refused outright.
	anon := client{mux: mux, tr: tr}
	if code, _ := anon.do(t, "POST", "/receipts/image", ""); code != 401 {
		t.Errorf("anonymous upload = %d, want 401", code)
	}
}

func TestVerifyDeletion(t *testing.T) {
	tr, db, mux := newTestTransport(t)
	ctx := context.Background()

	// A bill only this device holds is destroyed, not just unlinked.
	soloID, soloToken := newDevice(t, db)
	solo := client{mux: mux, tr: tr, token: soloToken}
	receiptID := seedReceipt(t, db, soloID)

	if code, body := solo.do(t, "DELETE", "/me/receipts/"+receiptID, ""); code != 200 {
		t.Fatalf("delete solo = %d (%s)", code, body)
	}
	if exists, err := db.ReceiptExists(ctx, receiptID); err != nil || exists {
		t.Errorf("solo receipt still exists (exists=%v err=%v) — delete only unlinked it", exists, err)
	}

	// A shared bill survives one participant deleting it, then dies with the last.
	aID, aToken := newDevice(t, db)
	_, bToken := newDevice(t, db)
	a := client{mux: mux, tr: tr, token: aToken}
	b := client{mux: mux, tr: tr, token: bToken}
	sharedID := seedReceipt(t, db, aID)

	_, body := a.do(t, "POST", "/receipts/"+sharedID+"/share", "")
	var share struct{ Token string }
	if err := json.Unmarshal([]byte(body), &share); err != nil {
		t.Fatalf("share body: %v", err)
	}
	if code, body := b.do(t, "POST", "/join/"+share.Token, ""); code != 200 {
		t.Fatalf("join = %d (%s)", code, body)
	}

	if code, body := a.do(t, "DELETE", "/me/receipts/"+sharedID, ""); code != 200 {
		t.Fatalf("A delete shared = %d (%s)", code, body)
	}
	if exists, err := db.ReceiptExists(ctx, sharedID); err != nil || !exists {
		t.Errorf("shared receipt destroyed while B still held it (exists=%v err=%v)", exists, err)
	}
	if code, body := b.do(t, "DELETE", "/me/receipts/"+sharedID, ""); code != 200 {
		t.Fatalf("B delete shared = %d (%s)", code, body)
	}
	if exists, err := db.ReceiptExists(ctx, sharedID); err != nil || exists {
		t.Errorf("shared receipt survived the last holder deleting it (exists=%v err=%v)", exists, err)
	}

	// Delete-everything empties the history and destroys what nobody else has.
	allID, allToken := newDevice(t, db)
	all := client{mux: mux, tr: tr, token: allToken}
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, seedReceipt(t, db, allID))
	}
	code, body := all.do(t, "DELETE", "/me/receipts", "")
	if code != 200 {
		t.Fatalf("delete all = %d (%s)", code, body)
	}
	if !strings.Contains(body, `"receipts_deleted":3`) {
		t.Errorf("delete all body = %s, want receipts_deleted 3", body)
	}
	for _, id := range ids {
		if exists, err := db.ReceiptExists(ctx, id); err != nil || exists {
			t.Errorf("receipt %s survived delete-all (exists=%v err=%v)", id, exists, err)
		}
	}
	_, body = all.do(t, "GET", "/me/receipts", "")
	if !strings.Contains(body, `"receipts":[]`) {
		t.Errorf("history after delete-all = %s, want empty", body)
	}
}
