package transport

import (
	"log/slog"

	"splitzies/persistence"
	"splitzies/storage"
)

type Transport struct {
	log               *slog.Logger
	persistenceClient *persistence.Client
	gcsClient         *storage.GCSClient
	visionClient      *storage.VisionClient
}

func NewTransport(log *slog.Logger, persistenceClient *persistence.Client, gcsClient *storage.GCSClient, visionClient *storage.VisionClient) *Transport {
	return &Transport{
		log:               log,
		persistenceClient: persistenceClient,
		gcsClient:         gcsClient,
		visionClient:      visionClient,
	}
}
