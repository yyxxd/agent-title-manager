package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func encodeImage(t *testing.T, level png.CompressionLevel, img image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: level}).Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func encoded(t *testing.T, level png.CompressionLevel, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: shade, G: 128, B: 64, A: 255})
		}
	}
	return encodeImage(t, level, img)
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

func TestSameImagesNormalizesColorTypes(t *testing.T) {
	palette := color.Palette{
		color.RGBA{R: 200, G: 40, B: 10, A: 255},
		color.RGBA{R: 10, G: 90, B: 220, A: 255},
	}
	paletted := image.NewPaletted(image.Rect(0, 0, 16, 16), palette)
	rgba := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			index := uint8((x + y) % 2)
			paletted.SetColorIndex(x, y, index)
			rgba.Set(x, y, palette[index])
		}
	}
	first := encodeImage(t, png.DefaultCompression, paletted)
	second := encodeImage(t, png.DefaultCompression, rgba)
	if !sameImages(iconImages(first), iconImages(second)) {
		t.Fatal("相同像素的不同颜色模型应通过校验")
	}
}

func TestSameImagesRejectsPixelChanges(t *testing.T) {
	first := encoded(t, png.DefaultCompression, 200)
	second := encoded(t, png.DefaultCompression, 100)
	if sameImages(iconImages(first), iconImages(second)) {
		t.Fatal("像素变化应判定为过期")
	}
}

func TestContainerHeaderRejectsWrongArchitecture(t *testing.T) {
	if !sameContainer("icon_windows_amd64.syso", []byte{0x64, 0x86}, []byte{0x64, 0x86, 0x00}) {
		t.Fatal("相同架构应通过")
	}
	if sameContainer("icon_windows_amd64.syso", []byte{0x64, 0x86}, []byte{0x64, 0xAA}) {
		t.Fatal("架构变化应判定为过期")
	}
}

func TestContainerHeaderRejectsIconDirectoryChanges(t *testing.T) {
	entry := func(width, height, bits byte) []byte {
		data := make([]byte, 6+16)
		data[2], data[3] = 1, 0
		data[4], data[5] = 1, 0
		data[6], data[7] = width, height
		data[12], data[13] = bits, 0
		return data
	}
	if !sameContainer("app.ico", entry(16, 16, 32), entry(16, 16, 32)) {
		t.Fatal("相同图标目录应通过")
	}
	if sameContainer("app.ico", entry(16, 16, 32), entry(16, 16, 8)) {
		t.Fatal("位深变化应判定为过期")
	}
}
