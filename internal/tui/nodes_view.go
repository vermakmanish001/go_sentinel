package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// NodesView displays worker node status
type NodesView struct {
	nodes  []*models.WorkerNode
	width  int
	height int
}

// NewNodesView creates a new nodes view
func NewNodesView() *NodesView {
	return &NodesView{
		nodes: make([]*models.WorkerNode, 0),
	}
}

// UpdateNodes updates the nodes
func (nv *NodesView) UpdateNodes(nodes []*models.WorkerNode) {
	nv.nodes = nodes
}

// SetSize sets the view size
func (nv *NodesView) SetSize(width, height int) {
	nv.width = width
	nv.height = height
}

// View renders the nodes view
func (nv *NodesView) View() string {
	if nv.width == 0 || nv.height == 0 {
		return ""
	}

	title := TitleStyle.Render("Worker Nodes")

	if len(nv.nodes) == 0 {
		content := HelpStyle.Render("No worker nodes connected")
		return BorderStyle.Width(nv.width - 4).Height(nv.height - 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, title, content),
		)
	}

	// Table header
	header := strings.Join([]string{
		TableHeaderStyle.Width(20).Render("Node ID"),
		TableHeaderStyle.Width(25).Render("Address"),
		TableHeaderStyle.Width(10).Render("Status"),
		TableHeaderStyle.Width(10).Render("Max VUs"),
	}, " | ")

	// Table rows
	rows := make([]string, 0, len(nv.nodes))
	for _, node := range nv.nodes {
		statusStyle := SuccessStyle
		switch node.Status {
		case models.WorkerStatusRunning:
			statusStyle = SuccessStyle
		case models.WorkerStatusError:
			statusStyle = ErrorStyle
		case models.WorkerStatusStopping:
			statusStyle = WarningStyle
		default:
			statusStyle = HelpStyle
		}

		row := strings.Join([]string{
			TableRowStyle.Width(20).Render(node.ID),
			TableRowStyle.Width(25).Render(node.Address),
			statusStyle.Width(10).Render(string(node.Status)),
			TableRowStyle.Width(10).Render(fmt.Sprintf("%d", node.MaxVUs)),
		}, " | ")

		rows = append(rows, row)
	}

	// Combine header and rows
	separator := strings.Repeat("-", nv.width-4)
	content := strings.Join(append([]string{header, separator}, rows...), "\n")

	return BorderStyle.Width(nv.width - 4).Height(nv.height - 2).Render(
		lipgloss.JoinVertical(lipgloss.Left, title, content),
	)
}
