package probe

import (
	"context"
	"testing"
	"time"
)

func TestParseFFProbeOutput(t *testing.T) {
	input := []byte(`{
		"streams": [
			{
				"codec_type": "video",
				"codec_name": "h264",
				"width": 1920,
				"height": 1080
			},
			{
				"codec_type": "audio",
				"codec_name": "aac",
				"sample_rate": "48000",
				"channels": 2
			}
		],
		"format": {
			"duration": "3600.500000"
		}
	}`)

	info, err := parseFFProbeOutput(input)
	if err != nil {
		t.Fatalf("parseFFProbeOutput: %v", err)
	}
	if info.Duration != 3600.5 {
		t.Errorf("Duration = %v, want 3600.5", info.Duration)
	}
	if info.VideoCodec != "h264" {
		t.Errorf("VideoCodec = %q, want h264", info.VideoCodec)
	}
	if info.Width != 1920 {
		t.Errorf("Width = %d, want 1920", info.Width)
	}
	if info.Height != 1080 {
		t.Errorf("Height = %d, want 1080", info.Height)
	}
	if info.AudioCodec != "aac" {
		t.Errorf("AudioCodec = %q, want aac", info.AudioCodec)
	}
	if info.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", info.SampleRate)
	}
	if info.Channels != 2 {
		t.Errorf("Channels = %d, want 2", info.Channels)
	}
}

func TestParseFFProbeOutputVideoOnly(t *testing.T) {
	input := []byte(`{
		"streams": [
			{
				"codec_type": "video",
				"codec_name": "hevc",
				"width": 3840,
				"height": 2160
			}
		],
		"format": {
			"duration": "90.0"
		}
	}`)

	info, err := parseFFProbeOutput(input)
	if err != nil {
		t.Fatalf("parseFFProbeOutput: %v", err)
	}
	if info.VideoCodec != "hevc" {
		t.Errorf("VideoCodec = %q, want hevc", info.VideoCodec)
	}
	if info.AudioCodec != "" {
		t.Errorf("AudioCodec = %q, want empty", info.AudioCodec)
	}
}

func TestParseFFProbeOutputFirstStreamWins(t *testing.T) {
	// When multiple video streams are present, the first one wins.
	input := []byte(`{
		"streams": [
			{"codec_type": "video", "codec_name": "h264", "width": 1920, "height": 1080},
			{"codec_type": "video", "codec_name": "hevc", "width": 3840, "height": 2160}
		],
		"format": {"duration": "60.0"}
	}`)

	info, err := parseFFProbeOutput(input)
	if err != nil {
		t.Fatalf("parseFFProbeOutput: %v", err)
	}
	if info.VideoCodec != "h264" {
		t.Errorf("VideoCodec = %q, want h264 (first stream)", info.VideoCodec)
	}
}

func TestParseFFProbeOutputInvalidJSON(t *testing.T) {
	_, err := parseFFProbeOutput([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestVideoMKVCodecID(t *testing.T) {
	cases := []struct{ codec, want string }{
		{"h264", "V_MPEG4/ISO/AVC"},
		{"hevc", "V_MPEGH/ISO/HEVC"},
		{"h265", "V_MPEGH/ISO/HEVC"},
		{"mpeg2video", "V_MPEG2"},
		{"vp8", "V_VP8"},
		{"vp9", "V_VP9"},
		{"av1", "V_AV1"},
		{"unknown_codec", "V_MS/VFW/FOURCC"},
	}
	for _, c := range cases {
		got := VideoMKVCodecID(c.codec)
		if got != c.want {
			t.Errorf("VideoMKVCodecID(%q) = %q, want %q", c.codec, got, c.want)
		}
	}
}

func TestAudioMKVCodecID(t *testing.T) {
	cases := []struct{ codec, want string }{
		{"aac", "A_AAC"},
		{"ac3", "A_AC3"},
		{"eac3", "A_EAC3"},
		{"mp3", "A_MPEG/L3"},
		{"dts", "A_DTS"},
		{"opus", "A_OPUS"},
		{"flac", "A_FLAC"},
		{"vorbis", "A_VORBIS"},
		{"unknown", "A_AAC"},
	}
	for _, c := range cases {
		got := AudioMKVCodecID(c.codec)
		if got != c.want {
			t.Errorf("AudioMKVCodecID(%q) = %q, want %q", c.codec, got, c.want)
		}
	}
}

func TestProbeTimeout(t *testing.T) {
	// Use a context that's already cancelled — ffprobe should fail quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	// Wait for cancellation.
	<-ctx.Done()

	_, err := Probe(ctx, "http://192.0.2.1/nonexistent") // TEST-NET, unreachable
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}
