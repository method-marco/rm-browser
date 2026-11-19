// Package imaging decodes pictures with Go's memory-safe decoders, inside
// the sandbox, and writes them out as new JPEG files sized for the screen
// (grey if the user wants that). The frontend (running in xochitl, as
// root) only ever opens these re-encoded files, never the original bytes.
package imaging

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"sync"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	MinImageSide   = 48         // smaller images are icons or tracking pixels
	maxImagePixels = 50_000_000 // decompression bombs
	// decoding takes width x height x 4 bytes (8 for 16-bit PNGs): pictures
	// are decoded in parallel only while their pixels fit in this together
	// (four 50-megapixel photos at once once took the backend to 1.5 GB and
	// the tablet into swap); a larger one is skipped
	decodeBudget = 160 << 20
)

// MemBudget lets work start while its bytes fit in what is left.
type MemBudget struct {
	mu   sync.Mutex
	cond *sync.Cond
	left int64
}

// NewMemBudget makes a budget of n bytes.
func NewMemBudget(n int64) *MemBudget {
	b := &MemBudget{left: n}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *MemBudget) Acquire(n int64) {
	b.mu.Lock()
	for b.left < n {
		b.cond.Wait()
	}
	b.left -= n
	b.mu.Unlock()
}

func (b *MemBudget) Release(n int64) {
	b.mu.Lock()
	b.left += n
	b.mu.Unlock()
	b.cond.Broadcast()
}

var decodeMem = NewMemBudget(decodeBudget)

// DecodeBytes estimates the memory image.Decode needs for cfg.
func DecodeBytes(cfg image.Config) int64 {
	per := int64(4)
	switch cfg.ColorModel {
	case color.RGBA64Model, color.NRGBA64Model:
		per = 8
	case color.Gray16Model:
		per = 2
	}
	return int64(cfg.Width) * int64(cfg.Height) * per
}

// Convert decodes data and encodes it as JPEG, at most maxW x maxH.
func Convert(data []byte, maxW, maxH int, grey bool) ([]byte, int, int, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	if cfg.Width < MinImageSide || cfg.Height < MinImageSide {
		return nil, 0, 0, fmt.Errorf("image too small")
	}
	need := DecodeBytes(cfg)
	if cfg.Width*cfg.Height > maxImagePixels || need > decodeBudget {
		return nil, 0, 0, fmt.Errorf("image too large")
	}
	decodeMem.Acquire(need)
	defer decodeMem.Release(need)
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	b := src.Bounds()
	w, h := FitSize(b.Dx(), b.Dy(), maxW, maxH)
	var dst draw.Image = image.NewRGBA(image.Rect(0, 0, w, h)) // the Paper Pro has a colour screen
	if grey {
		dst = image.NewGray(image.Rect(0, 0, w, h))
	}
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 88}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), w, h, nil
}

// FitSize scales (w, h) down to fit (maxW, maxH), keeping the aspect ratio.
// Small images are shown at 1.5x their size at most, so that pictures meant
// for phones aren't tiny on the tablet's dense screen, nor blown up.
func FitSize(w, h, maxW, maxH int) (int, int) {
	scale := 1.5
	if s := float64(maxW) / float64(w); s < scale {
		scale = s
	}
	if s := float64(maxH) / float64(h); s < scale {
		scale = s
	}
	nw, nh := int(float64(w)*scale+0.5), int(float64(h)*scale+0.5)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return nw, nh
}
