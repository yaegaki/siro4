// 1日分のスケジュールを作成する
package main

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type videoSource struct {
	ctx           context.Context
	c             *firestore.Client
	r             *rand.Rand
	allVideoCount int
	videos        []videoInfo
}

type videoSourceBlock struct {
	start int
	count int
}

type errVideoStatisticsNotExists struct{}

func (errVideoStatisticsNotExists) Error() string {
	return "VideoStatistics doesn't exist"
}

func newVideoSource(ctx context.Context, c *firestore.Client) (*videoSource, error) {
	snap, err := c.Collection("Info").Doc("VideoStatistics").Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, errVideoStatisticsNotExists{}
		}

		return nil, err
	}

	var statistics videoStatistics
	snap.DataTo(&statistics)

	videoSource := &videoSource{
		ctx:           ctx,
		c:             c,
		r:             rand.New(rand.NewSource(time.Now().UnixNano())),
		allVideoCount: statistics.VideoCount,
	}

	err = videoSource.Fetch(800)
	if err != nil {
		return nil, err
	}

	return videoSource, nil
}

type errCanNotFetchVideo struct{}

func (errCanNotFetchVideo) Error() string {
	return "can not fetch video"
}

func createBlocks(videos []videoInfo) []videoSourceBlock {
	sortedVideos := make([]videoInfo, len(videos))
	copy(sortedVideos, videos)
	sort.Slice(sortedVideos, func(i, j int) bool {
		return sortedVideos[i].Number < sortedVideos[j].Number
	})

	blocks := []videoSourceBlock{}
	block := videoSourceBlock{
		start: 0,
		count: 0,
	}
	for _, v := range sortedVideos {
		if block.count == 0 {
			block.start = v.Number
			block.count = 1
		} else {
			expect := block.start + block.count
			if expect == v.Number {
				block.count++
			} else {
				blocks = append(blocks, block)
				block = videoSourceBlock{
					start: v.Number,
					count: 1,
				}
			}
		}
	}

	if block.count > 0 {
		blocks = append(blocks, block)
	}

	return blocks
}

func getNextBlock(r *rand.Rand, blocks []videoSourceBlock, index, count, max int) videoSourceBlock {
	block := videoSourceBlock{
		start: index,
		count: count,
	}

	if block.start+block.count > max {
		block.count = max - block.start
	}
	blockEnd := block.start + block.count - 1

	for i, b := range blocks {
		if blockEnd < b.start {
			break
		}

		end := b.start + b.count - 1
		if block.start > end {
			continue
		}

		// 領域がかぶっているのでスライドさせる
		slideToFront := true
		if b.start == 0 {
			// ブロックの開始が既に0の場合は後ろにスライドさせるしかない
			slideToFront = false
		} else if end+1 >= max {
			// ブロックの終了が既に最後の場合は前にスライドさせるしかない
			slideToFront = true
		} else {
			// ランダムでどちらにスライドさせるか決める
			slideToFront = r.Intn(2) == 0
		}

		if slideToFront {
			newEnd := b.start - 1
			newStart := newEnd - count + 1
			var startLimit int
			if i == 0 {
				startLimit = 0
			} else {
				prevBlock := blocks[i-1]
				startLimit = prevBlock.start + prevBlock.count
			}
			if newStart < startLimit {
				newStart = startLimit
			}

			return videoSourceBlock{
				start: newStart,
				count: newEnd - newStart + 1,
			}
		} else {
			newStart := end + 1
			newEnd := newStart + count - 1
			var endLimit int
			if i+1 == len(blocks) {
				endLimit = max - 1
			} else {
				nextBlock := blocks[i+1]
				endLimit = nextBlock.start - 1
			}

			if newEnd > endLimit {
				newEnd = endLimit
			}

			return videoSourceBlock{
				start: newStart,
				count: newEnd - newStart + 1,
			}
		}
	}

	return block
}

func mergeBlock(blocks []videoSourceBlock, block videoSourceBlock) []videoSourceBlock {
	temp := make([]videoSourceBlock, 0, len(blocks)+1)
	for _, b := range blocks {
		temp = append(temp, b)
	}
	temp = append(temp, block)
	sort.Slice(temp, func(i, j int) bool {
		return temp[i].start < temp[j].start
	})

	result := make([]videoSourceBlock, 0, len(temp))
	mergeBlock := videoSourceBlock{
		start: 0,
		count: -1,
	}
	for _, b := range temp {
		if mergeBlock.count == -1 {
			mergeBlock = b
		} else {
			end := mergeBlock.start + mergeBlock.count
			if end < b.start {
				result = append(result, mergeBlock)
				mergeBlock = b
			} else {
				currentBlockEnd := b.start + b.count
				if currentBlockEnd > end {
					mergeBlock.count = currentBlockEnd - mergeBlock.start
				}
			}
		}
	}
	if mergeBlock.count > 0 {
		result = append(result, mergeBlock)
	}

	return result
}

