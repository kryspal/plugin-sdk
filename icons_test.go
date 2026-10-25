package pluginapi_test

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"testing"

	"github.com/kryspal/plugin-sdk"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCheckIcon(t *testing.T) {
	if ct, err := pluginapi.CheckIcon(pngOf(t, 64, 64), pluginapi.MaxEmbeddedIcon); err != nil || ct != "image/png" {
		t.Fatalf("64x64 PNG = %q, %v; want image/png", ct, err)
	}
	for name, data := range map[string][]byte{
		"not square": pngOf(t, 64, 32),
		"too small":  pngOf(t, 8, 8),
		"too large":  pngOf(t, 2048, 2048),
		"svg":        []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`),
		"broken png": []byte("\x89PNG\r\n\x1a\nnot really"),
		"over limit": pngOf(t, 64, 64),
	} {
		limit := pluginapi.MaxEmbeddedIcon
		if name == "over limit" {
			limit = 10
		}
		if _, err := pluginapi.CheckIcon(data, limit); !errors.Is(err, pluginapi.ErrIconRejected) {
			t.Errorf("%s: err = %v, want ErrIconRejected", name, err)
		}
	}
}
