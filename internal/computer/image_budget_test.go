package computer

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"math/rand"
	"testing"
)

func TestLargeImageFitsBridgeBudgetAndKeepsNativeMapping(t *testing.T) {
	input := image.NewRGBA(image.Rect(0, 0, 128, 96))
	random := rand.New(rand.NewSource(1))
	_, _ = random.Read(input.Pix)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, input); err != nil {
		t.Fatal(err)
	}
	original := Snapshot{PNG: encoded.Bytes(), Width: 128, Height: 96, Transform: [6]float64{2, 0, 0, 2, -100, 10}}
	fitted, err := fitImage(context.Background(), original, 4096)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(fitted.PNG))
	if err != nil {
		t.Fatal("PNG was truncated", err)
	}
	if len(fitted.PNG) > 4096 || decoded.Bounds().Dx() != fitted.Width || decoded.Bounds().Dy() != fitted.Height {
		t.Fatal("wrong actual image dimensions")
	}
	if float64(fitted.Width)*fitted.Transform[0] != 256 || float64(fitted.Height)*fitted.Transform[3] != 192 || fitted.Transform[4] != -100 || fitted.Transform[5] != 10 {
		t.Fatal("resizing changed the native coordinate mapping")
	}
}
