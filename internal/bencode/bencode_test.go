package bencode

import (
	"testing"
)

func TestEncodeString(t *testing.T) {
	got, err := Encode("spam")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "4:spam" {
		t.Errorf("Encode string = %q, want %q", got, "4:spam")
	}
}

func TestEncodeEmptyString(t *testing.T) {
	got, err := Encode("")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "0:" {
		t.Errorf("Encode empty string = %q, want %q", got, "0:")
	}
}

func TestEncodeInt(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, "i0e"},
		{42, "i42e"},
		{-3, "i-3e"},
	} {
		got, err := Encode(tc.in)
		if err != nil {
			t.Fatalf("Encode(%d): %v", tc.in, err)
		}
		if string(got) != tc.want {
			t.Errorf("Encode(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEncodeInt64(t *testing.T) {
	got, err := Encode(int64(262144))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "i262144e" {
		t.Errorf("Encode int64 = %q, want %q", got, "i262144e")
	}
}

func TestEncodeDict(t *testing.T) {
	// Keys must be sorted in the output
	d := map[string]interface{}{"b": "bar", "a": "foo"}
	got, err := Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	want := "d1:a3:foo1:b3:bare"
	if string(got) != want {
		t.Errorf("Encode dict = %q, want %q", got, want)
	}
}

func TestEncodeBytes(t *testing.T) {
	got, err := Encode([]byte{0, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	want := "3:\x00\x01\x02"
	if string(got) != want {
		t.Errorf("Encode bytes = %q, want %q", got, want)
	}
}

func TestDecodeString(t *testing.T) {
	val, err := Decode([]byte("4:spam"))
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := val.(string); !ok || s != "spam" {
		t.Errorf("Decode string = %v (%T), want \"spam\"", val, val)
	}
}

func TestDecodeInteger(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"i42e", 42},
		{"i-3e", -3},
		{"i0e", 0},
	} {
		val, err := Decode([]byte(tc.in))
		if err != nil {
			t.Fatalf("Decode(%q): %v", tc.in, err)
		}
		n, ok := val.(int64)
		if !ok {
			t.Fatalf("Decode(%q): want int64, got %T", tc.in, val)
		}
		if n != tc.want {
			t.Errorf("Decode(%q) = %d, want %d", tc.in, n, tc.want)
		}
	}
}

func TestDecodeList(t *testing.T) {
	val, err := Decode([]byte("l4:spami42ee"))
	if err != nil {
		t.Fatal(err)
	}
	list, ok := val.([]interface{})
	if !ok {
		t.Fatalf("want []interface{}, got %T", val)
	}
	if len(list) != 2 {
		t.Fatalf("list length = %d, want 2", len(list))
	}
	if list[0].(string) != "spam" {
		t.Errorf("list[0] = %v, want spam", list[0])
	}
	if list[1].(int64) != 42 {
		t.Errorf("list[1] = %v, want 42", list[1])
	}
}

func TestDecodeDict(t *testing.T) {
	val, err := Decode([]byte("d3:bar4:spam3:fooi42ee"))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := val.(map[string]interface{})
	if !ok {
		t.Fatalf("want map, got %T", val)
	}
	if d["bar"] != "spam" {
		t.Errorf("d[bar] = %v, want spam", d["bar"])
	}
	if d["foo"].(int64) != 42 {
		t.Errorf("d[foo] = %v, want 42", d["foo"])
	}
}

func TestRoundTripTorrent(t *testing.T) {
	// Simulate the torrent structure created by handleGet
	pieces := string(make([]byte, 20))
	info := map[string]interface{}{
		"length":       1,
		"name":         "vodarr-series-12345",
		"piece length": 262144,
		"pieces":       pieces,
	}
	orig := map[string]interface{}{
		"comment": `{"xtream_id":12345,"type":"series"}`,
		"info":    info,
	}

	encoded, err := Encode(orig)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	d, ok := decoded.(map[string]interface{})
	if !ok {
		t.Fatalf("decoded not a dict, got %T", decoded)
	}
	if d["comment"] != `{"xtream_id":12345,"type":"series"}` {
		t.Errorf("comment = %q, unexpected", d["comment"])
	}
	infoDecoded, ok := d["info"].(map[string]interface{})
	if !ok {
		t.Fatalf("info not a dict, got %T", d["info"])
	}
	if infoDecoded["name"] != "vodarr-series-12345" {
		t.Errorf("info.name = %q, want vodarr-series-12345", infoDecoded["name"])
	}
	if infoDecoded["pieces"] != pieces {
		t.Error("info.pieces mismatch after round-trip")
	}
}

func TestInfoHashConsistency(t *testing.T) {
	// Encoding the same dict twice must produce identical bytes
	// (required for consistent SHA1 info-hash computation).
	pieces := string(make([]byte, 20))
	info := map[string]interface{}{
		"length":       1,
		"name":         "vodarr-movie-42",
		"piece length": 262144,
		"pieces":       pieces,
	}
	b1, err := Encode(info)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := Encode(info)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Error("Encode is not deterministic for identical input")
	}
}
