package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// MetricsView displays real-time metrics
type MetricsView struct {
	metrics models.MetricSnapshot
	width   int
	height  int
}

// NewMetricsView creates a new metrics view
func NewMetricsView() *MetricsView {
	return &MetricsView{
		metrics: models.MetricSnapshot{},
	}
}

// UpdateMetrics updates the metrics
func (mv *MetricsView) UpdateMetrics(metrics models.MetricSnapshot) {
	mv.metrics = metrics
}

// SetSize sets the view size
func (mv *MetricsView) SetSize(width, height int) {
	mv.width = width
	mv.height = height
}

// View renders the metrics view
func (mv *MetricsView) View() string {
	if mv.width == 0 || mv.height == 0 {
		return ""
	}

	var sections []string

	// RPS section
	rpsSection := mv.renderRPS()
	sections = append(sections, rpsSection)

	// Latency section
	latencySection := mv.renderLatency()
	sections = append(sections, latencySection)

	// Error rate section
	errorSection := mv.renderErrorRate()
	sections = append(sections, errorSection)

	// Combine sections
	content := strings.Join(sections, "\n\n")
	return BorderStyle.Width(mv.width - 4).Height(mv.height - 2).Render(content)
}

// renderRPS renders the RPS metrics
func (mv *MetricsView) renderRPS() string {
	title := TitleStyle.Render("Requests Per Second")
	
	rows := []string{
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("Current:"), MetricValueStyle.Render(fmt.Sprintf("%.2f", mv.metrics.RPS.Current))),
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("Average:"), MetricValueStyle.Render(fmt.Sprintf("%.2f", mv.metrics.RPS.Average))),
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("Peak:"), MetricValueStyle.Render(fmt.Sprintf("%.2f", mv.metrics.RPS.Peak))),
	}

	content := strings.Join(rows, "\n")
	return lipgloss.JoinVertical(lipgloss.Left, title, content)
}

// renderLatency renders the latency metrics
func (mv *MetricsView) renderLatency() string {
	title := TitleStyle.Render("Latency (ms)")

	rows := []string{
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("Min:"), MetricValueStyle.Render(fmt.Sprintf("%d", mv.metrics.Latency.Min.Milliseconds()))),
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("Mean:"), MetricValueStyle.Render(fmt.Sprintf("%d", mv.metrics.Latency.Mean.Milliseconds()))),
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("Max:"), MetricValueStyle.Render(fmt.Sprintf("%d", mv.metrics.Latency.Max.Milliseconds()))),
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("P50:"), MetricValueStyle.Render(fmt.Sprintf("%d", mv.metrics.Latency.P50.Milliseconds()))),
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("P95:"), MetricValueStyle.Render(fmt.Sprintf("%d", mv.metrics.Latency.P95.Milliseconds()))),
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("P99:"), MetricValueStyle.Render(fmt.Sprintf("%d", mv.metrics.Latency.P99.Milliseconds()))),
	}

	content := strings.Join(rows, "\n")
	return lipgloss.JoinVertical(lipgloss.Left, title, content)
}

// renderErrorRate renders the error rate metrics
func (mv *MetricsView) renderErrorRate() string {
	title := TitleStyle.Render("Error Rate")

	errorStyle := SuccessStyle
	if mv.metrics.ErrorRate.Percentage > 1.0 {
		errorStyle = WarningStyle
	}
	if mv.metrics.ErrorRate.Percentage > 5.0 {
		errorStyle = ErrorStyle
	}

	rows := []string{
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("Rate:"), errorStyle.Render(fmt.Sprintf("%.2f/s", mv.metrics.ErrorRate.Rate))),
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("Percentage:"), errorStyle.Render(fmt.Sprintf("%.2f%%", mv.metrics.ErrorRate.Percentage))),
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("Total Errors:"), MetricValueStyle.Render(fmt.Sprintf("%d", mv.metrics.TotalErrors))),
		fmt.Sprintf("%s %s", MetricLabelStyle.Render("Total Requests:"), MetricValueStyle.Render(fmt.Sprintf("%d", mv.metrics.TotalRequests))),
	}

	content := strings.Join(rows, "\n")
	return lipgloss.JoinVertical(lipgloss.Left, title, content)
}
