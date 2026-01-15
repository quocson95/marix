package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Styles for the UI
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")).
			MarginLeft(2)

	itemStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	selectedItemStyle = lipgloss.NewStyle().
				PaddingLeft(2).
				Foreground(lipgloss.Color("#7D56F4")).
				Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			MarginTop(1).
			MarginLeft(2)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF0000")).
			Bold(true).
			MarginLeft(2)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575")).
			Bold(true).
			MarginLeft(2)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#874BFD")).
			Padding(1, 2)
)

// MenuChoice represents a menu selection
type MenuChoice int

const (
	MenuNone MenuChoice = iota
	MenuConnect
	MenuServers
	MenuSFTP
	MenuBackup
	MenuSettings
	MenuQuit
)

// Model represents the main application model
type Model struct {
	choices  []string
	cursor   int
	selected MenuChoice
	err      error
	width    int
	height   int
}

// InitialModel creates the initial model
func InitialModel() Model {
	return Model{
		choices: []string{
			"Connect to Server",
			"Manage Servers",
			"SFTP Browser",
			"Backup & Restore",
			"Settings",
			"Quit",
		},
		cursor:   0,
		selected: MenuNone,
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.selected = MenuQuit
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}

		case "enter", " ":
			m.selected = MenuChoice(m.cursor + 1)
			if m.selected == MenuQuit {
				return m, tea.Quit
			}
			// For now, just show selection
			return m, nil
		}
	}

	return m, nil
}

// View renders the UI
func (m Model) View() string {
	if m.selected == MenuQuit {
		return ""
	}

	s := titleStyle.Render("🔐 Marix SSH Client") + "\n\n"

	// Menu items
	for i, choice := range m.choices {
		cursor := " "
		if m.cursor == i {
			cursor = ">"
			choice = selectedItemStyle.Render(choice)
		} else {
			choice = itemStyle.Render(choice)
		}
		s += fmt.Sprintf("%s %s\n", cursor, choice)
	}

	// Help text
	s += "\n" + helpStyle.Render("↑/k up • ↓/j down • enter select • esc/q quit")

	// Show selected action
	if m.selected != MenuNone {
		s += "\n\n" + successStyle.Render(fmt.Sprintf("Selected: %s", m.choices[m.cursor]))
	}

	return boxStyle.Render(s)
}
