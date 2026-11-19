package main

// Pictures of web pages: downloaded here, then converted by
// internal/imaging (decoded inside the sandbox, re-encoded as JPEG).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/jpeg"
	"net/http"
	"time"

	"github.com/method-marco/rm-browser/backend/internal/imaging"
)

// prepareImage downloads src and stores it scaled to fit maxW x maxH.
// It returns the absolute file path and the shown size.
func (a *App) prepareImage(ctx context.Context, src string, maxW, maxH int, grey bool) (string, int, int, bool) {
	if maxW < 100 || maxH < 100 {
		return "", 0, 0, false
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%v", src, maxW, maxH, grey)))
	name := "img/" + hex.EncodeToString(sum[:12]) + ".jpg"
	if b, err := a.store.Cache.ReadFile(name); err == nil {
		if cfg, err := jpeg.DecodeConfig(bytes.NewReader(b)); err == nil {
			now := time.Now()
			_ = a.store.Cache.Chtimes(name, now, now) // recently used: pruned last
			return a.store.CachePath(name), cfg.Width, cfg.Height, true
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if !allowHTTP.Load() {
		src = httpsURL(src) // HTTPS-only: an insecure image is tried securely or not at all
	}
	req, err := newRequest(ctx, http.MethodGet, src, nil)
	if err != nil {
		return "", 0, 0, false
	}
	req.Header.Set("Accept", "image/webp,image/png,image/jpeg,image/gif;q=0.9,*/*;q=0.5")
	f, err := fetch(ctx, a.client(), req, maxImageBytes)
	if err != nil {
		return "", 0, 0, false
	}
	out, w, h, err := imaging.Convert(f.Body, maxW, maxH, grey)
	if err != nil {
		return "", 0, 0, false
	}
	if err := a.store.Cache.MkdirAll("img", 0o700); err != nil {
		return "", 0, 0, false
	}
	if err := a.store.Cache.WriteFile(name, out, 0o644); err != nil {
		return "", 0, 0, false
	}
	return a.store.CachePath(name), w, h, true
}
