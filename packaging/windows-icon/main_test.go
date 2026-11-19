package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func encoded(t *testing.T, level png.CompressionLevel, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: shade, G: 128, B: 64, A: 255})
		}
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: level}).Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestSameImagesIgnoresCompression(t *testing.T) {
	fast := encoded(t, png.BestSpeed, 200)
	small := encoded(t, png.BestCompression, 200)
	if bytes.Equal(fast, small) {
		t.Fatal("测试数据没有产生不同的压缩字节")
	}
	if !sameImages(iconImages(fast), iconImages(small)) {
		t.Fatal("相同像素应通过校验")
	}
}

func TestSameImagesRejectsPixelChanges(t *testing.T) {
	first := encoded(t, png.DefaultCompression, 200)
	second := encoded(t, png.DefaultCompression, 100)
	if sameImages(iconImages(first), iconImages(second)) {
		t.Fatal("像素变化应判定为过期")
	}
}
