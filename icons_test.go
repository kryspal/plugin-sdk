package pluginapi_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"testing"

	"github.com/kryspal/plugin-sdk"
)

// testWebP is a 16x16 lossless WebP.
const testWebP = "UklGRhwAAABXRUJQVlA4TA8AAAAvD8ADAAcQ0f/+ByKi/wEA"

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// The rule reads the header, not the name: PNG and WebP only, square, 16 to 1024 pixels, within the size limit.
func TestCheckIcon(t *testing.T) {
	icon := pngOf(t, 64, 64)
	webp, err := base64.StdEncoding.DecodeString(testWebP)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		data  []byte
		limit int
		want  string
	}{
		"png 16":          {pngOf(t, 16, 16), pluginapi.MaxEmbeddedIcon, "image/png"},
		"png 64":          {icon, pluginapi.MaxEmbeddedIcon, "image/png"},
		"png 1024":        {pngOf(t, 1024, 1024), pluginapi.MaxCatalogIcon, "image/png"},
		"webp":            {webp, pluginapi.MaxEmbeddedIcon, "image/webp"},
		"svg":             {[]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><script>alert(1)</script></svg>`), pluginapi.MaxCatalogIcon, ""},
		"jpeg":            {[]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), pluginapi.MaxCatalogIcon, ""},
		"png header only": {[]byte("\x89PNG\r\n\x1a\nnot really"), pluginapi.MaxCatalogIcon, ""},
		"riff, not webp":  {[]byte("RIFF\x10\x00\x00\x00WAVEfmt "), pluginapi.MaxCatalogIcon, ""},
		"not square":      {pngOf(t, 32, 16), pluginapi.MaxCatalogIcon, ""},
		"too small":       {pngOf(t, 8, 8), pluginapi.MaxCatalogIcon, ""},
		"too many pixels": {pngOf(t, 2048, 2048), pluginapi.MaxCatalogIcon, ""},
		"too many bytes":  {icon, len(icon) - 1, ""},
		"empty":           {nil, pluginapi.MaxCatalogIcon, ""},
	} {
		got, err := pluginapi.CheckIcon(c.data, c.limit)
		if got != c.want || (c.want == "") != (err != nil) {
			t.Errorf("%s: CheckIcon = %q, %v; want %q", name, got, err, c.want)
		}
		if err != nil && !errors.Is(err, pluginapi.ErrIconRejected) {
			t.Errorf("%s: error %v does not wrap ErrIconRejected", name, err)
		}
	}
}
