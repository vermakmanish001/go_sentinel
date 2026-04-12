package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// Model is the root Bubbletea model
type Model struct {
	metricsView *MetricsView
	nodesView   *NodesView
	width       int
	height      int
	paused      bool
	testID      string
	metrics     models.MetricSnapshot
	nodes       []*models.WorkerNode
}

// tickMsg is sent periodically to update the view
type tickMsg time.Time

// MetricsUpdateMsg updates metrics
type MetricsUpdateMsg models.MetricSnapshot

// NodesUpdateMsg updates nodes
type NodesUpdateMsg []*models.WorkerNode

// NewModel creates a new dashboard model
func NewModel(testID string) *Model {
	return &Model{
		metricsView: NewMetricsView(),
		nodesView:   NewNodesView(),
		testID:      testID,
		paused:      false,
		metrics:     models.MetricSnapshot{},
		nodes:       make([]*models.WorkerNode, 0),
	}
}

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.tick(),
	)
}

// Update updates the model
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.metricsView.SetSize(msg.Width/2-2, msg.Height-10)
		m.nodesView.SetSize(msg.Width/2-2, msg.Height-10)
		return m, nil

	case tickMsg:
		if !m.paused {
			return m, m.tick()
		}
		return m, nil

	case MetricsUpdateMsg:
		m.metrics = models.MetricSnapshot(msg)
		m.metricsView.UpdateMetrics(m.metrics)
		return m, nil

	case NodesUpdateMsg:
		m.nodes = []*models.WorkerNode(msg)
		m.nodesView.UpdateNodes(m.nodes)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "p":
			m.paused = !m.paused
			if !m.paused {
				return m, m.tick()
			}
			return m, nil
		case "r":
			m.metrics = models.MetricSnapshot{}
			m.metricsView.UpdateMetrics(m.metrics)
			return m, nil
		}
	}

	return m, nil
}

// View renders the model
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	// Header
	header := m.renderHeader()

	// Main content (side by side)
	metricsContent := m.metricsView.View()
	nodesContent := m.nodesView.View()
	mainContent := lipgloss.JoinHorizontal(lipgloss.Top, metricsContent, "  ", nodesContent)

	// Footer
	footer := m.renderFooter()

	// Combine
	return lipgloss.JoinVertical(lipgloss.Left, header, mainContent, footer)
}

// renderHeader renders the header
func (m *Model) renderHeader() string {
	title := TitleStyle.Render("GoSentinel Load Testing Dashboard")
	status := SuccessStyle.Render("RUNNING")
	if m.paused {
		status = WarningStyle.Render("PAUSED")
	}
	testInfo := fmt.Sprintf("Test ID: %s | Status: %s", m.testID, status)
	return lipgloss.JoinHorizontal(lipgloss.Left, title, "  ", testInfo)
}

// renderFooter renders the footer
func (m *Model) renderFooter() string {
	help := HelpStyle.Render("Press 'q' to quit, 'p' to pause, 'r' to reset")
	return help
}

// tick sends a tick message
func (m *Model) tick() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// UpdateMetrics updates metrics from external source
func (m *Model) UpdateMetrics(metrics models.MetricSnapshot) {
	m.metrics = metrics
	m.metricsView.UpdateMetrics(metrics)
}

// UpdateNodes updates nodes from external source
func (m *Model) UpdateNodes(nodes []*models.WorkerNode) {
	m.nodes = nodes
	m.nodesView.UpdateNodes(nodes)
}
