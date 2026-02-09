package transport

import (
	"splitzies/storage"
)

type Transport struct {
	gcsClient    *storage.GCSClient
	visionClient *storage.VisionClient
}

func NewTransport(gcsClient *storage.GCSClient, visionClient *storage.VisionClient) *Transport {
	return &Transport{
		gcsClient:    gcsClient,
		visionClient: visionClient,
	}
}
