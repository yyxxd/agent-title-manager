// The resource objects are committed so ordinary Go builds include the icon.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"github.com/tc-hib/winres"
)

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false, "verify the committed resource objects")
	flag.Parse()
	if err := generate(*root, *check); err != nil {
		log.Fatal(err)
	}
}

func generate(root string, check bool) error {
	pngBytes, err := os.ReadFile(filepath.Join(root, "internal", "desktop", "icon.png"))
	if err != nil {
		return err
	}
	source, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return err
	}
	icon, err := winres.NewIconFromResizedImage(source, []int{16, 20, 24, 32, 40, 48, 64, 96, 128, 256})
	if err != nil {
		return err
	}
	var resources winres.ResourceSet
	// Wails v3 loads icon group 3 for the native window.
	if err := resources.SetIcon(winres.ID(3), icon); err != nil {
		return err
	}
	for _, arch := range []winres.Arch{winres.ArchAMD64, winres.ArchARM64} {
		path := filepath.Join(root, "cmd", "atm", fmt.Sprintf("icon_windows_%s.syso", arch))
		var object bytes.Buffer
		if err := resources.WriteObject(&object, arch); err != nil {
			return err
		}
		if err := save(path, object.Bytes(), check); err != nil {
			return err
		}
	}
	// The NSIS installer and uninstaller use the same icon.
	var ico bytes.Buffer
	if err := icon.SaveICO(&ico); err != nil {
		return err
	}
	return save(filepath.Join(root, "packaging", "app.ico"), ico.Bytes(), check)
}

func save(path string, content []byte, check bool) error {
	if !check {
		return os.WriteFile(path, content, 0644)
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// PNG 压缩字节随 Go 工具链变化，只比较解码后的图标像素。
	if !bytes.Equal(committed, content) && !sameImages(iconImages(committed), iconImages(content)) {
		return fmt.Errorf("%s is out of date. Run go generate ./cmd/atm", path)
	}
	return nil
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// iconImages 提取 ICO 或资源对象里内嵌的 PNG 图标。压缩字节可能不同，像素必须一致。
func iconImages(data []byte) []image.Image {
	var images []image.Image
	for {
		index := bytes.Index(data, pngSignature)
		if index < 0 {
			return images
		}
		payload, rest, err := pngStream(data[index:])
		var img image.Image
		if err == nil {
			img, err = png.Decode(bytes.NewReader(payload))
		}
		if err != nil {
			data = data[index+len(pngSignature):]
			continue
		}
		images = append(images, img)
		data = rest
	}
}

func pngStream(data []byte) ([]byte, []byte, error) {
	for offset := len(pngSignature); offset+8 <= len(data); {
		length := int(binary.BigEndian.Uint32(data[offset:]))
		kind := string(data[offset+4 : offset+8])
		end := offset + 8 + length + 4
		if length < 0 || end > len(data) {
			return nil, nil, errors.New("PNG 数据不完整")
		}
		if kind == "IEND" {
			return data[:end], data[end:], nil
		}
		offset = end
	}
	return nil, nil, errors.New("PNG 数据缺少结尾")
}

func sameImages(left, right []image.Image) bool {
	if len(left) != len(right) || len(left) == 0 {
		return false
	}
	for i := range left {
		bounds := left[i].Bounds()
		if bounds != right[i].Bounds() {
			return false
		}
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				if !sameColor(left[i].At(x, y), right[i].At(x, y)) {
					return false
				}
			}
		}
	}
	return true
}

// 不同颜色模型的 color.Color 实现不可直接比较，统一按 RGBA 通道值判断。
func sameColor(left, right color.Color) bool {
	lr, lg, lb, la := left.RGBA()
	rr, rg, rb, ra := right.RGBA()
	return lr == rr && lg == rg && lb == rb && la == ra
}
