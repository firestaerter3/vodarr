package strm

import (
	"encoding/binary"
	"math"

	"github.com/vodarr/vodarr/internal/probe"
)

// BuildMKVHeader constructs a valid EBML/Matroska binary header containing the
// stream metadata from info. The returned bytes can be written to the start of
// a .mkv file so that real ffprobe/mediainfo tools can read codec, resolution,
// and duration without accessing the full stream.
//
// Structure:
//
//	EBML Header — DocType "matroska"
//	Segment (unknown size)
//	  Info — TimestampScale + Duration + app strings
//	  Tracks — video track + audio track (if present)
func BuildMKVHeader(info *probe.MediaInfo) []byte {
	ebmlHeader := buildEBMLHeader()
	segment := buildSegment(info)
	out := make([]byte, 0, len(ebmlHeader)+len(segment))
	out = append(out, ebmlHeader...)
	out = append(out, segment...)
	return out
}

// EBML element IDs (raw bytes, marker bits included per the EBML spec).
var (
	idEBML               = []byte{0x1A, 0x45, 0xDF, 0xA3}
	idEBMLVersion        = []byte{0x42, 0x86}
	idEBMLReadVersion    = []byte{0x42, 0xF7}
	idEBMLMaxIDLength    = []byte{0x42, 0xF2}
	idEBMLMaxSizeLength  = []byte{0x42, 0xF3}
	idDocType            = []byte{0x42, 0x82}
	idDocTypeVersion     = []byte{0x42, 0x87}
	idDocTypeReadVersion = []byte{0x42, 0x85}

	idSegment        = []byte{0x18, 0x53, 0x80, 0x67}
	idInfo           = []byte{0x15, 0x49, 0xA9, 0x66}
	idTimestampScale = []byte{0x2A, 0xD7, 0xB1}
	idDuration       = []byte{0x44, 0x89}
	idMuxingApp      = []byte{0x4D, 0x80}
	idWritingApp     = []byte{0x57, 0x41}

	idTracks     = []byte{0x16, 0x54, 0xAE, 0x6B}
	idTrackEntry = []byte{0xAE}
	idTrackNumber = []byte{0xD7}
	idTrackUID   = []byte{0x73, 0xC5}
	idTrackType  = []byte{0x83}
	idFlagEnabled = []byte{0xB9}
	idFlagDefault = []byte{0x88}
	idCodecID    = []byte{0x86}

	idVideo       = []byte{0xE0}
	idPixelWidth  = []byte{0xB0}
	idPixelHeight = []byte{0xBA}

	idAudio       = []byte{0xE1}
	idSamplingFreq = []byte{0xB5}
	idChannels    = []byte{0x9F}

	idCluster         = []byte{0x1F, 0x43, 0xB6, 0x75}
	idClusterTimestamp = []byte{0xE7}
)

// vint encodes n as an EBML variable-length integer (data-size encoding).
// The leading marker bit is set; values are limited to 2^(7*width)-2.
func vint(n uint64) []byte {
	switch {
	case n < 0x7F:
		return []byte{byte(0x80 | n)}
	case n < 0x3FFF:
		return []byte{byte(0x40 | (n >> 8)), byte(n)}
	case n < 0x1FFFFF:
		return []byte{byte(0x20 | (n >> 16)), byte(n >> 8), byte(n)}
	case n < 0x0FFFFFFF:
		return []byte{byte(0x10 | (n >> 24)), byte(n >> 16), byte(n >> 8), byte(n)}
	default:
		// 5-byte VINT for very large sizes
		return []byte{
			byte(0x08 | (n >> 32)),
			byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n),
		}
	}
}

// elem encodes: id bytes + VINT(len(data)) + data.
func mkvElem(id, data []byte) []byte {
	sz := vint(uint64(len(data)))
	out := make([]byte, 0, len(id)+len(sz)+len(data))
	out = append(out, id...)
	out = append(out, sz...)
	out = append(out, data...)
	return out
}

func uint8Elem(id []byte, v uint8) []byte { return mkvElem(id, []byte{v}) }
func strElem(id []byte, s string) []byte  { return mkvElem(id, []byte(s)) }

// uintMinimal encodes v as the minimal number of big-endian bytes.
func uintMinimal(v uint64) []byte {
	switch {
	case v <= 0xFF:
		return []byte{byte(v)}
	case v <= 0xFFFF:
		return []byte{byte(v >> 8), byte(v)}
	case v <= 0xFFFFFF:
		return []byte{byte(v >> 16), byte(v >> 8), byte(v)}
	case v <= 0xFFFFFFFF:
		return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	default:
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, v)
		for i, x := range b {
			if x != 0 {
				return b[i:]
			}
		}
		return []byte{0}
	}
}

func uintElem(id []byte, v uint64) []byte { return mkvElem(id, uintMinimal(v)) }

// float64Elem encodes v as an 8-byte IEEE 754 double (big-endian).
func float64Elem(id []byte, v float64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, math.Float64bits(v))
	return mkvElem(id, b)
}

