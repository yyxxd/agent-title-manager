package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Handoff is the helper's input. It is passed as a JSON file path so no
// user-controlled value is ever concatenated into a shell command.
type Handoff struct {
	AppPID     uint32 `json:"appPid"`
	Installer  string `json:"installer"`
	InstallDir string `json:"installDir"`
	DataDir    string `json:"dataDir"`
	Version    string `json:"version"`
	Restart    string `json:"restart"`
	Isolated   bool   `json:"isolated"`
}

// Status is written after the helper finishes so the restarted app can
// surface success or failure through the existing toast feedback.
type Status struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
	Message string `json:"message"`
}

func ReadHandoff(path string) (Handoff, error) {
	var h Handoff
	data, err := os.ReadFile(path)
	if err != nil {
		return h, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&h); err != nil {
		return h, fmt.Errorf("更新参数无效: %w", err)
	}
	if h.Installer == "" || h.InstallDir == "" || h.DataDir == "" || h.Version == "" || h.Restart == "" {
		return h, errors.New("更新参数不完整")
	}
	for _, path := range []string{h.Installer, h.InstallDir, h.DataDir, h.Restart} {
		if !filepath.IsAbs(path) {
			return h, errors.New("更新参数必须使用绝对路径")
		}
	}
	return h, nil
}

func StatusPath(dataDir string) string {
	return filepath.Join(dataDir, "update-status.json")
}

func WriteStatus(dataDir string, status Status) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dataDir, ".update-status-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(data); err == nil {
		err = temp.Close()
	}
	if err != nil {
		return err
	}
	return os.Rename(temp.Name(), StatusPath(dataDir))
}

func ReadStatus(dataDir string) (Status, error) {
	var status Status
	data, err := os.ReadFile(StatusPath(dataDir))
	if err != nil {
		return status, err
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return status, err
	}
	if err := os.Remove(StatusPath(dataDir)); err != nil {
		return status, err
	}
	return status, nil
}
