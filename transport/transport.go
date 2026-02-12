package transport

import (
	"splitzies/persistence"
	"splitzies/storage"
)

type Transport struct {
	persistenceClient *persistence.Client
	gcsClient         *storage.GCSClient
	visionClient      *storage.VisionClient
}

func NewTransport(persistenceClient *persistence.Client, gcsClient *storage.GCSClient, visionClient *storage.VisionClient) *Transport {
	return &Transport{
		persistenceClient: persistenceClient,
		gcsClient:         gcsClient,
		visionClient:      visionClient,
	}
}
