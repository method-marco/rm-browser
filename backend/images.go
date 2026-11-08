package main

// Images are decoded here, inside the sandbox, by Go's memory-safe
// decoders, then scaled to fit the screen, turned grey and stored as new
// JPEG files. The frontend (running in xochitl, as root) only ever opens
// these re-encoded files, never what the site sent.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	minImageSide   = 48         // smaller images are icons or tracking pixels
	maxImagePixels = 50_000_000 // decompression bombs
)

// prepareImage downloads src and stores it scaled to fit maxW x maxH.
// It returns the absolute file path and the shown size.
func (a *App) prepareImage(ctx context.Context, src string, maxW, maxH int) (string, int, int, bool) {
	if maxW < 100 || maxH < 100 {
		return "", 0, 0, false
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", src, maxW, maxH)))
	name := "img/" + hex.EncodeToString(sum[:12]) + ".jpg"
	if b, err := a.store.cache.ReadFile(name); err == nil {
		if cfg, err := jpeg.DecodeConfig(bytes.NewReader(b)); err == nil {
			return a.store.cachePath(name), cfg.Width, cfg.Height, true
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := newRequest(ctx, http.MethodGet, src, nil)
	if err != nil {
		return "", 0, 0, false
	}
	req.Header.Set("Accept", "image/webp,image/png,image/jpeg,image/gif;q=0.9,*/*;q=0.5")
	f, err := fetch(ctx, a.client(), req, maxImageBytes)
	if err != nil {
		return "", 0, 0, false
	}
	out, w, h, err := convertImage(f.Body, maxW, maxH)
	if err != nil {
		return "", 0, 0, false
	}
	if err := a.store.cache.MkdirAll("img", 0o700); err != nil {
		return "", 0, 0, false
	}
	if err := a.store.cache.WriteFile(name, out, 0o644); err != nil {
		return "", 0, 0, false
	}
	return a.store.cachePath(name), w, h, true
}

func convertImage(data []byte, maxW, maxH int) ([]byte, int, int, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	if cfg.Width < minImageSide || cfg.Height < minImageSide {
		return nil, 0, 0, fmt.Errorf("image too small")
	}
	if cfg.Width*cfg.Height > maxImagePixels {
		return nil, 0, 0, fmt.Errorf("image too large")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	b := src.Bounds()
	w, h := fitSize(b.Dx(), b.Dy(), maxW, maxH)
	dst := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 88}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), w, h, nil
}

// fitSize scales (w, h) down to fit (maxW, maxH), keeping the aspect ratio.
// Small images are shown at 1.5x their size at most, so that pictures meant
// for phones aren't tiny on the tablet's dense screen, nor blown up.
func fitSize(w, h, maxW, maxH int) (int, int) {
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