func (vs *videoSource) Fetch(count int) error {
	c := 0
	if len(vs.videos)+count > vs.allVideoCount {
		return errCanNotFetchVideo{}
	}

	exists := map[string]struct{}{}
	for _, v := range vs.videos {
		exists[v.ID] = struct{}{}
	}

	blocks := createBlocks(vs.videos)

	max := vs.allVideoCount - 100
	vc := vs.c.Collection("Video")
	totalFetch := 0
	for c < count {
		// あまりにも多すぎる場合はやめておく
		if totalFetch >= 3000 {
			return errCanNotFetchVideo{}
		}

		startIndex := vs.r.Intn(max)
		fetchBlock := getNextBlock(vs.r, blocks, startIndex, 100, vs.allVideoCount)
		if fetchBlock.count <= 0 {
			return errCanNotFetchVideo{}
		}

		iter := vc.OrderBy("number", firestore.Asc).
			StartAt(fetchBlock.start).
			Limit(fetchBlock.count).
			Documents(vs.ctx)

		log.Printf("fetch video:%v count:%v", fetchBlock.start, fetchBlock.count)
		totalFetch += fetchBlock.count

		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return err
			}
			var v videoInfo
			doc.DataTo(&v)
			_, ok := exists[v.ID]
			if ok {
				continue
			}

			vs.videos = append(vs.videos, v)
			exists[v.ID] = struct{}{}
			c++
		}

		blocks = mergeBlock(blocks, fetchBlock)
	}
	log.Printf("total fetch: %v", totalFetch)

	return nil
}

func (vs *videoSource) GetVideo(excludeIDs map[string]struct{}) (videoInfo, error) {
	for len(vs.videos) <= len(excludeIDs) {
		err := vs.Fetch(100)
		if err != nil {
			return videoInfo{}, err
		}
	}

	l := len(vs.videos)
	for {
		v := vs.videos[vs.r.Intn(l)]
		_, ok := excludeIDs[v.ID]
		if ok {
			continue
		}

		return v, nil
	}
}

type videoChannelItem struct {
	Time     time.Time
	Duration time.Duration
	VideoID  string
}

type videoChannel struct {
	Items []videoChannelItem
}

func (c videoChannel) getFinishTime() time.Time {
	l := len(c.Items)
	if l == 0 {
		return time.Time{}
	}

	item := c.Items[l-1]
	return item.Time.Add(item.Duration)
}

type errNotExists struct{}

func (errNotExists) Error() string {
	return "not exists"
}

func (c videoChannel) getVideoID(t time.Time) (string, error) {
	for _, item := range c.Items {
		if item.Time.Before(t) {
			continue
		}

		if t.Before(item.Time.Add(item.Duration)) {
			return item.VideoID, nil
		}
	}

	return "", errNotExists{}
}

type schedule struct {
	Channels []videoChannel
}

func (s schedule) merge(other schedule) schedule {
	result := schedule{
		Channels: make([]videoChannel, 0, channelCount),
	}

	for i, c := range s.Channels {
		var newChannel videoChannel
		var otherChannel videoChannel
		if i < len(other.Channels) {
			otherChannel = other.Channels[i]
		}

		newChannel.Items = make([]videoChannelItem, 0, len(c.Items)+len(otherChannel.Items))
		for _, it := range c.Items {
			newChannel.Items = append(newChannel.Items, it)
		}
		for _, it := range otherChannel.Items {
			newChannel.Items = append(newChannel.Items, it)
		}
		result.Channels = append(result.Channels, newChannel)
	}

	return result
}

// getPart startTime時間を含む場所からDuration分のスケジュールを取得する
func (s schedule) getPart(startTime time.Time, duration time.Duration) schedule {
	result := schedule{
		Channels: make([]videoChannel, len(s.Channels)),
	}

	endTime := startTime.Add(duration)

	for i, c := range s.Channels {
		newChannel := videoChannel{}
		for _, it := range c.Items {
			finish := it.Time.Add(it.Duration)
			if finish.Before(startTime) {
				continue
			}

			if it.Time.After(endTime) {
				break
			}

			newChannel.Items = append(newChannel.Items, it)
		}

		result.Channels[i] = newChannel
	}

	return result
}

const channelCount = 4

func toScheduleKey(t time.Time) string {
	return t.Format("2006-01-02")
}

func getSchedule(ctx context.Context, storeClient *firestore.Client, t time.Time) (schedule, error) {
	key := toScheduleKey(t)
	snap, err := storeClient.Collection("Schedule").Doc(key).Get(ctx)
	if err != nil {
		return schedule{}, err
	}

	var s scheduleForStore
	snap.DataTo(&s)
	channels := make([]videoChannel, channelCount)
	err = json.Unmarshal(s.Channel1, &channels[0])
	if err != nil {
		return schedule{}, err
	}
	err = json.Unmarshal(s.Channel2, &channels[1])
	if err != nil {
		return schedule{}, err
	}
	err = json.Unmarshal(s.Channel3, &channels[2])
	if err != nil {
		return schedule{}, err
	}
	err = json.Unmarshal(s.Channel4, &channels[3])
	if err != nil {
		return schedule{}, err
	}

	return schedule{
		Channels: channels,
	}, nil
}

