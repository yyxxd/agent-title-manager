package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const uninstallKeyPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\AgentTitleManager`

var uninstallValues = []struct {
	name string
	kind uint32
}{
	{"DisplayName", registry.SZ},
	{"DisplayVersion", registry.SZ},
	{"InstallLocation", registry.SZ},
	{"DisplayIcon", registry.SZ},
	{"UninstallString", registry.SZ},
	{"NoModify", registry.DWORD},
	{"NoRepair", registry.DWORD},
}

type uninstallValue struct {
	Present bool   `json:"present"`
	Text    string `json:"text,omitempty"`
	Number  uint32 `json:"number,omitempty"`
}

func openUninstallKey(path string, access uint32) (registry.Key, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, access|registry.WOW64_32KEY)
	if err != nil {
		return 0, fmt.Errorf("无法读取安装登记: %w", err)
	}
	return key, nil
}

func (h *WindowsHelper) verifyRegistryVersion(installDir, want string) error {
	if h.handoff.Isolated {
		return nil
	}
	key, err := openUninstallKey(uninstallKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := checkRegistryLocation(key, installDir); err != nil {
		return err
	}
	version, _, err := key.GetStringValue("DisplayVersion")
	if err != nil || version != want {
		return fmt.Errorf("登记版本不符: 期望 %s，实际 %s (%v)", want, version, err)
	}
	return nil
}

func checkRegistryLocation(key registry.Key, installDir string) error {
	location, _, err := key.GetStringValue("InstallLocation")
	if err != nil || !strings.EqualFold(filepath.Clean(location), filepath.Clean(installDir)) {
		return fmt.Errorf("安装登记路径不符: 期望 %s，实际 %s (%v)", installDir, location, err)
	}
	return nil
}

func (h *WindowsHelper) prepare(installDir, dataDir string) (func() error, error) {
	if h.handoff.Isolated {
		return prepareSnapshot(installDir, dataDir)
	}
	restoreRegistry, backup, err := snapshotUninstallKey(uninstallKeyPath, installDir)
	if err != nil {
		return nil, err
	}
	if !backup["DisplayVersion"].Present || backup["DisplayVersion"].Text != h.handoff.PreviousVersion {
		return nil, errors.New("安装登记与当前版本不符")
	}
	restoreFiles, err := prepareSnapshot(installDir, dataDir)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(backup)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dataDir, "update-backup", "uninstall-registry.json"), data, 0600); err != nil {
		return nil, err
	}
	return func() error {
		return errors.Join(restoreFiles(), restoreRegistry())
	}, nil
}

func snapshotUninstallKey(path, installDir string) (func() error, map[string]uninstallValue, error) {
	key, err := openUninstallKey(path, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return nil, nil, err
	}
	defer key.Close()
	if err := checkRegistryLocation(key, installDir); err != nil {
		return nil, nil, err
	}
	saved, err := readUninstallValues(key)
	if err != nil {
		return nil, nil, err
	}
	return func() error { return restoreUninstallValues(path, installDir, saved) }, saved, nil
}

func readUninstallValues(key registry.Key) (map[string]uninstallValue, error) {
	values := make(map[string]uninstallValue, len(uninstallValues))
	for _, field := range uninstallValues {
		value, err := readUninstallValue(key, field.name, field.kind)
		if err != nil {
			return nil, err
		}
		values[field.name] = value
	}
	return values, nil
}

func readUninstallValue(key registry.Key, name string, kind uint32) (uninstallValue, error) {
	var value uninstallValue
	var actual uint32
	var err error
	if kind == registry.DWORD {
		var number uint64
		number, actual, err = key.GetIntegerValue(name)
		value.Number = uint32(number)
	} else {
		value.Text, actual, err = key.GetStringValue(name)
	}
	if errors.Is(err, registry.ErrNotExist) {
		return uninstallValue{}, nil
	}
	if err != nil || actual != kind {
		return uninstallValue{}, fmt.Errorf("无法备份安装登记项 %s（类型 %d）: %v", name, actual, err)
	}
	value.Present = true
	return value, nil
}

func restoreUninstallValues(path, installDir string, saved map[string]uninstallValue) error {
	key, err := openUninstallKey(path, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := checkRegistryLocation(key, installDir); err != nil {
		return err
	}
	for _, field := range uninstallValues {
		value := saved[field.name]
		switch {
		case !value.Present:
			err = key.DeleteValue(field.name)
			if errors.Is(err, registry.ErrNotExist) {
				err = nil
			}
		case field.kind == registry.DWORD:
			err = key.SetDWordValue(field.name, value.Number)
		default:
			err = key.SetStringValue(field.name, value.Text)
		}
		if err != nil {
			return fmt.Errorf("恢复安装登记项 %s 失败: %w", field.name, err)
		}
	}
	actual, err := readUninstallValues(key)
	if err != nil {
		return err
	}
	for name, value := range saved {
		if actual[name] != value {
			return fmt.Errorf("恢复后安装登记项 %s 不一致", name)
		}
	}
	return nil
}
