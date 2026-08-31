package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"splitzies/persistence"
	"splitzies/storage"
	tr "splitzies/transport"
)

//go:embed swagger/docs.html swagger.yaml
var swaggerFS embed.FS

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

	if err := db.RunMigrations(ctx, "migrations"); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	gcsClient, err := storage.NewGCSClient(ctx)
	if err != nil {
		log.Fatalf("failed to create GCS client: %v", err)
	}
	defer gcsClient.Close()

	visionClient, err := storage.NewVisionClient(ctx)
	if err != nil {
		log.Fatalf("failed to create Vision client: %v", err)
	}
	defer visionClient.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	t := tr.NewTransport(logger, db, gcsClient, visionClient)

	mux := http.NewServeMux()

	// Anonymous device identity — the basis for history without an account.
	mux.HandleFunc("POST /devices", t.CreateDeviceHandler)
	mux.HandleFunc("GET /me/receipts", t.ListMyReceiptsHandler)
	mux.HandleFunc("DELETE /me/receipts/{receipt_id}", t.DeleteMyReceiptHandler)

	// Receipt image upload
	mux.HandleFunc("POST /receipts/image", t.UploadReceiptImageHandler)

	// Receipt CRUD
	mux.HandleFunc("GET /receipts/{receipt_id}", t.GetReceiptHandler)
	mux.HandleFunc("PATCH /receipts/{receipt_id}", t.PatchReceiptHandler)
	mux.HandleFunc("GET /receipts/{receipt_id}/items", t.GetReceiptItemsHandler)
	mux.HandleFunc("GET /receipts/{receipt_id}/users", t.GetReceiptUsersHandler)
	mux.HandleFunc("POST /receipts/{receipt_id}/users", t.AddUserToReceiptHandler)
	mux.HandleFunc("DELETE /receipts/{receipt_id}/users/{user_id}", t.RemoveUserFromReceiptHandler)
	mux.HandleFunc("POST /receipts/{receipt_id}/users/{user_id}/claim", t.ClaimUserHandler)
	mux.HandleFunc("POST /receipts/{receipt_id}/users/{user_id}/items", t.AssignItemsToUserHandler)
	mux.HandleFunc("DELETE /receipts/{receipt_id}/users/{user_id}/items/{item_id}", t.UnassignItemFromUserHandler)

	// Collaboration: share a bill with friends who have no account.
	mux.HandleFunc("POST /receipts/{receipt_id}/share", t.CreateShareLinkHandler)
	mux.HandleFunc("DELETE /receipts/{receipt_id}/share", t.RevokeShareLinkHandler)
	mux.HandleFunc("POST /join/{token}", t.JoinShareLinkHandler)

	// Swagger UI
	mux.HandleFunc("GET /swagger/docs.html", func(w http.ResponseWriter, r *http.Request) {
		data, _ := fs.ReadFile(swaggerFS, "swagger/docs.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})
	mux.HandleFunc("GET /swagger.yaml", func(w http.ResponseWriter, r *http.Request) {
		data, _ := fs.ReadFile(swaggerFS, "swagger.yaml")
		w.Header().Set("Content-Type", "application/x-yaml")
		w.Write(data)
	})
	mux.HandleFunc("GET /swagger", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/swagger/docs.html", http.StatusFound)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, corsMiddleware(t.DeviceMiddleware(mux))))
}

func corsMiddleware(next http.Handler) http.Handler {
	// exactOrigins covers production; base44 preview URLs are handled by the
	// wildcard suffix check below so they don't need to be listed individually.
	exactOrigins := map[string]struct{}{
		"https://splitzies.base44.app": {},
	}

	// Allow localhost origins only in development.
	if os.Getenv("ENV") == "development" || os.Getenv("ENV") == "dev" {
		exactOrigins["http://localhost:3000"] = struct{}{}
		exactOrigins["http://localhost:5173"] = struct{}{}
	}

	if extra := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS")); extra != "" {
		for _, o := range strings.Split(extra, ",") {
			if o = strings.TrimSpace(o); o != "" {
				exactOrigins[o] = struct{}{}
			}
		}
	}

	isAllowed := func(origin string) bool {
		if _, ok := exactOrigins[origin]; ok {
			return true
		}
		// Allow any subdomain of known preview hosting platforms (https only).
		for _, suffix := range []string{".base44.app", ".vusercontent.net", ".vercel.app"} {
			if strings.HasPrefix(origin, "https://") && strings.HasSuffix(origin, suffix) {
				return true
			}
		}
		return false
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && isAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			// Without this the browser hides ETag from JS and conditional
			// polling silently degrades to a full fetch every time.
			w.Header().Set("Access-Control-Expose-Headers", "ETag")
		}

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization,X-Device-Token,If-None-Match")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
