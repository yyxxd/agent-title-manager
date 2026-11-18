// The resource objects are committed so ordinary Go builds include the icon.
package main

import (
	"bytes"
	"flag"
	"fmt"
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
		if err := saveResource(path, &resources, arch, check); err != nil {
			return err
		}
	}
	return nil
}

func saveResource(path string, resources *winres.ResourceSet, arch winres.Arch, check bool) error {
	var object bytes.Buffer
	if err := resources.WriteObject(&object, arch); err != nil {
		return err
	}
	if !check {
		return os.WriteFile(path, object.Bytes(), 0644)
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(committed, object.Bytes()) {
		return fmt.Errorf("%s is out of date. Run go generate ./cmd/atm", path)
	}
	return nil
}