func buildEBMLHeader() []byte {
	body := mkvConcat(
		uintElem(idEBMLVersion, 1),
		uintElem(idEBMLReadVersion, 1),
		uintElem(idEBMLMaxIDLength, 4),
		uintElem(idEBMLMaxSizeLength, 8),
		strElem(idDocType, "matroska"),
		uintElem(idDocTypeVersion, 4),
		uintElem(idDocTypeReadVersion, 2),
	)
	return mkvElem(idEBML, body)
}

func buildSegment(info *probe.MediaInfo) []byte {
	segBody := mkvConcat(
		buildInfo(info),
		buildTracks(info),
	)
	// A minimal Cluster (Timestamp=0, no blocks) makes ffprobe confirm the
	// streams are real and report codec/resolution/duration from Info+Tracks.
	// Without a Cluster, ffprobe returns empty JSON even though it parsed the
	// Tracks element correctly.
	clusterBody := uintElem(idClusterTimestamp, 0)
	segBody = append(segBody, mkvElem(idCluster, clusterBody)...)

	// Use a known (exact) Segment size so ffprobe stops reading after our
	// header data and never tries to parse the sparse 0x00 padding beyond it.
	return mkvElem(idSegment, segBody)
}

// defaultDurationSecs is used when the probe returns no duration (e.g. HLS
// streams where the manifest doesn't report a total length). 45 minutes is
// safely above Sonarr's sample-detection threshold (~20 min for TV episodes).
const defaultDurationSecs = 45 * 60

func buildInfo(info *probe.MediaInfo) []byte {
	// TimestampScale = 1 000 000 ns means each timestamp unit is 1 ms.
	body := uintElem(idTimestampScale, 1000000)
	dur := 0.0
	if info != nil {
		dur = info.Duration
	}
	if dur <= 0 {
		dur = defaultDurationSecs
	}
	// Duration is expressed in TimestampScale units (milliseconds).
	body = append(body, float64Elem(idDuration, dur*1000)...)
	body = append(body, strElem(idMuxingApp, "vodarr")...)
	body = append(body, strElem(idWritingApp, "vodarr")...)
	return mkvElem(idInfo, body)
}

func buildTracks(info *probe.MediaInfo) []byte {
	// Work with a local copy so we can fill in defaults without mutating the caller's struct.
	var effective probe.MediaInfo
	if info != nil {
		effective = *info
	}
	// Always emit at least a video track so Sonarr recognises the stub as video.
	if effective.VideoCodec == "" {
		effective.VideoCodec = "h264"
		effective.Width = 1920
		effective.Height = 1080
	}
	// Always emit an audio track — Sonarr rejects files with "No audio tracks detected".
	if effective.AudioCodec == "" {
		effective.AudioCodec = "aac"
		effective.Channels = 2
		effective.SampleRate = 48000
	}
	tracks := append(buildVideoTrack(&effective), buildAudioTrack(&effective)...)
	return mkvElem(idTracks, tracks)
}

func buildVideoTrack(info *probe.MediaInfo) []byte {
	codecID := probe.VideoMKVCodecID(info.VideoCodec)

	videoBody := mkvConcat(
		uintElem(idPixelWidth, uint64(info.Width)),
		uintElem(idPixelHeight, uint64(info.Height)),
	)

	body := mkvConcat(
		uintElem(idTrackNumber, 1),
		uintElem(idTrackUID, 1),
		uint8Elem(idTrackType, 1), // 1 = video
		uint8Elem(idFlagEnabled, 1),
		uint8Elem(idFlagDefault, 1),
		strElem(idCodecID, codecID),
		mkvElem(idVideo, videoBody),
	)
	return mkvElem(idTrackEntry, body)
}

func buildAudioTrack(info *probe.MediaInfo) []byte {
	codecID := probe.AudioMKVCodecID(info.AudioCodec)

	var audioBody []byte
	if info.SampleRate > 0 {
		// SamplingFrequency in Matroska is a 4-byte IEEE 754 float.
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, math.Float32bits(float32(info.SampleRate)))
		audioBody = append(audioBody, mkvElem(idSamplingFreq, b)...)
	}
	if info.Channels > 0 {
		audioBody = append(audioBody, uintElem(idChannels, uint64(info.Channels))...)
	}

	body := mkvConcat(
		uintElem(idTrackNumber, 2),
		uintElem(idTrackUID, 2),
		uint8Elem(idTrackType, 2), // 2 = audio
		uint8Elem(idFlagEnabled, 1),
		uint8Elem(idFlagDefault, 1),
		strElem(idCodecID, codecID),
	)
	if len(audioBody) > 0 {
		body = append(body, mkvElem(idAudio, audioBody)...)
	}
	return mkvElem(idTrackEntry, body)
}

func mkvConcat(parts ...[]byte) []byte {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	out := make([]byte, 0, total)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
