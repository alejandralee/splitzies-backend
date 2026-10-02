package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"splitzies/money"
	"splitzies/persistence"
)

// DeviceTokenHeader carries the anonymous device token minted by POST /devices.
const DeviceTokenHeader = "X-Device-Token"

// defaultHistoryLimit and maxHistoryLimit bound GET /me/receipts page sizes.
const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 100
)

type deviceContextKey struct{}

// DeviceMiddleware resolves the X-Device-Token header into a device ID on the
// request context. It is intentionally permissive: a request with a missing or
// unknown token proceeds anonymously, exactly as before devices existed, so the
// already-deployed frontend keeps working unchanged.
func (t *Transport) DeviceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get(DeviceTokenHeader)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		device, err := t.persistenceClient.DeviceByToken(r.Context(), token)
		if err != nil {
			if !errors.Is(err, persistence.ErrDeviceNotFound) {
				t.log.Error("failed to resolve device token", "error", err)
			}
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), deviceContextKey{}, device.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// deviceIDFromContext returns the calling device's ID, or nil when the request
// is anonymous.
func deviceIDFromContext(ctx context.Context) *string {
	id, ok := ctx.Value(deviceContextKey{}).(string)
	if !ok || id == "" {
		return nil
	}
	return &id
}

// requireDevice writes a 401 and returns false when the request carries no
// valid device token. Used by the endpoints that are meaningless without one.
func (t *Transport) requireDevice(w http.ResponseWriter, r *http.Request) (string, bool) {
	deviceID := deviceIDFromContext(r.Context())
	if deviceID == nil {
		writeJSONError(w, http.StatusUnauthorized, "device_required",
			"a valid "+DeviceTokenHeader+" header is required; create one with POST /devices", requestID(r))
		return "", false
	}
	return *deviceID, true
}

// requireReceiptMember resolves the calling device and checks it is on the
// receipt, writing the response and returning false when it isn't.
//
// Knowing a receipt ID is not authority to change the bill. IDs travel through
// chat threads, screenshots and browser history, and "stop sharing" has to
// actually revoke something — with no check here, anyone who ever saw a link
// could still retotal the bill or delete participants long after it was
// revoked. Membership comes from uploading the receipt or from opening a live
// share link, and every mutation goes through this.
func (t *Transport) requireReceiptMember(w http.ResponseWriter, r *http.Request, receiptID string) (string, bool) {
	deviceID, ok := t.requireDevice(w, r)
	if !ok {
		return "", false
	}

	member, err := t.persistenceClient.DeviceOnReceipt(r.Context(), receiptID, deviceID)
	if err != nil {
		t.log.Error("failed to check receipt membership",
			"request_id", requestID(r), "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to check access to this bill", requestID(r))
		return "", false
	}
	if !member {
		writeJSONError(w, http.StatusForbidden, "not_a_participant",
			"you don't have access to this bill — open its share link to join", requestID(r))
		return "", false
	}

	return deviceID, true
}

// requireReceiptParticipant is requireReceiptMember plus a check that the
// participant in the URL actually belongs to the receipt in the URL.
//
// The assignment endpoints take a receipt, a participant and an item, and the
// store only checked that the participant and item agreed with each other —
// not that either belonged to the receipt being addressed. Without this, being
// a member of any one bill was enough to operate on a participant from
// another.
func (t *Transport) requireReceiptParticipant(w http.ResponseWriter, r *http.Request, receiptID, userID string) bool {
	if _, ok := t.requireReceiptMember(w, r, receiptID); !ok {
		return false
	}

	exists, err := t.persistenceClient.ReceiptUserExists(r.Context(), receiptID, userID)
	if err != nil {
		t.log.Error("failed to verify receipt participant",
			"request_id", requestID(r), "receipt_id", receiptID, "user_id", userID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to verify participant", requestID(r))
		return false
	}
	if !exists {
		writeJSONError(w, http.StatusNotFound, "user_not_found",
			"participant "+userID+" is not on this bill", requestID(r))
		return false
	}

	return true
}

// CreateDeviceHandler handles POST /devices. It mints an anonymous identity so
// a user gets bill history without creating an account. The returned token is
// shown once and must be stored by the client.
func (t *Transport) CreateDeviceHandler(w http.ResponseWriter, r *http.Request) {
	rid := requestID(r)

	device, token, err := t.persistenceClient.CreateDevice(r.Context())
	if err != nil {
		t.log.Error("failed to create device", "request_id", rid, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "device_create_failed", "failed to create device", rid)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(CreateDeviceResponse{
		DeviceID:    device.ID,
		DeviceToken: token,
	}); err != nil {
		t.log.Error("failed to encode create device response", "request_id", rid, "error", err)
	}
}

// ListMyReceiptsHandler handles GET /me/receipts — the calling device's bill
// history, newest first, keyset-paginated on the cursor returned by the
// previous page.
func (t *Transport) ListMyReceiptsHandler(w http.ResponseWriter, r *http.Request) {
	rid := requestID(r)
	ctx := r.Context()

	deviceID, ok := t.requireDevice(w, r)
	if !ok {
		return
	}

	limit := defaultHistoryLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxHistoryLimit {
			writeJSONError(w, http.StatusBadRequest, "invalid_limit",
				newValidationError("limit", "limit must be between 1 and "+strconv.Itoa(maxHistoryLimit)).Error(), rid)
			return
		}
		limit = parsed
	}

	var cursor time.Time
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_cursor",
				newValidationError("cursor", "cursor must be an RFC3339 timestamp from a previous page").Error(), rid)
			return
		}
		cursor = parsed
	}

	summaries, err := t.persistenceClient.ListReceiptsForDevice(ctx, deviceID, limit, cursor)
	if err != nil {
		t.log.Error("failed to list device receipts", "request_id", rid, "device_id", deviceID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to list receipts", rid)
		return
	}

	response := ListMyReceiptsResponse{Receipts: make([]ReceiptSummary, 0, len(summaries))}
	for _, s := range summaries {
		currency := s.Currency
		if currency == nil {
			currency = &defaultUSD
		}

		summary := ReceiptSummary{
			ReceiptID:        s.ReceiptID,
			Title:            s.Title,
			ReceiptDate:      s.ReceiptDate,
			CreatedAt:        s.CreatedAt,
			UpdatedAt:        s.UpdatedAt,
			Role:             s.Role,
			ParticipantCount: s.ParticipantCount,
			ItemCount:        s.ItemCount,
			ItemTotal:        money.NewAmount(s.ItemTotal, currency),
			Tax:              money.Ptr(s.Tax, currency),
			Tip:              money.Ptr(s.Tip, currency),
		}

		// The device's own share of this bill, when it has claimed a participant.
		if total, err := t.deviceTotalOnReceipt(ctx, s.ReceiptID, deviceID, currency); err != nil {
			t.log.Error("failed to compute device total", "request_id", rid, "receipt_id", s.ReceiptID, "error", err)
		} else {
			summary.YourTotal = total
		}

		response.Receipts = append(response.Receipts, summary)
	}

	// Only advertise a cursor when a full page came back — a short page is the end.
	if len(summaries) == limit {
		next := summaries[len(summaries)-1].JoinedAt.Format(time.RFC3339Nano)
		response.NextCursor = &next
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.log.Error("failed to encode list receipts response", "request_id", rid, "error", err)
	}
}

