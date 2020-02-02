package main

import "time"

type videoStatistics struct {
	LatestVideoID          string    `firestore:"latestVideoID"`
	LatestVideoPublishedAt time.Time `firestore:"latestVideoPublishedAt"`
	VideoCount             int       ` firestore:"videoCount"`
}

type videoInfo struct {
	ID          string        `firestore:"id"`
	Title       string        `firestore:"title"`
	PublishedAt time.Time     `firestore:"publishedAt"`
	Duration    time.Duration `firestore:"duration"`
	// Number 公開された順番
	// ランダムに取得する際にこの値でオーダーしてカーソルを使う
	Number int `firestore:"number"`
}

type videoInfoPart struct {
	ID          string    `firestore:"id"`
	Title       string    `firestore:"title"`
	PublishedAt time.Time `firestore:"publishedAt"`
}

type scheduleForStore struct {
	Channel1 []byte `firestore:"channel1"`
	Channel2 []byte `firestore:"channel2"`
	Channel3 []byte `firestore:"channel3"`
	Channel4 []byte `firestore:"channel4"`
}
