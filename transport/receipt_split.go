package transport

import (
	"math"

	"splitzies/money"
	"splitzies/persistence"
)

// BillSplitResult holds the computed amounts for a bill split
type BillSplitResult struct {
	AmountByUserItem map[string]float64 // key: "userID:itemID"
	UserTotal        map[string]float64 // key: userID
}

// ComputeBillSplit calculates equal split amounts for each user-item assignment.
// Each user assigned to an item gets 1/n of the total, rounded to cents.
func ComputeBillSplit(items []persistence.ReceiptItem, assignments []persistence.ReceiptUserItem) BillSplitResult {
	itemPrice := make(map[string]float64)
	for _, item := range items {
		itemPrice[item.ID] = item.TotalPrice
	}

	itemUserOrder := make(map[string][]string)
	for _, a := range assignments {
		itemUserOrder[a.ReceiptItemID] = append(itemUserOrder[a.ReceiptItemID], a.ReceiptUserID)
	}

	amountByUserItem := make(map[string]float64)
	for itemID, userIDs := range itemUserOrder {
		totalPrice := itemPrice[itemID]
		n := len(userIDs)
		if n == 0 {
			continue
		}
		totalCents := int(math.Round(totalPrice * 100))
		baseCents := totalCents / n
		remainder := totalCents - baseCents*n
		for i, userID := range userIDs {
			cents := baseCents
			if i < remainder {
				cents++
			}
			key := userID + ":" + itemID
			amountByUserItem[key] = float64(cents) / 100
		}
	}

	userTotal := make(map[string]float64)
	for _, a := range assignments {
		key := a.ReceiptUserID + ":" + a.ReceiptItemID
		userTotal[a.ReceiptUserID] += amountByUserItem[key]
	}

	return BillSplitResult{
		AmountByUserItem: amountByUserItem,
		UserTotal:        userTotal,
	}
}

// ToGetReceiptResponse builds GetReceiptResponse from receipt data and bill split result
func ToGetReceiptResponse(
	receiptID string,
	users []persistence.ReceiptUser,
	items []persistence.ReceiptItem,
	assignments []persistence.ReceiptUserItem,
	split BillSplitResult,
	currency *string,
) GetReceiptResponse {
	responseUsers := make([]GetReceiptUserResponse, len(users))
	for i, u := range users {
		total := split.UserTotal[u.ID]
		amt := money.NewAmount(total, currency)
		responseUsers[i] = GetReceiptUserResponse{
			ID:        u.ID,
			ReceiptID: u.ReceiptID,
			Name:      u.Name,
			UserTotal: &amt,
		}
	}

	responseItems := make([]ReceiptItem, len(items))
	for i, item := range items {
		responseItems[i] = ReceiptItem{
			ID:           item.ID,
			Name:         item.Name,
			Quantity:     item.Quantity,
			TotalPrice:   money.Ptr(&item.TotalPrice, currency),
			PricePerItem: money.Ptr(&item.PricePerItem, currency),
		}
	}

	responseAssignments := make([]GetReceiptAssignmentResponse, len(assignments))
	for i, a := range assignments {
		key := a.ReceiptUserID + ":" + a.ReceiptItemID
		amt := money.NewAmount(split.AmountByUserItem[key], currency)
		responseAssignments[i] = GetReceiptAssignmentResponse{
			ID:         a.ID,
			UserID:     a.ReceiptUserID,
			ItemID:     a.ReceiptItemID,
			AmountOwed: amt,
		}
	}

	return GetReceiptResponse{
		ReceiptID:   receiptID,
		Users:       responseUsers,
		Items:       responseItems,
		Assignments: responseAssignments,
	}
}
