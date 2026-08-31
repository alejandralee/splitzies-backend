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
			ImageURL:         s.ImageURL,
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

// DeleteMyReceiptHandler handles DELETE /me/receipts/{receipt_id}. It hides the
// bill from this device's history only — other participants keep it, since a
// shared bill isn't any one person's to delete.
func (t *Transport) DeleteMyReceiptHandler(w http.ResponseWriter, r *http.Request) {
	rid := requestID(r)
	receiptID := r.PathValue("receipt_id")

	deviceID, ok := t.requireDevice(w, r)
	if !ok {
		return
	}

	if err := t.persistenceClient.RemoveDeviceFromReceipt(r.Context(), receiptID, deviceID); err != nil {
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "receipt_not_found",
				"receipt "+receiptID+" is not in this device's history", rid)
			return
		}
		t.log.Error("failed to remove receipt from history", "request_id", rid, "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to remove receipt from history", rid)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"message": "receipt removed from history"}); err != nil {
		t.log.Error("failed to encode delete history response", "request_id", rid, "error", err)
	}
}
