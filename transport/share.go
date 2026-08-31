package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"splitzies/persistence"
)

// errNoAppBaseURL is returned when APP_BASE_URL is unset. There is deliberately
// no default: a share link built against a guessed host still scans, still
// returns 200 from whatever is parked there, and fails only in the friend's
// hands. Refusing to mint one makes a misconfigured deploy obvious here.
var errNoAppBaseURL = errors.New("APP_BASE_URL is not set")

// shareURL builds the link a friend opens (and the string the client renders as
// a QR code) from a share token.
func shareURL(token string) (string, error) {
	base := strings.TrimSuffix(strings.TrimSpace(os.Getenv("APP_BASE_URL")), "/")
	if base == "" {
		return "", errNoAppBaseURL
	}
	return base + "/join/" + token, nil
}

// CreateShareLinkHandler handles POST /receipts/{receipt_id}/share.
// Idempotent: repeat calls return the same active link, so re-opening the share
// sheet shows a stable QR code rather than invalidating the one already sent.
func (t *Transport) CreateShareLinkHandler(w http.ResponseWriter, r *http.Request) {
	rid := requestID(r)
	receiptID := r.PathValue("receipt_id")
	ctx := r.Context()

	link, err := t.persistenceClient.CreateOrGetShareLink(ctx, receiptID, deviceIDFromContext(ctx))
	if err != nil {
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "receipt_not_found", fmt.Sprintf("receipt %q not found", receiptID), rid)
			return
		}
		t.log.Error("failed to create share link", "request_id", rid, "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "share_link_failed", "failed to create share link", rid)
		return
	}

	url, err := shareURL(link.Token)
	if err != nil {
		t.log.Error("cannot build share link", "request_id", rid, "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "share_link_failed", "sharing is not configured on this server", rid)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(ShareLinkResponse{
		ReceiptID: link.ReceiptID,
		Token:     link.Token,
		URL:       url,
		CreatedAt: link.CreatedAt,
	}); err != nil {
		t.log.Error("failed to encode share link response", "request_id", rid, "error", err)
	}
}

// RevokeShareLinkHandler handles DELETE /receipts/{receipt_id}/share. Friends
// who already joined keep their access — only the link stops working.
func (t *Transport) RevokeShareLinkHandler(w http.ResponseWriter, r *http.Request) {
	rid := requestID(r)
	receiptID := r.PathValue("receipt_id")

	if err := t.persistenceClient.RevokeShareLink(r.Context(), receiptID); err != nil {
		if errors.Is(err, persistence.ErrShareLinkNotFound) {
			writeJSONError(w, http.StatusNotFound, "share_link_not_found",
				fmt.Sprintf("receipt %q has no active share link", receiptID), rid)
			return
		}
		t.log.Error("failed to revoke share link", "request_id", rid, "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "revoke_share_link_failed", "failed to revoke share link", rid)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"message": "share link revoked"}); err != nil {
		t.log.Error("failed to encode revoke share link response", "request_id", rid, "error", err)
	}
}

// JoinShareLinkHandler handles POST /join/{token}. It resolves a share link to
// its receipt and, when the caller has a device token, adds the bill to their
// history. A friend with no account can then edit the receipt through the
// normal receipt endpoints.
func (t *Transport) JoinShareLinkHandler(w http.ResponseWriter, r *http.Request) {
	rid := requestID(r)
	token := r.PathValue("token")
	ctx := r.Context()

	link, err := t.persistenceClient.ShareLinkByToken(ctx, token)
	if err != nil {
		if errors.Is(err, persistence.ErrShareLinkNotFound) {
			writeJSONError(w, http.StatusNotFound, "share_link_not_found",
				"this share link is invalid or has been revoked", rid)
			return
		}
		t.log.Error("failed to resolve share link", "request_id", rid, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to resolve share link", rid)
		return
	}

	response := JoinShareLinkResponse{ReceiptID: link.ReceiptID}

	// Joining without a device token still works — the bill just won't appear in
	// any history until the client mints one.
	if deviceID := deviceIDFromContext(ctx); deviceID != nil {
		if err := t.persistenceClient.AddDeviceToReceipt(ctx, link.ReceiptID, *deviceID, persistence.RoleMember); err != nil {
			t.log.Error("failed to add device to receipt", "request_id", rid, "receipt_id", link.ReceiptID, "error", err)
			writeJSONError(w, http.StatusInternalServerError, "join_failed", "failed to join receipt", rid)
			return
		}
		response.Joined = true

		if user, err := t.persistenceClient.DeviceReceiptUser(ctx, link.ReceiptID, *deviceID); err != nil {
			t.log.Error("failed to look up claimed participant", "request_id", rid, "receipt_id", link.ReceiptID, "error", err)
		} else if user != nil {
			response.YourUserID = &user.ID
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.log.Error("failed to encode join response", "request_id", rid, "error", err)
	}
}

// ClaimUserHandler handles POST /receipts/{receipt_id}/users/{user_id}/claim.
// It links an existing participant to the calling device, for the case where
// the bill's owner typed everyone's names in before sharing the link.
func (t *Transport) ClaimUserHandler(w http.ResponseWriter, r *http.Request) {
	rid := requestID(r)
	receiptID := r.PathValue("receipt_id")
	userID := r.PathValue("user_id")
	ctx := r.Context()

	deviceID, ok := t.requireDevice(w, r)
	if !ok {
		return
	}

	if err := t.persistenceClient.ClaimReceiptUser(ctx, receiptID, userID, deviceID); err != nil {
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "user_not_found", err.Error(), rid)
			return
		}
		if strings.Contains(err.Error(), "already claims") {
			writeJSONError(w, http.StatusConflict, "already_claimed", err.Error(), rid)
			return
		}
		t.log.Error("failed to claim participant", "request_id", rid, "receipt_id", receiptID, "user_id", userID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "claim_failed", "failed to claim participant", rid)
		return
	}

	// Make sure the device sees the bill in its history even if it landed here
	// without going through the join endpoint.
	if err := t.persistenceClient.AddDeviceToReceipt(ctx, receiptID, deviceID, persistence.RoleMember); err != nil {
		t.log.Error("failed to add device to receipt on claim", "request_id", rid, "receipt_id", receiptID, "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"message": "participant claimed",
		"user_id": userID,
	}); err != nil {
		t.log.Error("failed to encode claim response", "request_id", rid, "error", err)
	}
}
