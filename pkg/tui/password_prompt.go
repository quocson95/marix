package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PasswordPromptModel is a reusable password input component
type PasswordPromptModel struct {
	input       textinput.Model
	title       string
	description string
	submitted   bool
	cancelled   bool
	password    string
	width       int
	height      int
}

// PasswordSubmittedMsg is sent when password is submitted
type PasswordSubmittedMsg struct {
	Password  string
	Cancelled bool
}

// NewPasswordPromptModel creates a new password prompt
func NewPasswordPromptModel(title, description string) *PasswordPromptModel {
	input := textinput.New()
	input.Placeholder = "Enter password"
	input.EchoMode = textinput.EchoPassword
	input.EchoCharacter = '•'
	input.CharLimit = 256
	input.Width = 50
	input.Prompt = "> "
	input.Focus()

	return &PasswordPromptModel{
		input:       input,
		title:       title,
		description: description,
	}
}

func (m *PasswordPromptModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *PasswordPromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			m.password = m.input.Value()
			m.submitted = true
			return m, func() tea.Msg {
				return PasswordSubmittedMsg{
					Password:  m.password,
					Cancelled: false,
				}
			}

		case "esc":
			m.cancelled = true
			return m, func() tea.Msg {
				return PasswordSubmittedMsg{
					Password:  "",
					Cancelled: true,
				}
			}
		}
	}

	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *PasswordPromptModel) View() string {
	var b strings.Builder

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		MarginBottom(1)
	b.WriteString(titleStyle.Render(m.title))
	b.WriteString("\n\n")

	// Description
	if m.description != "" {
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
		b.WriteString(descStyle.Render(m.description))
		b.WriteString("\n\n")
	}

	// Input
	b.WriteString(m.input.View())
	b.WriteString("\n\n")

	// Help text
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true)
	b.WriteString(helpStyle.Render("enter: submit • esc: cancel"))

	// Box style
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2)

	return boxStyle.Render(b.String())
}

func (m *PasswordPromptModel) SetError(err error) {
	if err != nil {
		m.description = fmt.Sprintf("❌ %v", err)
	}
}
