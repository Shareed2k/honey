// rtsp_probe: verify a live RTSP stream actually delivers video, via ffmpeg.
//
// honey overrides the image entrypoint with the honey-plugin-init shim and execs
// argv[0] directly, so argv[0] MUST be the absolute binary path
// (/usr/local/bin/ffmpeg, /usr/local/bin/ffprobe), not bare "ffmpeg"/"ffprobe".
//
// #Config fields are required-with-default (`| *…`), never optional (`?`):
// evalAction decodes the unified config back to a plain map to apply defaults,
// and CUE omits uninstantiated optional fields, which would drop them from argv.

// check: read `sample` seconds of the RTSP url's VIDEO stream and exit 0 only if
// packets actually flow. A dead / unreachable / silent (frozen) stream makes
// ffmpeg exit non-zero (connect error or the -timeout socket read timeout);
// honey surfaces that as a step failure. This is the real "is it returning
// video" test.
//
// It uses `-c copy` (no decode) deliberately: a real camera may not deliver a
// keyframe / SPS fast enough for ffmpeg to determine the frame size, which fails
// a decode-based probe with "Could not find codec parameters ... unspecified
// size" even though the stream is live. Copying packets needs no codec
// parameters — any video RTP packet flowing proves the camera is streaming.
// `-map 0:v:0` makes it video-specific (no video stream → non-zero).
actions: check: {
	#Config: {
		input:      string
		sample:     string | *"3"        // seconds of stream to read before declaring healthy
		transport:  string | *"tcp"      // rtsp_transport: tcp (reliable) or udp
		timeout_us: string | *"15000000" // rtsp socket I/O timeout, microseconds (15s)
	}
	// -timeout is the RTSP demuxer's socket-timeout option (microseconds). Do
	// NOT use -rw_timeout here: the rtsp demuxer rejects it once the input
	// actually opens ("Option rw_timeout not found"), and -stimeout was removed
	// in ffmpeg 8.
	argv: [
		"/usr/local/bin/ffmpeg", "-hide_banner",
		"-rtsp_transport", config.transport,
		"-timeout", config.timeout_us,
		"-i", config.input,
		"-map", "0:v:0",
		"-t", config.sample,
		"-c", "copy",
		"-f", "null", "-",
	]
	output_format: "text"
}

// probe: ffprobe the first video stream as JSON (codec/size). A fast
// connectivity + stream-present check for debugging; does not prove frames flow
// the way `check` does.
actions: probe: {
	#Config: {
		input:      string
		transport:  string | *"tcp"
		timeout_us: string | *"8000000"
	}
	argv: [
		"/usr/local/bin/ffprobe", "-v", "error",
		"-rtsp_transport", config.transport,
		"-timeout", config.timeout_us,
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height",
		"-of", "json",
		config.input,
	]
	output_format: "json"
}
