package main

import (
	"context"
	"log"

	"cloud.google.com/go/firestore"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/youtube/v3"
)

func createFirestoreClient(ctx context.Context) (*firestore.Client, error) {
	c, err := firestore.NewClient(ctx, "siro-4")
	if err != nil {
		log.Printf("Error creating firestore client: %v", err)
		return nil, err
	}

	return c, nil
}

func createYoutubeService(ctx context.Context) (*youtube.Service, error) {
	client, err := google.DefaultClient(context.Background(), youtube.YoutubeReadonlyScope)
	if err != nil {
		log.Printf("Error creating google client: %v", err)
		return nil, err
	}

	service, err := youtube.New(client)
	if err != nil {
		log.Printf("Error creating YouTube client: %v", err)
		return nil, err
	}

	return service, nil
}
