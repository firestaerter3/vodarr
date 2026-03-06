package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// MediaInfo holds stream metadata extracted by ffprobe.
type MediaInfo struct {
	Duration   float64 // seconds
	VideoCodec string  // ffprobe codec_name e.g. "h264"
	Width      int
	Height     int
	AudioCodec string // ffprobe codec_name e.g. "aac"
	SampleRate int    // e.g. 48000
	Channels   int    // e.g. 2
}

// probeTimeout is the maximum time allowed for ffprobe to run.
const probeTimeout = 15 * time.Second

// ffprobeOutput mirrors the JSON structure returned by ffprobe.
type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type ffprobeStream struct {
	CodecType  string `json:"codec_type"`
	CodecName  string `json:"codec_name"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	SampleRate string `json:"sample_rate"` // string in ffprobe output
	Channels   int    `json:"channels"`
}

type ffprobeFormat struct {
	Duration string `json:"duration"` // string in ffprobe output
}

// Probe runs ffprobe on the given URL and returns extracted MediaInfo.
// Returns an error if ffprobe is not found or the stream is unreachable.
// No fallback defaults are provided — the caller decides what to do on error.
func Probe(ctx context.Context, url string) (*MediaInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		url,
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	return parseFFProbeOutput(out)
}

// parseFFProbeOutput parses raw ffprobe JSON output into MediaInfo.
func parseFFProbeOutput(data []byte) (*MediaInfo, error) {
	var fp ffprobeOutput
	if err := json.Unmarshal(data, &fp); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	info := &MediaInfo{}

	if fp.Format.Duration != "" {
		d, err := strconv.ParseFloat(fp.Format.Duration, 64)
		if err == nil {
			info.Duration = d
		}
	}

	for _, s := range fp.Streams {
		switch s.CodecType {
		case "video":
			if info.VideoCodec == "" {
				info.VideoCodec = s.CodecName
				info.Width = s.Width
				info.Height = s.Height
			}
		case "audio":
			if info.AudioCodec == "" {
				info.AudioCodec = s.CodecName
				if s.SampleRate != "" {
					sr, err := strconv.Atoi(s.SampleRate)
					if err == nil {
						info.SampleRate = sr
					}
				}
				info.Channels = s.Channels
			}
		}
	}

	return info, nil
}

// VideoMKVCodecID maps an ffprobe video codec_name to a Matroska CodecID.
func VideoMKVCodecID(codec string) string {
	switch codec {
	case "h264":
		return "V_MPEG4/ISO/AVC"
	case "hevc", "h265":
		return "V_MPEGH/ISO/HEVC"
	case "mpeg2video":
		return "V_MPEG2"
	case "vp8":
		return "V_VP8"
	case "vp9":
		return "V_VP9"
	case "av1":
		return "V_AV1"
	default:
		return "V_MS/VFW/FOURCC"
	}
}

// defaultProber is the production Prober implementation.
type defaultProber struct{}

func (defaultProber) Probe(ctx context.Context, url string) (*MediaInfo, error) {
	return Probe(ctx, url)
}

// DefaultProber is the production Prober that shells out to ffprobe.
// Use it when wiring up the qbit.Handler in main.
var DefaultProber = defaultProber{}

// AudioMKVCodecID maps an ffprobe audio codec_name to a Matroska CodecID.
func AudioMKVCodecID(codec string) string {
	switch codec {
	case "aac":
		return "A_AAC"
	case "ac3":
		return "A_AC3"
	case "eac3":
		return "A_EAC3"
	case "mp3":
		return "A_MPEG/L3"
	case "dts":
		return "A_DTS"
	case "opus":
		return "A_OPUS"
	case "flac":
		return "A_FLAC"
	case "vorbis":
		return "A_VORBIS"
	default:
		return "A_AAC"
	}
}
