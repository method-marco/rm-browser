package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"
)

func TestConvertImage(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			src.Set(x, y, color.NRGBA{R: uint8(x), G: 100, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, src)
	for _, grey := range []bool{false, true} {
		out, w, h, err := Convert(buf.Bytes(), 860, 1400, grey)
		if err != nil {
			t.Fatal(err)
		}
		if w != 860 || h != 430 {
			t.Errorf("size %dx%d", w, h)
		}
		img, format, err := image.Decode(bytes.NewReader(out))
		if err != nil || format != "jpeg" {
			t.Fatalf("decode: %v %s", err, format)
		}
		_, isGrey := img.(*image.Gray)
		if isGrey != grey {
			t.Errorf("grey=%v: got %T", grey, img)
		}
		if !grey {
			r, g, b, _ := img.At(430, 215).RGBA()
			if r == g && g == b {
				t.Errorf("colour lost: %d %d %d", r, g, b)
			}
		}
	}

	tiny := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	buf.Reset()
	_ = png.Encode(&buf, tiny)
	if _, _, _, err := Convert(buf.Bytes(), 860, 1400, false); err == nil {
		t.Error("tracking pixel should be dropped")
	}
	if _, _, _, err := Convert([]byte("<svg/>"), 860, 1400, false); err == nil {
		t.Error("garbage should fail")
	}
}

func TestFitSize(t *testing.T) {
	if w, h := FitSize(200, 100, 860, 1400); w != 300 || h != 150 {
		t.Errorf("small image: %dx%d", w, h)
	}
	if w, h := FitSize(1000, 4000, 860, 1400); w != 350 || h != 1400 {
		t.Errorf("tall image: %dx%d", w, h)
	}
}

// Pictures are decoded in parallel only while their pixels fit in the
// budget; one that alone is too big is skipped.
func TestDecodeBudget(t *testing.T) {
	if n := DecodeBytes(image.Config{ColorModel: color.NRGBA64Model, Width: 6000, Height: 4000}); n <= decodeBudget {
		t.Fatalf("16-bit 24 MP fits? %d", n)
	}
	b := NewMemBudget(100)
	b.Acquire(60)
	done := make(chan bool)
	go func() { b.Acquire(60); done <- true }()
	select {
	case <-done:
		t.Fatal("over budget")
	case <-time.After(50 * time.Millisecond):
	}
	b.Release(60)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("never started")
	}
	// a 16-bit PNG of 5000 x 5000 needs 200 MB to decode: skipped
	img := image.NewNRGBA64(image.Rect(0, 0, 5000, 5000))
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	if _, _, _, err := Convert(buf.Bytes(), 860, 1400, false); err == nil {
		t.Error("200 MB decode accepted")
	}
}
