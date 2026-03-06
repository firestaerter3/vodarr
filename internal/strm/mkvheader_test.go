package strm

import (
	"bytes"
	"testing"

	"github.com/vodarr/vodarr/internal/probe"
)

// ebmlMagic is the first four bytes of every valid EBML file.
var ebmlMagic = []byte{0x1A, 0x45, 0xDF, 0xA3}

func TestBuildMKVHeaderEBMLMagic(t *testing.T) {
	info := &probe.MediaInfo{
		Duration:   3600,
		VideoCodec: "h264",
		Width:      1920,
		Height:     1080,
		AudioCodec: "aac",
		SampleRate: 48000,
		Channels:   2,
	}
	header := BuildMKVHeader(info)

	if len(header) < 4 {
		t.Fatalf("header too short: %d bytes", len(header))
	}
	if !bytes.Equal(header[:4], ebmlMagic) {
		t.Errorf("EBML magic = %X, want %X", header[:4], ebmlMagic)
	}
}

func TestBuildMKVHeaderDocType(t *testing.T) {
	info := &probe.MediaInfo{VideoCodec: "h264", Width: 1920, Height: 1080}
	header := BuildMKVHeader(info)

	if !bytes.Contains(header, []byte("matroska")) {
		t.Error("header does not contain DocType string 'matroska'")
	}
}

func TestBuildMKVHeaderVideoCodecString(t *testing.T) {
	cases := []struct {
		codec string
		want  string
	}{
		{"h264", "V_MPEG4/ISO/AVC"},
		{"hevc", "V_MPEGH/ISO/HEVC"},
		{"vp9", "V_VP9"},
	}
	for _, c := range cases {
		info := &probe.MediaInfo{VideoCodec: c.codec, Width: 1280, Height: 720}
		header := BuildMKVHeader(info)
		if !bytes.Contains(header, []byte(c.want)) {
			t.Errorf("codec %q: header does not contain MKV CodecID %q", c.codec, c.want)
		}
	}
}

func TestBuildMKVHeaderAudioCodecString(t *testing.T) {
	cases := []struct {
		codec string
		want  string
	}{
		{"aac", "A_AAC"},
		{"ac3", "A_AC3"},
		{"mp3", "A_MPEG/L3"},
	}
	for _, c := range cases {
		info := &probe.MediaInfo{
			VideoCodec: "h264",
			Width:      1920,
			Height:     1080,
			AudioCodec: c.codec,
			SampleRate: 48000,
			Channels:   2,
		}
		header := BuildMKVHeader(info)
		if !bytes.Contains(header, []byte(c.want)) {
			t.Errorf("codec %q: header does not contain MKV CodecID %q", c.codec, c.want)
		}
	}
}

func TestBuildMKVHeaderDuration(t *testing.T) {
	info := &probe.MediaInfo{
		Duration:   1800.0, // 30 min
		VideoCodec: "h264",
		Width:      1920,
		Height:     1080,
	}
	header := BuildMKVHeader(info)

	// Duration element ID is 0x44 0x89; it must appear in the header.
	durationID := []byte{0x44, 0x89}
	if !bytes.Contains(header, durationID) {
		t.Error("header does not contain Duration element (0x44 0x89)")
	}
}

func TestBuildMKVHeaderSizeSanity(t *testing.T) {
	info := &probe.MediaInfo{
		Duration:   3600,
		VideoCodec: "h264",
		Width:      1920,
		Height:     1080,
		AudioCodec: "aac",
		SampleRate: 48000,
		Channels:   2,
	}
	header := BuildMKVHeader(info)

	if len(header) < 150 {
		t.Errorf("header too small: %d bytes (want >= 150)", len(header))
	}
	if len(header) > 500 {
		t.Errorf("header too large: %d bytes (want <= 500)", len(header))
	}
}

func TestBuildMKVHeaderNilInfo(t *testing.T) {
	// nil info must produce a valid (minimal) header — no panic.
	header := BuildMKVHeader(nil)

	if len(header) < 4 {
		t.Fatalf("nil-info header too short: %d bytes", len(header))
	}
	if !bytes.Equal(header[:4], ebmlMagic) {
		t.Errorf("nil-info EBML magic = %X, want %X", header[:4], ebmlMagic)
	}
	if !bytes.Contains(header, []byte("matroska")) {
		t.Error("nil-info header does not contain 'matroska'")
	}
}

func TestBuildMKVHeaderVideoOnly(t *testing.T) {
	info := &probe.MediaInfo{VideoCodec: "vp9", Width: 1280, Height: 720}
	header := BuildMKVHeader(info)

	if !bytes.Contains(header, []byte("V_VP9")) {
		t.Error("header missing video codec")
	}
	// No audio codec — A_AAC should NOT appear
	if bytes.Contains(header, []byte("A_")) {
		t.Error("header should not contain audio codec entry when AudioCodec is empty")
	}
}