// deviceTotalOnReceipt returns what the device's claimed participant owes on a
// receipt, or nil if the device hasn't claimed anyone. It reuses ComputeBillSplit
// so history totals and the receipt screen can never disagree.
func (t *Transport) deviceTotalOnReceipt(ctx context.Context, receiptID, deviceID string, currency *string) (*money.Amount, error) {
	user, err := t.persistenceClient.DeviceReceiptUser(ctx, receiptID, deviceID)
	if err != nil || user == nil {
		return nil, err
	}

	items, err := t.persistenceClient.GetReceiptItems(ctx, receiptID)
	if err != nil {
		return nil, err
	}
	assignments, err := t.persistenceClient.GetReceiptAssignments(ctx, receiptID)
	if err != nil {
		return nil, err
	}

	split := ComputeBillSplit(items, assignments)
	amount := money.NewAmount(split.UserTotal[user.ID], currency)
	return &amount, nil
}

// DeleteMyReceiptHandler handles DELETE /me/receipts/{receipt_id}. It drops the
// bill from this device's history and, when no other device is holding it,
// deletes the bill and everything on it outright. A bill someone else still
// has stays theirs — a shared bill isn't any one person's to destroy.
func (t *Transport) DeleteMyReceiptHandler(w http.ResponseWriter, r *http.Request) {
	rid := requestID(r)
	receiptID := r.PathValue("receipt_id")

	deviceID, ok := t.requireDevice(w, r)
	if !ok {
		return
	}

	if err := t.persistenceClient.DeleteReceiptForDevice(r.Context(), receiptID, deviceID); err != nil {
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "receipt_not_found",
				"receipt "+receiptID+" is not in this device's history", rid)
			return
		}
		t.log.Error("failed to delete receipt from history", "request_id", rid, "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to delete receipt", rid)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"message": "receipt deleted"}); err != nil {
		t.log.Error("failed to encode delete history response", "request_id", rid, "error", err)
	}
}

// DeleteAllMyReceiptsHandler handles DELETE /me/receipts — the "delete
// everything" the app offers in place of an account settings page. Every bill
// in this device's history goes, and each one nobody else is holding is
// deleted along with its items, participants, assignments, share link and
// stored receipt text.
//
// The device identity itself is kept so the app keeps working without minting
// a new one; there is nothing left attached to it.
func (t *Transport) DeleteAllMyReceiptsHandler(w http.ResponseWriter, r *http.Request) {
	rid := requestID(r)

	deviceID, ok := t.requireDevice(w, r)
	if !ok {
		return
	}

	removed, err := t.persistenceClient.DeleteAllReceiptsForDevice(r.Context(), deviceID)
	if err != nil {
		t.log.Error("failed to delete device history", "request_id", rid, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to delete history", rid)
		return
	}

	t.log.Info("deleted device history", "request_id", rid, "receipts_removed", removed)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(DeleteAllReceiptsResponse{
		Message:         "history deleted",
		ReceiptsDeleted: removed,
	}); err != nil {
		t.log.Error("failed to encode delete-all response", "request_id", rid, "error", err)
	}
}
