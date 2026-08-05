// rtsp_probe: verify a live RTSP stream actually delivers video, via ffmpeg.
//
// honey overrides the image entrypoint with the honey-plugin-init shim and execs
// argv[0] directly, so argv[0] MUST be the absolute binary path
// (/usr/local/bin/ffmpeg, /usr/local/bin/ffprobe), not bare "ffmpeg"/"ffprobe".
//
// #Config fields are required-with-default (`| *…`), never optional (`?`):
// evalAction decodes the unified config back to a plain map to apply defaults,
// and CUE omits uninstantiated optional fields, which would drop them from argv.

// check: read the first VIDEO packet from the RTSP url and exit 0 only if one
// arrives. A dead / unreachable / silent (frozen) stream makes ffprobe exit
// non-zero (connect error, or the -timeout socket read timeout with no data);
// honey surfaces that as a step failure. This is the real "is it returning
// video" test.
//
// It reads a single packet with ffprobe — no decode, no output — deliberately:
// a real camera may not deliver a keyframe / SPS fast enough for ffmpeg to
// determine the frame size, and a decode- or `-c copy`-to-null probe then fails
// with "Could not find codec parameters ... unspecified size" / "Could not write
// header" even though the stream is live. Any video packet arriving proves the
// camera is streaming, with no codec-parameter dependency. `-select_streams v:0`
// makes it video-specific.
actions: check: {
	#Config: {
		input:      string
		transport:  string | *"tcp"      // rtsp_transport: tcp (reliable) or udp
		timeout_us: string | *"15000000" // rtsp socket I/O timeout, microseconds (15s)
	}
	// -timeout is the RTSP demuxer's socket-timeout option (microseconds). Do
	// NOT use -rw_timeout here: the rtsp demuxer rejects it once the input
	// actually opens ("Option rw_timeout not found"), and -stimeout was removed
	// in ffmpeg 8.
	argv: [
		"/usr/local/bin/ffprobe", "-v", "error",
		"-rtsp_transport", config.transport,
		"-timeout", config.timeout_us,
		"-select_streams", "v:0",
		"-show_entries", "packet=pts",
		"-read_intervals", "%+#1",
		"-of", "csv=p=0",
		"-i", config.input,
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