func createChannel(source *videoSource, startTime time.Time, otherChannels []videoChannel) (videoChannel, error) {
	// 1つのチャンネルでは1日はかぶりなし
	// 他のチャンネルと同じ時間にはかぶりなし

	currentTime := startTime
	items := []videoChannelItem{}

	nextDay := truncateHour(startTime.Add(24 * time.Hour))

	minutes30 := 30 * time.Minute

	for currentTime.Before(nextDay) {
		excludeIDs := make(map[string]struct{}, len(items)+len(otherChannels))
		for _, item := range items {
			excludeIDs[item.VideoID] = struct{}{}
		}

		for _, ch := range otherChannels {
			id, err := ch.getVideoID(currentTime)
			if err != nil {
				continue
			}
			excludeIDs[id] = struct{}{}
		}

		for {
			v, err := source.GetVideo(excludeIDs)
			if err != nil {
				return videoChannel{}, nil
			}

			// 30分以上の動画は候補に入れない
			if v.Duration >= minutes30 {
				excludeIDs[v.ID] = struct{}{}
				continue
			}

			items = append(items, videoChannelItem{
				Time:     currentTime,
				Duration: v.Duration,
				VideoID:  v.ID,
			})

			currentTime = currentTime.Add(v.Duration)
			break
		}
	}

	return videoChannel{
		Items: items,
	}, nil
}

func createSchedule(source *videoSource, prevSchedule *schedule, t time.Time) (schedule, error) {
	getStartTime := func(i int) time.Time {
		if prevSchedule == nil {
			return t
		}

		if i >= len(prevSchedule.Channels) {
			return t
		}

		finishTime := prevSchedule.Channels[i].getFinishTime()
		if finishTime.Before(t) {
			return t
		}

		return finishTime
	}

	channels := make([]videoChannel, 0, channelCount)
	for i := 0; i < channelCount; i++ {
		startTime := getStartTime(i)
		channel, err := createChannel(source, startTime, channels)
		if err != nil {
			return schedule{}, err
		}

		channels = append(channels, channel)
	}

	return schedule{
		Channels: channels,
	}, nil
}

func truncateHour(t time.Time) time.Time {
	t = t.In(jst)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, jst)
}

func getToday() time.Time {
	return truncateHour(time.Now())
}

func dumpSchedule(s schedule) {
	for i, c := range s.Channels {
		log.Printf("Channel:%v", i)
		for _, it := range c.Items {
			log.Printf("%v: %v(%v)", it.Time.In(jst), it.VideoID, it.Duration)
		}
	}
}

func exportScheduleInternal(ctx context.Context, storeClient *firestore.Client, s schedule) error {
	key := toScheduleKey(s.Channels[0].Items[0].Time)
	ch1, err := json.Marshal(s.Channels[0])
	if err != nil {
		return err
	}
	ch2, err := json.Marshal(s.Channels[1])
	if err != nil {
		return err
	}
	ch3, err := json.Marshal(s.Channels[2])
	if err != nil {
		return err
	}
	ch4, err := json.Marshal(s.Channels[3])
	if err != nil {
		return err
	}
	_, err = storeClient.Collection("Schedule").Doc(key).Set(ctx, scheduleForStore{
		Channel1: ch1,
		Channel2: ch2,
		Channel3: ch3,
		Channel4: ch4,
	})
	log.Printf("export schedule: %v", key)

	return err
}

// exportSchedule 明日のスケジュールを作成する
func exportSchedule(ctx context.Context, storeClient *firestore.Client) error {
	today := getToday()
	tommorow := today.Add(24 * time.Hour)

	_, err := getSchedule(ctx, storeClient, tommorow)
	// 明日のスケジュールが既に作成されている場合は何もしない
	if err == nil || status.Code(err) != codes.NotFound {
		return err
	}

	todaySchedule, err := getSchedule(ctx, storeClient, today)
	notFound := status.Code(err) == codes.NotFound
	if err != nil && !notFound {
		return err
	}

	videoSource, err := newVideoSource(ctx, storeClient)
	if err != nil {
		return err
	}

	// 今日のスケジュールが存在しない
	if notFound {
		todaySchedule, err = createSchedule(videoSource, nil, today)
		if err != nil {
			return err
		}
		err = exportScheduleInternal(ctx, storeClient, todaySchedule)
		if err != nil {
			return err
		}
	}

	tommorowSchedule, err := createSchedule(videoSource, &todaySchedule, tommorow)
	if err != nil {
		return err
	}

	return exportScheduleInternal(ctx, storeClient, tommorowSchedule)
}
