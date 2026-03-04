package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	// Colors
	primaryColor   = lipgloss.Color("39")
	secondaryColor = lipgloss.Color("240")
	successColor   = lipgloss.Color("46")
	warningColor   = lipgloss.Color("226")
	errorColor     = lipgloss.Color("196")
	borderColor    = lipgloss.Color("240")

	// Title style
	TitleStyle = lipgloss.NewStyle().
		Foreground(primaryColor).
		Bold(true).
		Padding(0, 1)

	// Border styles
	BorderStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2)

	ThinBorderStyle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Padding(0, 1)

	// Status styles
	SuccessStyle = lipgloss.NewStyle().
		Foreground(successColor).
		Bold(true)

	WarningStyle = lipgloss.NewStyle().
		Foreground(warningColor).
		Bold(true)

	ErrorStyle = lipgloss.NewStyle().
		Foreground(errorColor).
		Bold(true)

	// Table styles
	TableHeaderStyle = lipgloss.NewStyle().
		Foreground(primaryColor).
		Bold(true).
		Padding(0, 1)

	TableRowStyle = lipgloss.NewStyle().
		Padding(0, 1)

	// Metric styles
	MetricLabelStyle = lipgloss.NewStyle().
		Foreground(secondaryColor).
		Padding(0, 1)

	MetricValueStyle = lipgloss.NewStyle().
		Foreground(primaryColor).
		Bold(true).
		Padding(0, 1)

	// Help text style
	HelpStyle = lipgloss.NewStyle().
		Foreground(secondaryColor).
		Italic(true)
)
