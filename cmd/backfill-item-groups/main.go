// Command backfill-item-groups is a one-off tool that explodes legacy
// receipt_items rows (quantity/total_price/price_per_item) into
// receipt_item_groups + per-unit receipt_items_new rows. Run it after the
// "add_item_groups_and_units" migration and before the
// "drop_legacy_item_tables" migration:
//
//	go run ./cmd/backfill-item-groups
package main

import (
	"context"
	"log"
	"os"

	"splitzies/persistence"
)

func main() {
	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	db, err := persistence.NewClient(ctx, databaseURL)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer db.Close(ctx)

	if err := db.BackfillItemGroups(ctx); err != nil {
		log.Fatalf("backfill failed: %v", err)
	}

	log.Println("backfill complete")
}
