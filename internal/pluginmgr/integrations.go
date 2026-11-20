package pluginmgr

import "context"

const OrcaIntegration = "orca"

type integration struct {
	id, name, detail string
}

var integrations = map[string]integration{
	OrcaIntegration: {OrcaIntegration, "Orca", "请在 Orca 设置中关闭「Name chats automatically」，避免两个应用同时改名。"},
}

func isIntegration(id string) bool {
	_, ok := integrations[id]
	return ok
}

func managedID(id string) bool { return builtins[id] != "" || isIntegration(id) }

func (m *Manager) IntegrationEnabled(ctx context.Context, id string) (bool, error) {
	if !isIntegration(id) {
		return false, nil
	}
	return m.enabled(ctx, id)
}

func (m *Manager) Changes() <-chan struct{} {
	m.changeMu.Lock()
	defer m.changeMu.Unlock()
	return m.changes
}

func (m *Manager) notifyChange() {
	m.changeMu.Lock()
	defer m.changeMu.Unlock()
	close(m.changes)
	m.changes = make(chan struct{})
}
