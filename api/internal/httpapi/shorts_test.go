package httpapi

import (
	"os"
	"strings"
	"testing"
)

func TestShortsPlaybackPage(t *testing.T) {
	page, err := watchHTML.ReadFile("shorts.html")
	if err != nil {
		t.Fatal(err)
	}
	body := string(page)
	for _, need := range []string{
		"/v1/feed/shorts",
		"random=1",
		"fwdK",
		"FORWARD_MAX = 3",
		"BACK = 1",
		"ch=",
		"bigplay",
		"hud-audio",
		"scrub-play",
		"#3ea6ff",
		"pointer: coarse",
		"togglePlayback",
		"flashMuteHud",
		"toggleMute",
		`class="mute"`,
		"打开声音",
		"let muted = true",
		"hitUiControl",
		"hitPicture",
		"pointer-events: none",
		`getAttribute("aria-label") === label`,
		"hover: none",
		"sameOriginMedia",
		"media-edge.js",
		"mtMedia.ready",
		"/v1/public/site",
		"shorts-engine.js",
		"webcodecs",
		"stage-box",
		"likecount",
		"decodeIds",
		"fwdIds",
		"d <= fwdK",
		"playSafe",
		"unlockAudio",
		"SLIDE_KEY_MS = 560",
		"SLIDE_FLICK_MS = 420",
		"SLIDE_BACK_MS = 380",
		"easeInOutPoly",
		"xformIds",
		"setSlidePos",
		"translate3d(0, 200%, 0)",
		"setSlidePos(v.id, 2)",
		"slideDir",
		"raf-dt",
		"idle-cost",
		"window-cost",
		"setHotWindow",
		"wrapIdx(idx - 1)",
		"onLog: slog",
		"neighborsOk",
		"wc.neighborsReady",
	} {
		if !strings.Contains(body, need) {
			t.Fatalf("shorts.html missing %q", need)
		}
	}
	if strings.Contains(body, "const neighborsOk = true") {
		t.Fatal("neighbors must wait for listenReady before opening extra decoders")
	}
	if strings.Contains(body, "ensureSound") || strings.Contains(body, "wantSound") {
		t.Fatal("video tap must not unmute; mute state is only the mute button")
	}
	if strings.Contains(body, `class="slide is-paused"`) {
		t.Fatal("slides must not start with the play triangle visible")
	}
	if !strings.Contains(body, "!d.moved") {
		t.Fatal("a click without swipe must toggle playback after pointer capture")
	}
	if !strings.Contains(body, "if (!hitPicture(e.clientX, e.clientY)) return") {
		t.Fatal("PC click-to-pause must hit the picture, not the letterbox")
	}
	ch, err := watchHTML.ReadFile("channel.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ch), `query: "?ch="`) {
		t.Fatal("channel.html should pass ch= into shorts links")
	}
	for _, name := range []string{"shorts-engine.js", "shorts-worklet.js", "shorts-worker.js", "shorts-decode-worker.js"} {
		if _, err := watchHTML.ReadFile(name); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	eng, err := watchHTML.ReadFile("shorts-engine.js")
	if err != nil {
		t.Fatal(err)
	}
	engine := string(eng)
	for _, need := range []string{"holdFrame", "pruneHeld", "parkCanvas", "presentIdle", "paintFirst", "createClock", "SYNC_LOST", "SYNC_TIGHT", "SYNC_CATCH_BEHIND", "takeVideoOne", "audioPlayPts", "setHotWindow", "snapshotFrame", "ingestIdle", "freezeToBitmaps", "dropQueuedMedia", "clock-start", "pcm-fill", "pcm-start", "logHot", "createScriptProcessor(2048, 2, 2)", "audioWorklet.addModule", "isMuted", "audio-in", "pcm-out", "pcm-underrun", "pcm-worklet", "PCM_AHEAD", "ARM_AUDIO", "NEIGHBOR_AUDIO", "pumpSpeaker", "listenReady", "neighborsReady", `slog("sync"`, "mode=", "dv=", "da=", "vdiff=", "mediaTime", "warms: warmIds.slice()", "function applyWarms", "pcmS=", "aacS=", "fifo=", "dumpq", "pcm-buffer", "enterRebuffer", "REBUFFER_HIGH"} {
		if !strings.Contains(engine, need) {
			t.Fatalf("shorts-engine.js missing %q", need)
		}
	}
	if !strings.Contains(engine, "SYNC_CATCH_BEHIND = 3") || !strings.Contains(engine, "function videoIsLost") {
		t.Fatal("video TryToMatch must reach 3s behind the clock")
	}
	if !strings.Contains(engine, "function holdForBackground") || !strings.Contains(engine, "vis-hold") || !strings.Contains(engine, "visCatch") {
		t.Fatal("backgrounding must flush the speaker and catch video up on return")
	}
	if strings.Contains(engine, "flattenCanvas") {
		t.Fatal("flattenCanvas locks the IDR on Chrome zero-copy canvases")
	}
	if strings.Contains(engine, "getImageData") {
		t.Fatal("getImageData on a VideoFrame canvas snaps back to the IDR")
	}
	if strings.Contains(engine, "clock-align") {
		t.Fatal("clock-align fast-forwards late video and empties the start buffer")
	}
	if strings.Contains(engine, "clock-jump") {
		t.Fatal("do not seek the wall clock onto a far-ahead P-frame")
	}
	if strings.Contains(engine, "drop-late") {
		t.Fatal("late video must LostMatch instead of drop-late")
	}
	if !strings.Contains(engine, "lost-match") || !strings.Contains(engine, "try-match") {
		t.Fatal("video must enter LostMatch / TryToMatch against the audio clock")
	}
	if strings.Contains(engine, "drop-hole") {
		t.Fatal("dropping a GOP hole freezes the picture while audio continues")
	}
	if !strings.Contains(engine, "pts-gap") || !strings.Contains(engine, "HOLE_THRESH") {
		t.Fatal("log a pts gap but still present the due frame")
	}
	if !strings.Contains(engine, "drop-back") {
		t.Fatal("a late frame with an earlier pts must not rewind the picture")
	}
	if strings.Contains(engine, "else if (msg.pts != null) shownPts[msg.id] = msg.pts") {
		t.Fatal("queued listen frames must not move shownPts before they are drawn")
	}
	if !strings.Contains(engine, "function armStart") {
		t.Fatal("engine must hold the first frames until both tracks have a start buffer")
	}
	if !strings.Contains(engine, "function dequeRunSeconds") || !strings.Contains(engine, `"vr="`) {
		t.Fatal("start must wait for a consecutive video run, not a GOP hole of summed durations")
	}
	if !strings.Contains(engine, "video: dequeRunSeconds(st.video)") {
		t.Fatal("held video reported to the worker must be the consecutive run, not summed GOP-hole durations")
	}
	if !strings.Contains(engine, "start-hole") || !strings.Contains(engine, "function videoHasGopHole") {
		t.Fatal("a remaining GOP hole must still start audio instead of freezing on the cover")
	}
	if strings.Contains(engine, "dropQueuedMedia(prev)") {
		t.Fatal("leaving a clip must keep its decoded deque instead of dropping it")
	}
	if strings.Contains(engine, `slog("av-reset"`) || strings.Contains(engine, "AV_SKEW") {
		t.Fatal("A/V deque head skew must not rewind or drop queued media")
	}
	if !strings.Contains(engine, "startPts: 0") {
		t.Fatal("neighbors must open from the first frame, not a leftover resumeAt")
	}
	if !strings.Contains(engine, "clock.primed()") || !strings.Contains(engine, "msg.id === activeId && clock.primed()") {
		t.Fatal("warm cover frames must not be dropped because of a previous play of the same id")
	}
	if !strings.Contains(engine, "st.dropHigh") || !strings.Contains(engine, "dropHighV") {
		t.Fatal("after av-reset, leftover high pts frames must be dropped until both tracks see the start GOP")
	}
	if !strings.Contains(engine, "function dropRewindPts") {
		t.Fatal("a cover video frame must not open the gate for leftover 11s audio")
	}
	if !strings.Contains(engine, "delete lastRenderedPts[id]") {
		t.Fatal("reopening a clip must forget the previous playhead")
	}
	if !strings.Contains(engine, "function pumpSpeaker") {
		t.Fatal("engine must prefill the speaker FIFO instead of due()-gating each audio frame")
	}
	if !strings.Contains(engine, "function takeBackPcm") || !strings.Contains(engine, "pcm-restore") {
		t.Fatal("unplayed worklet PCM must go back on the clip deque instead of being dropped")
	}
	if strings.Contains(engine, "due(aclock, st.audio") {
		t.Fatal("speaker must not wait for wall-clock due() before sending PCM")
	}
	if strings.Contains(engine, "audio.q.length > 24") {
		t.Fatal("dropping oldest PCM chunks punches holes in the speaker FIFO")
	}
	if !strings.Contains(engine, "PCM_AHEAD = 0.4") {
		t.Fatal("speaker FIFO must hold ~250ms against main-thread hitch")
	}
	if !strings.Contains(engine, "ARM_AUDIO = 0.6") {
		t.Fatal("start must wait for ~0.6s of decoded audio so the speaker FIFO can fill")
	}
	if !strings.Contains(engine, "NEIGHBOR_AUDIO = 0.2") {
		t.Fatal("neighbors may open after ~0.2s of listen audio, not the 0.6s speaker arm")
	}
	if !strings.Contains(engine, "function maybeKickSpeaker") {
		t.Fatal("speaker must not play until the worklet FIFO is primed")
	}
	if strings.Contains(engine, "if (clock.primed()) startArmed = true") {
		t.Fatal("a cover frame must not start clocks without the 0.3s/0.6s start buffer")
	}
	if strings.Contains(engine, "qs > 0.05") {
		t.Fatal("speaker must not start on a dry 50ms FIFO")
	}
	if !strings.Contains(engine, "function attachWorklet") {
		t.Fatal("engine must move PCM playback onto AudioWorklet")
	}
	worklet, err := watchHTML.ReadFile("shorts-worklet.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(worklet), "registerProcessor(\"shorts-pcm\"") {
		t.Fatal("shorts-worklet.js must register shorts-pcm")
	}
	if !strings.Contains(string(worklet), "type: \"underrun\"") {
		t.Fatal("worklet must report underruns to the page")
	}
	if !strings.Contains(string(worklet), "type: \"stat\"") {
		t.Fatal("worklet must report actual FIFO depth")
	}
	if !strings.Contains(string(worklet), "d.type === \"start\"") {
		t.Fatal("worklet must wait for start before consuming PCM")
	}
	if !strings.Contains(string(worklet), "d.type === \"hold\"") {
		t.Fatal("worklet must freeze FIFO consumption without reset")
	}
	if !strings.Contains(string(worklet), "type: \"drain\"") {
		t.Fatal("worklet must return unplayed FIFO depth on reset")
	}
	if !strings.Contains(string(worklet), "this.playing") {
		t.Fatal("worklet must hold the FIFO silent until start")
	}
	if !strings.Contains(string(worklet), "d.gen < this.gen") {
		t.Fatal("worklet must accept a newer gen instead of dropping all PCM")
	}
	if !strings.Contains(engine, "function copyPcm") {
		t.Fatal("PCM posted to the worklet must be a packed copy, not a transferred buffer")
	}
	if strings.Contains(engine, "[samples.buffer]") {
		t.Fatal("do not transfer ArrayBuffers into AudioWorklet; Chrome drops the samples")
	}
	if strings.Contains(engine, "primeIfNeeded") {
		t.Fatal("shorts-engine.js should presentIdle instead of primeIfNeeded")
	}
	if strings.Contains(engine, "livePtsReady") || strings.Contains(engine, "function mediaTime") {
		t.Fatal("shorts-engine.js should use a wall clock, not a shared mediaTime")
	}
	if !strings.Contains(engine, "if (Number.isFinite(shown)) return shown") {
		t.Fatal("progress mediaTime must follow the last painted frame, not a running clock during stalls")
	}
	if strings.Contains(engine, "vclock") || strings.Contains(engine, "aclock") {
		t.Fatal("shorts-engine.js should keep one clock set by audio, not vclock/aclock")
	}
	if !strings.Contains(engine, "const clock = createClock()") {
		t.Fatal("shorts-engine.js must keep a single wall clock")
	}
	if !strings.Contains(engine, "armFromPainted") || !strings.Contains(engine, "clock-resume") {
		t.Fatal("shorts-engine.js should resume from the already painted frame on activate")
	}
	if !strings.Contains(body, "wc.presentIdle(id, true);") || !strings.Contains(body, "animateShift(0, slideDir, SLIDE_KEY_MS, easeInOutPoly)") {
		t.Fatal("goTo should paint idle then animate, not activate before the slide")
	}
	goToIdle := strings.Index(body, "wc.presentIdle(id, true);")
	goToAnim := strings.Index(body, "animateShift(0, slideDir, SLIDE_KEY_MS, easeInOutPoly)")
	goToAct := strings.Index(body, "function goTo(id, dir)")
	if goToIdle < goToAct || goToAnim < goToIdle {
		t.Fatal("goTo should paint idle then animate, not activate before the slide")
	}
	if strings.Contains(body, "wc.presentIdle(id);\n      activate(id);\n      animateShift") {
		t.Fatal("goTo must not activate before the slide animation")
	}
	if !strings.Contains(engine, "shorts-decode-worker.js") || !strings.Contains(engine, "MessageChannel") {
		t.Fatal("engine must spawn a demux worker and a decode worker per pipe")
	}
	if strings.Contains(engine, "worker.postMessage") {
		t.Fatal("engine must talk to per-pipe workers, not a shared media worker")
	}
	src, err := os.ReadFile("shorts-worker-src.js")
	if err != nil {
		t.Fatal(err)
	}
	bundled, err := os.ReadFile("shorts-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	dec, err := os.ReadFile("shorts-decode-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	demux := string(src)
	decode := string(dec)
	if !strings.Contains(string(bundled), "function wrapTrack") || !strings.Contains(string(bundled), "function runDemux") {
		t.Fatal("shorts-worker.js is stale; sync tail from shorts-worker-src.js")
	}
	if strings.Contains(demux, ".codedHeight") || strings.Contains(decode, ".codedHeight") {
		t.Fatal("workers must not use sync codedHeight")
	}
	if !strings.Contains(demux, "getPrimaryVideoTrack") {
		t.Fatal("demux should use getPrimaryVideoTrack")
	}
	if !strings.Contains(demux, "id === prevId") || !strings.Contains(decode, "id === prevId") {
		t.Fatal("workers should keep previous hot")
	}
	if !strings.Contains(demux, "msg.warms") || !strings.Contains(decode, "msg.warms") {
		t.Fatal("workers should keep a list of forward warm ids")
	}
	if !strings.Contains(decode, "audioToStereo") {
		t.Fatal("decode worker should copy AudioData before close")
	}
	if !strings.Contains(decode, "firstSnap") {
		t.Fatal("decode worker should snapshot only the first idle frame")
	}
	if !strings.Contains(decode, "emitVideo") {
		t.Fatal("decode worker should snapshot idle frames in the worker")
	}
	if !strings.Contains(decode, "canSubmit") {
		t.Fatal("decode worker should skip work that cannot submit now")
	}
	if !strings.Contains(demux, "IDLE_SLEEP_MS") || !strings.Contains(decode, "IDLE_SLEEP_MS") {
		t.Fatal("workers should sleep only when buffers are full")
	}
	if strings.Contains(demux, "waitDecodeSlot") || strings.Contains(decode, "waitDecodeSlot") {
		t.Fatal("workers must not block the scheduler on decodeQueueSize")
	}
	if strings.Contains(demux, "function pickWork") || strings.Contains(decode, "function pickWork") {
		t.Fatal("multi-worker pipes must not share a pickWork time-slice scheduler")
	}
	if strings.Contains(demux, "LISTEN_CRITICAL") || strings.Contains(decode, "LISTEN_CRITICAL") {
		t.Fatal("do not pause neighbors when listen is below 0.3s")
	}
	if !strings.Contains(demux, "function runDemux") || !strings.Contains(decode, "function runDecode") {
		t.Fatal("each pipe needs a demux loop and a decode loop")
	}
	if !strings.Contains(demux, "listen && aCredit > 0") {
		t.Fatal("listen demux must pull audio before video so a slow video fetch does not starve AAC")
	}
	if !strings.Contains(demux, "=== \"credit\"") || !strings.Contains(decode, "type: \"credit\"") {
		t.Fatal("decode must backpressure demux when the packet pipe is full")
	}
	if !strings.Contains(decode, "pipe.id !== listenId") && !strings.Contains(decode, "p.id !== listenId") {
		t.Fatal("idle vs listen must still change bitmap/queue limits")
	}
	if !strings.Contains(demux, "idle-eos") {
		t.Fatal("neighbors must not loop-wrap independently of the listen timeline")
	}
	if !strings.Contains(demux, "function promoteListen") || !strings.Contains(demux, "listen-wrap") {
		t.Fatal("becoming listen must wrap tracks that idle-eos'd as a neighbor")
	}
	if !strings.Contains(demux, "msg.type === \"rewind\"") || !strings.Contains(decode, "msg.type === \"rewind\"") {
		t.Fatal("workers must rewind a desynced pipe back to the first frame")
	}
	if !strings.Contains(demux, "p.epoch !== epoch") {
		t.Fatal("demux must drop in-flight packets across rewind")
	}
	if !strings.Contains(decode, "vdec.flush") || !strings.Contains(decode, "adec.flush") {
		t.Fatal("rewind must flush decoder output so leftover 11s audio cannot land")
	}
	if !strings.Contains(demux, "wrap-skip") || !strings.Contains(demux, "empty-held") {
		t.Fatal("do not add a loop offset when the cover GOP never arrived")
	}
	if !strings.Contains(demux, "vLastEnd || 0) > 2") {
		t.Fatal("warm must not demux the whole clip to EOS")
	}
	if !strings.Contains(decode, "GOP_QUEUE") {
		t.Fatal("post-cover idle decode must allow a few in-flight P-frames")
	}
	if !strings.Contains(decode, "function clampDur") || !strings.Contains(decode, "VPD_MAX") || !strings.Contains(decode, "APD_MAX") {
		t.Fatal("decode worker should clamp packet duration and cap vpd/apd")
	}
	if !strings.Contains(decode, "PacketDeque") {
		t.Fatal("decode worker should keep encoded packets in PacketDeque")
	}
	if strings.Contains(demux, "async function decodeOne") || strings.Contains(decode, "async function decodeOne") {
		t.Fatal("workers should pump packet deques instead of decodeOne")
	}
	if !strings.Contains(decode, "decoder") || !strings.Contains(decode, "action: \"open\"") {
		t.Fatal("decode worker should log decoder open/close")
	}
	if !strings.Contains(decode, "function copyFrameBitmap") {
		t.Fatal("decode worker should copy VideoFrames out of the decoder DPB")
	}
	if strings.Contains(decode, "pend * (kind === \"video\"") {
		t.Fatal("frame budget must not include decodeQueue pending")
	}
	if !strings.Contains(decode, "PIPE_SLACK") {
		t.Fatal("submit should keep a hardware decode pipeline above the held budget")
	}
	if !strings.Contains(decode, "LISTEN_BUDGET + PIPE_SLACK") {
		t.Fatal("listen decode must keep the decode pipeline, not only the 0.5s held budget")
	}
	if strings.Contains(decode, "if (waiting) continue") {
		t.Fatal("snap must keep feeding the cover GOP instead of waiting after one packet")
	}
	if strings.Contains(decode, "while (!pipe.firstSnap && sent < SNAP_QUEUE && sendPacket") || strings.Contains(decode, "while (!p.firstSnap && sent < SNAP_QUEUE && sendPacket") {
		t.Fatal("snap must not synchronously burst GOP packets; VideoDecoder output is async")
	}
	if strings.Contains(decode, "videoPend || 0) >= 2") {
		t.Fatal("cover snap must keep feeding until the first frame outputs; HW may hold the IDR")
	}
	if !strings.Contains(decode, "Keep feeding the cover GOP until the first frame outputs") {
		t.Fatal("cover snap must keep feeding until the first frame outputs; HW may hold the IDR")
	}
	if !strings.Contains(decode, "enqueue pts before decode") {
		t.Fatal("packet timestamps must be queued before decode(); Chrome may output synchronously")
	}
	if !strings.Contains(decode, "function takeVideoPts") || !strings.Contains(decode, "vPtsQ") {
		t.Fatal("decoded frames must use submitted packet timestamps, not hardware VideoFrame.timestamp")
	}
	if !strings.Contains(decode, "B-frames leave decode order") {
		t.Fatal("packet pts must be matched to VideoFrame.timestamp so B-frames are not shown in decode order")
	}
	if !strings.Contains(decode, "SNAP_QUEUE") {
		t.Fatal("cover snap must be allowed more than one decode slot")
	}
	if !strings.Contains(decode, "dropLeadingDelta") {
		t.Fatal("snap must skip leading delta packets until a keyframe")
	}
	if !strings.Contains(decode, "sentVideoKey") {
		t.Fatal("snap must not drop GOP P-frames after the cover key is sent")
	}
	if !strings.Contains(decode, "idle && p.firstSnap") && !strings.Contains(decode, "idle && pipe.firstSnap") {
		t.Fatal("idle snap must keep GOP frames after the cover for playback")
	}
	if strings.Contains(decode, "idle && p.firstSnap) {\n    frame.close()") || strings.Contains(decode, "idle && pipe.firstSnap) {\n    frame.close()") {
		t.Fatal("do not drop GOP frames after the cover")
	}
	if strings.Contains(decode, "bitmapCanvas") {
		t.Fatal("do not reuse OffscreenCanvas after transferToImageBitmap")
	}
	if strings.Contains(demux, "async function finishEos") || strings.Contains(decode, "async function finishEos") {
		t.Fatal("loop wrap must not flush the decoder at EOS")
	}
	if !strings.Contains(demux, "function wrapTrack") {
		t.Fatal("demux should wrap to the start at EOS without waiting for frames to drain")
	}
	if strings.Contains(demux, "/ 1e6 - (pipe.vBase") || strings.Contains(demux, "/ 1e6 - (p.vBase") {
		t.Fatal("loop video pts must stay on the wrapped timeline so LostMatch does not freeze the picture")
	}
	if strings.Contains(demux, "pkt.pts -= (this.aBase") {
		t.Fatal("loop audio pts must stay on the wrapped timeline")
	}
	if !strings.Contains(demux, "function absPktPts") {
		t.Fatal("packet pts must be stamped absolute at demux time")
	}
	if !strings.Contains(demux, "wrap does not restamp queued packets") {
		t.Fatal("loop wrap must not add vBase again to packets already in vpd")
	}
	if strings.Contains(demux, "local + (pipe.vBase || 0)") || strings.Contains(demux, "local + (p.vBase || 0)") {
		t.Fatal("do not add current vBase at send time; leftover loop packets would jump a full clip")
	}
	if !strings.Contains(decode, "decoderReady") {
		t.Fatal("decode must wait for VideoDecoder before submitting a cover packet")
	}
	if !strings.Contains(decode, "await sleep(0)") {
		t.Fatal("decode should yield 0ms when waiting on hardware decode")
	}
	if !strings.Contains(string(bundled), "function runDemux") {
		t.Fatal("shorts-worker.js is stale; sync tail from shorts-worker-src.js")
	}
	if !strings.Contains(string(bundled), "wrap does not restamp queued packets") {
		t.Fatal("shorts-worker.js is stale; sync tail from shorts-worker-src.js")
	}
	if !strings.Contains(string(bundled), "idle-eos") || !strings.Contains(string(bundled), "msg.type === \"rewind\"") {
		t.Fatal("shorts-worker.js is stale; sync tail from shorts-worker-src.js")
	}
	if !strings.Contains(string(bundled), "wrap-skip") || !strings.Contains(string(bundled), "vLastEnd || 0) > 2") {
		t.Fatal("shorts-worker.js is stale; sync tail from shorts-worker-src.js")
	}
	if !strings.Contains(demux, "dropAudio") || !strings.Contains(string(bundled), `reason: "drop-audio"`) {
		t.Fatal("demux should drop AAC when dropAudio is set")
	}
	if !strings.Contains(decode, "dropAudio") {
		t.Fatal("decode worker should skip AAC when dropAudio is set")
	}
	if !strings.Contains(decode, "p.rawFrames") {
		t.Fatal("decode worker should skip bitmap copy when rawFrames is set")
	}
}

func TestShortsHwtestPage(t *testing.T) {
	page, err := watchHTML.ReadFile("shorts-hwtest.html")
	if err != nil {
		t.Fatal(err)
	}
	body := string(page)
	for _, need := range []string{
		`grid-template-columns: repeat(5, minmax(0, 1fr))`,
		"grid-template-rows: repeat(2, auto)",
		"width: 84%",
		"class=\"fps\"",
		"fpsOf",
		"SLOTS = 10",
		"GAP_MS = 3000",
		`CHANNEL = "demo1"`,
		"kind=short",
		"开始",
		"停止",
		"重置",
		"当下正常硬件解码的路数",
		"shorts-hwtest.js",
		"media-edge.js",
		".stage.ok",
		".stage.bad",
	} {
		if !strings.Contains(body, need) {
			t.Fatalf("shorts-hwtest.html missing %q", need)
		}
	}
	eng, err := watchHTML.ReadFile("shorts-hwtest.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(eng)
	for _, need := range []string{
		"SNAP_SEC = 1",
		"SYNC_TIGHT = 0.2",
		"dropAudio: true",
		"clientWidth",
		"vclock",
		"createClock",
		"Math.abs(pts - now) > SNAP_SEC",
		"pts >= now",
		"DRAW_EVERY_MS = 1000",
		"rawFrames: true",
		"noteFps",
		"STATUS_EVERY_MS = 100",
		"STUCK_MS = 2000",
		"decoder-closed",
		`msg.type === "audio"`,
	} {
		if !strings.Contains(js, need) {
			t.Fatalf("shorts-hwtest.js missing %q", need)
		}
	}
	if !strings.Contains(js, `if (msg.type === "audio") return;`) {
		t.Fatal("hwtest engine must drop audio samples")
	}
	if strings.Contains(js, "st.video.pop()") {
		t.Fatal("frame deque must not drop newest frames; backpressure is via held budget")
	}
}
