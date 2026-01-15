package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m AppModel) updateSettings(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" {
			m.state = StateMenu
			return m, nil
		}
	}

	var cmd tea.Cmd
	updatedModel, cmd := m.settingsModel.Update(msg)
	m.settingsModel = updatedModel.(*SettingsModel)
	return m, cmd
}
