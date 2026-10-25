package pluginapi

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/png" // image.DecodeConfig reads PNG

	_ "golang.org/x/image/webp" // image.DecodeConfig reads WebP
)

// Plugin icon limits (PROTOCOL.md §5.2, host feature plugin_icon). The host and the plugin test suite use the same rule.
const (
	// MaxCatalogIcon is the largest icon.png attached to a plugin release for the catalog.
	MaxCatalogIcon = 128 << 10
	// MaxEmbeddedIcon is the largest Manifest.icon_png.
	MaxEmbeddedIcon = 32 << 10
	// IconMinSide and IconMaxSide bound the icon's width and height in pixels. The upper bound also stops decompression
	// bombs: a tiny PNG can declare tens of thousands of pixels a browser would then decode.
	IconMinSide = 16
	IconMaxSide = 1024
)

// ErrIconRejected wraps every reason CheckIcon rejects an icon.
var ErrIconRejected = errors.New("plugin icon rejected")

// CheckIcon checks a plugin icon of at most limit bytes and returns its content type, image/png or image/webp. The format
// comes from the file header, never the file name; SVG is never accepted (it can carry scripts and external resources).
// The icon must be square, IconMinSide to IconMaxSide pixels. A rejected icon returns an error wrapping ErrIconRejected that
// says why.
func CheckIcon(data []byte, limit int) (string, error) {
	if len(data) > limit {
		return "", fmt.Errorf("%w: %d bytes, at most %d", ErrIconRejected, len(data), limit)
	}
	var ctype string
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		ctype = "image/png"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		ctype = "image/webp"
	default:
		return "", fmt.Errorf("%w: not a PNG or WebP image", ErrIconRejected)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrIconRejected, err.Error())
	}
	if cfg.Width != cfg.Height || cfg.Width < IconMinSide || cfg.Width > IconMaxSide {
		return "", fmt.Errorf("%w: %dx%d, it must be square, %d to %d pixels", ErrIconRejected, cfg.Width, cfg.Height, IconMinSide, IconMaxSide)
	}
	return ctype, nil
}
