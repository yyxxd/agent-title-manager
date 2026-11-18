package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runName = "AgentTitleManager"

type preferences struct {
	Startup          bool `json:"startup"`
	StartupAvailable bool `json:"startupAvailable"`
}

func (s *Service) preferences() (preferences, error) {
	if s.scope != "" {
		return preferences{}, nil
	}
	result := preferences{StartupAvailable: true}
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(runName)
	if errors.Is(err, registry.ErrNotExist) {
		return result, nil
	}
	result.Startup = value != ""
	return result, err
}

func (s *Service) setPreferences(raw json.RawMessage) (preferences, error) {
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	if s.scope != "" {
		return preferences{}, errors.New("隔离配置不修改登录启动")
	}
	if err := json.Unmarshal(raw, &request); err != nil || request.Enabled == nil {
		return preferences{}, errors.New("缺少登录启动选项")
	}
	return preferences{Startup: *request.Enabled, StartupAvailable: true}, setStartup(*request.Enabled)
}

func setStartup(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		err := key.DeleteValue(runName)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return key.SetStringValue(runName, syscall.EscapeArg(executable)+" --background")
}
