// デイリーで行うジョブ
// Youtubeから動画の情報を取得、スケジュールの作成を行う
package main

import (
	"context"
	"log"
)

func exportJob(ctx context.Context) error {
	client, err := createFirestoreClient(ctx)
	if err != nil {
		return err
	}

	service, err := createYoutubeService(ctx)
	if err != nil {
		return err
	}

	err = exportVideo(ctx, service, client)
	if err != nil {
		log.Printf("Can't export video: %v", err)
		return err
	}

	err = exportSchedule(ctx, client)
	if err != nil {
		log.Printf("Can't export schedule: %v", err)
		return err
	}

	return nil
}
