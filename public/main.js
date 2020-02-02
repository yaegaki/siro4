(async () => {
    const noticeElem = document.getElementById('notice');
    const containerElem = document.getElementById('player-container');

    document.getElementById('watch-button').addEventListener('click', () => {
        noticeElem.setAttribute('style', 'display:none');
        containerElem.setAttribute('style', 'display:grid');

        watch();
    });

    // watchボタンを押す前からスケジュールの取得とyoutubeIFrameAPIの準備はしておく
    const schedulePromise = fetchSchedule();

    const youtubeReadyPromise = new Promise(resolve => {
        var tag = document.createElement('script');

        tag.src = "https://www.youtube.com/iframe_api";
        var firstScriptTag = document.getElementsByTagName('script')[0];
        firstScriptTag.parentNode.insertBefore(tag, firstScriptTag);

        function onYouTubeIframeAPIReady() {
            resolve();
        }

        // グローバルにこの関数が定義されている場合に勝手に呼び出される
        window.onYouTubeIframeAPIReady = onYouTubeIframeAPIReady;
    });

    async function fetchSchedule() {
        const res = await fetch("/schedule");
        const rawSchedule = await res.json();
        return {
            channels: rawSchedule.Channels.map(c => {
                return {
                    items: c.Items.map(i => {
                        return {
                            time: parseDate(i.Time),
                            duration: i.Duration /  1000000000,
                            videoId: i.VideoID,
                        };
                    }),
                };
            }),
        };
    }

    function sleep(time) {
        return new Promise(resolve => setTimeout(resolve, time));
    }

    async function watch() {
        await youtubeReadyPromise;
        const schedule = await schedulePromise;
        console.log(schedule);

        const players = ['player1', 'player2', 'player3', 'player4'].map((p, i) => createPlayer(p, schedule, i));

        // スケジュールを1時間に一度取得する
        updateScheduleTask(players);
    }

    async function updateScheduleTask(players) {
        const hour = 1000 * 60 * 60;
        while (true) {
            await sleep(hour);
            const newSchedule = await fetchSchedule();
            console.log('update schedule.');
            console.log(newSchedule);
            players.forEach(p => p.applySchedle(newSchedule));
        }
    }

    function createPlayer(elemId, schedule, channelId) {
        let currentVideoInfo = getVideoAndOffset(schedule.channels[channelId], getNowDate());

        const ytPlayer = new YT.Player(elemId, {
            height: '360',
            width: '640',
            videoId: currentVideoInfo.video.videoId,
            playerVars: {
                autoplay: true,
                cc_load_policy: 0,
                disablekb: 0,
                fs: 0,
                start: currentVideoInfo.offset,
                playsinline: 1,
            },
            events: {
                onStateChange(e) {
                    // console.log(e);
                },
            }
        });

        const player = {
            ytPlayer,
            applySchedle(newSchedule) {
                schedule = newSchedule;
            },
        };

        console.log(ytPlayer);

        // 再生時間の同期
        (async () => {
            const defaultSyncTime = 5 * 1000;
            const fastSyncTime = 1000;
            let syncTime = defaultSyncTime;
            let bufferCount = 0;
            while (true) {
                await sleep(syncTime);
                const playerState = player.ytPlayer.getPlayerState();
                if (playerState === YT.PlayerState.UNSTARTED) continue;
                if (playerState === YT.PlayerState.BUFFERING) {
                    bufferCount++;
                }
                const videoData = player.ytPlayer.getVideoData();
                if (videoData == null) continue;

                const currentVideoId = videoData.video_id;
                currentVideoInfo = getVideoAndOffset(schedule.channels[channelId], getNowDate(), currentVideoId);

                if (bufferCount >= 2 || currentVideoInfo.video.videoId !== currentVideoId) {
                    player.ytPlayer.loadVideoById(currentVideoInfo.video.videoId, currentVideoInfo.offset);
                    bufferCount = 0;
                    syncTime = defaultSyncTime;
                    console.log(`change video from:${currentVideoId} to:${currentVideoInfo.video.videoId}`);
                }
                else {
                    if (playerState === YT.PlayerState.PAUSED) {
                        player.ytPlayer.playVideo();
                    }

                    const diff = Math.abs(player.ytPlayer.getCurrentTime() - currentVideoInfo.offset);
                    // 10秒以上離れたらシークする
                    if (diff > 10) {
                        player.ytPlayer.seekTo(currentVideoInfo.offset, true);
                        console.log(`seek video diff:${diff}`);
                        console.log(videoData);
                    }

                    const remain = currentVideoInfo.video.duration - currentVideoInfo.offset;
                    syncTime = remain > 5 ? defaultSyncTime : fastSyncTime;
                }
            }
        })();

        return player;
    }

    function getVideoAndOffset(channel, date, currentId) {
        let skip = currentId != null;

        for (let i = 0; i < channel.items.length; i++) {
            const item = channel.items[i];
            if (skip) {
                if (item.videoId !== currentId) {
                    continue;
                }

                skip = false;
            }

            const diff = subDate(date, item.time);
            if (diff < 0 || diff > item.duration) {
                continue;
            }

            return {
                video: item,
                offset: diff,
            }
        }

        return {
            video: channel.items[0],
            offset: 0,
        };
    }

    function parseDate(dateStr) {
        const x = dateStr.split('T');
        const [year, month, day] = x[0].split('-');

        // 時間はjstで指定されていること前提
        const [hours, minutes, seconds] = x[1].split('+')[0].split(':');
        const p = s => parseInt(s, 10);
        return {
            year: p(year),
            month: p(month),
            day: p(day),
            hours: p(hours),
            minutes: p(minutes),
            seconds: p(seconds),
        };
    }

    function getNowDate() {
        // デフォルトのDateオブジェクトはタイムゾーン関係が面倒なので独自のものを使用する
        const now = new Date();
        // 一旦UTCにする
        now.setMinutes(now.getMinutes() + now.getTimezoneOffset());
        // JSTにする
        const jstOffset = 60 * 9;
        now.setMinutes(now.getMinutes() + jstOffset);
        return {
            year: now.getFullYear(),
            month: now.getMonth() + 1,
            day: now.getDate(),
            hours: now.getHours(),
            minutes: now.getMinutes(),
            seconds: now.getSeconds(),
        }
    }

    function toRawDate(date) {
        return new Date(date.year, date.month - 1, date.day, date.hours, date.minutes, date.seconds);
    }

    // 日付の引き算
    // 単位は秒
    function subDate(date1, date2) {
        // 引き算ではタイムゾーンの影響がないので一旦デフォルトのDateに戻して計算する
        return (toRawDate(date1) - toRawDate(date2)) / 1000;
    }
})();