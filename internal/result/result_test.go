package result_test

import (
	"encoding/json"
	"testing"

	"github.com/mik3dev/fal-cli/internal/result"
)

const fluxOut = `{"images":[{"url":"https://v3.fal.media/files/a.jpeg","width":1024,"height":768,"content_type":"image/jpeg"},{"url":"https://v3.fal.media/files/b.jpeg","content_type":"image/jpeg"}],"timings":{"inference":0.3},"seed":42,"has_nsfw_concepts":[false,false],"prompt":"cat"}`
const klingOut = `{"video":{"url":"https://v3.fal.media/files/v.mp4","content_type":"video/mp4","file_name":"v.mp4","file_size":1234}}`
const elevenOut = `{"audio":{"url":"https://v3.fal.media/files/s.mp3","content_type":"audio/mpeg","file_name":"s.mp3"},"timestamps":[{"text":"hi","timestamp":[0,0.5]}]}`

func TestExtractImagesArray(t *testing.T) {
	m := result.Extract(json.RawMessage(fluxOut))
	if len(m) != 2 || m[0].Key != "images[0]" || m[0].URL != "https://v3.fal.media/files/a.jpeg" || m[0].ContentType != "image/jpeg" || m[1].Key != "images[1]" {
		t.Fatalf("m = %+v", m)
	}
}

func TestExtractSingleVideoFile(t *testing.T) {
	m := result.Extract(json.RawMessage(klingOut))
	if len(m) != 1 || m[0].Key != "video" || m[0].FileName != "v.mp4" || m[0].ContentType != "video/mp4" {
		t.Fatalf("m = %+v", m)
	}
}

func TestExtractAudioAndTimestampsIgnored(t *testing.T) {
	m := result.Extract(json.RawMessage(elevenOut))
	if len(m) != 1 || m[0].Key != "audio" || m[0].URL != "https://v3.fal.media/files/s.mp3" {
		t.Fatalf("m = %+v", m)
	}
}

func TestExtractBareFalURLsUnderURLKeys(t *testing.T) {
	out := `{"video_url":"https://fal.media/x/v.mp4","thumb_urls":["https://v3.fal.run/t1.png","https://example.com/no.png"],"homepage_url":"https://example.com","note":"https://fal.media/ignored.png"}`
	m := result.Extract(json.RawMessage(out))
	if len(m) != 2 || m[0].Key != "video_url" || m[1].Key != "thumb_urls[0]" {
		t.Fatalf("m = %+v", m)
	}
}

func TestExtractDedupes(t *testing.T) {
	out := `{"image":{"url":"https://v3.fal.media/files/a.png","content_type":"image/png"},"image_url":"https://v3.fal.media/files/a.png","garbage":"x","n":null}`
	m := result.Extract(json.RawMessage(out))
	if len(m) != 1 || m[0].Key != "image" {
		t.Fatalf("m = %+v", m)
	}
	if got := result.Extract(json.RawMessage(`not json`)); len(got) != 0 {
		t.Fatalf("invalid json: %+v", got)
	}
}

func TestIDsHaveRequestAndSeed(t *testing.T) {
	raw := json.RawMessage(fluxOut)
	ids := result.IDs("req-1", raw, result.Extract(raw))
	if ids["request_id"] != "req-1" || ids["seed"] != float64(42) {
		t.Fatalf("ids = %v", ids)
	}
	urls, _ := ids["images_urls"].([]string)
	if len(urls) != 2 || ids["images_url"] != nil {
		t.Fatalf("ids = %v", ids)
	}
	one := result.IDs("req-2", json.RawMessage(klingOut), result.Extract(json.RawMessage(klingOut)))
	if one["video_url"] != "https://v3.fal.media/files/v.mp4" || one["seed"] != nil {
		t.Fatalf("one = %v", one)
	}
	single := json.RawMessage(`{"images":[{"url":"https://v3.fal.media/files/a.jpeg","content_type":"image/jpeg"}]}`)
	if s := result.IDs("r", single, result.Extract(single)); s["images_url"] != "https://v3.fal.media/files/a.jpeg" {
		t.Fatalf("single = %v", s)
	}
}
