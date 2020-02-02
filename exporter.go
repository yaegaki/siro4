// SiroChannelの動画情報をFirestoreにエクスポートする
package main

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/youtube/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func parseInt64(value string) int64 {
	if len(value) == 0 {
		return 0
	}
	parsed, err := strconv.Atoi(value[:len(value)-1])
	if err != nil {
		return 0
	}
	return int64(parsed)
}

func parseDuration(str string) time.Duration {
	durationRegex := regexp.MustCompile(`P(?P<years>\d+Y)?(?P<months>\d+M)?(?P<days>\d+D)?T?(?P<hours>\d+H)?(?P<minutes>\d+M)?(?P<seconds>\d+S)?`)
	matches := durationRegex.FindStringSubmatch(str)

	years := parseInt64(matches[1])
	months := parseInt64(matches[2])
	days := parseInt64(matches[3])
	hours := parseInt64(matches[4])
	minutes := parseInt64(matches[5])
	seconds := parseInt64(matches[6])

	hour := int64(time.Hour)
	minute := int64(time.Minute)
	second := int64(time.Second)
	return time.Duration(years*24*365*hour + months*30*24*hour + days*24*hour + hours*hour + minutes*minute + seconds*second)
}

const siroChannelID = "UCLhUvJ_wO9hOvv_yYENu4fQ"
const timeLayout = "2006-01-02T15:04:05Z07:00"

var jst = time.FixedZone("Asia/Tokyo", 9*60*60)

func getChannel(ctx context.Context, service *youtube.Service) (*youtube.Channel, error) {
	res, err := service.Channels.List("contentDetails").Id(siroChannelID).Do()
	if err != nil {
		return nil, err
	}

	return res.Items[0], nil
}

// digVideoInfoPart 指定された日付より新しく公開された動画の情報を取得する
func digVideoInfoPart(ctx context.Context, service *youtube.Service, playlistID string, latestVideoID string, latestVideoPublishedAt time.Time) ([]videoInfoPart, error) {
	nextPageToken := ""

	videoMap := map[string]videoInfoPart{}
	oldCount := 0

	for {
		res, err := service.PlaylistItems.List("snippet").
			PlaylistId(playlistID).
			MaxResults(50).
			PageToken(nextPageToken).
			Do()

		if err != nil {
			return nil, err
		}

		for _, playlistItem := range res.Items {
			publishedAt, err := time.Parse(timeLayout, playlistItem.Snippet.PublishedAt)
			if err != nil {
				return nil, err
			}

			publishedAtJST := publishedAt.In(jst)

			part := videoInfoPart{
				ID:          playlistItem.Snippet.ResourceId.VideoId,
				Title:       playlistItem.Snippet.Title,
				PublishedAt: publishedAtJST,
			}

			_, ok := videoMap[part.ID]
			if ok {
				continue
			}

			if latestVideoID == part.ID || publishedAt.Before(latestVideoPublishedAt) {
				oldCount++
			}

			videoMap[part.ID] = part
		}

		// PlayListItemsは新しく投稿された順になっていない(アップロードされた順?)
		// 古いのを10個以上見つけた場合は最新の物を取得できている可能性が高い
		if oldCount > 10 {
			break
		}

		nextPageToken = res.NextPageToken
		if nextPageToken == "" {
			break
		}
	}

	result := make([]videoInfoPart, 0, len(videoMap))
	for _, part := range videoMap {
		if latestVideoID == part.ID || part.PublishedAt.Before(latestVideoPublishedAt) {
			continue
		}
		result = append(result, part)
	}

	return result, nil
}

func digVideoDuration(ctx context.Context, service *youtube.Service, videoIds []string) (map[string]time.Duration, error) {
	videoIdsStr := strings.Join(videoIds, ",")
	res, err := service.Videos.List("contentDetails").Id(videoIdsStr).Do()
	if err != nil {
		return nil, err
	}

	result := map[string]time.Duration{}

	for _, video := range res.Items {
		duration := parseDuration(video.ContentDetails.Duration)
		result[video.Id] = duration
	}

	return result, nil
}

type errCanNotGetDuration string

func (s errCanNotGetDuration) Error() string {
	return fmt.Sprintf("Can not get duration: video id :%v", s)
}

func exportVideo(ctx context.Context, service *youtube.Service, storeClient *firestore.Client) error {
	channel, err := getChannel(ctx, service)
	if err != nil {
		return err
	}

	videoStatisticsDoc := storeClient.Collection("Info").Doc("VideoStatistics")
	s, err := videoStatisticsDoc.Get(ctx)
	var statistics videoStatistics
	if err != nil && status.Code(err) != codes.NotFound {
		return err
	}

	var latestVideoID string
	var latestVideoPublishedAt time.Time
	if s.Exists() {
		s.DataTo(&statistics)
		latestVideoID = statistics.LatestVideoID
		latestVideoPublishedAt = statistics.LatestVideoPublishedAt
	}

	parts, err := digVideoInfoPart(ctx, service, channel.ContentDetails.RelatedPlaylists.Uploads, latestVideoID, latestVideoPublishedAt)
	if err != nil {
		return err
	}

	sort.Slice(parts, func(i, j int) bool {
		return parts[i].PublishedAt.Before(parts[j].PublishedAt)
	})

	collection := storeClient.Collection("Video")

	var tempParts []videoInfoPart
	var latestVideo videoInfo
	exportCount := 0
	export := func() error {
		if len(tempParts) == 0 {
			return nil
		}

		videoIds := make([]string, 0, len(tempParts))
		for _, part := range tempParts {
			videoIds = append(videoIds, part.ID)
		}

		durationMap, err := digVideoDuration(ctx, service, videoIds)
		if err != nil {
			return err
		}

		for _, part := range tempParts {
			duration, ok := durationMap[part.ID]
			if !ok {
				return errCanNotGetDuration(part.ID)
			}

			video := videoInfo{
				ID:          part.ID,
				Title:       part.Title,
				PublishedAt: part.PublishedAt,
				Duration:    duration,
				Number:      statistics.VideoCount + exportCount,
			}

			docRef := collection.Doc(video.ID)
			_, err = docRef.Set(ctx, video)
			if err != nil {
				return err
			}

			exportCount++
			latestVideo = video
		}

		tempParts = []videoInfoPart{}
		return nil
	}

	var lastErr error

	for _, part := range parts {
		tempParts = append(tempParts, part)
		if len(tempParts) >= 50 {
			err := export()
			if err != nil {
				lastErr = err
				break
			}
		}
	}

	if lastErr == nil {
		lastErr = export()
	}

	if exportCount > 0 {
		_, err := videoStatisticsDoc.Set(ctx, videoStatistics{
			LatestVideoID:          latestVideo.ID,
			LatestVideoPublishedAt: latestVideo.PublishedAt,
			VideoCount:             statistics.VideoCount + exportCount,
		})
		if err != nil {
			return err
		}
		log.Printf("export:%v, %v(%v)", exportCount, latestVideo.Title, latestVideo.ID)
	}

	return lastErr
}
