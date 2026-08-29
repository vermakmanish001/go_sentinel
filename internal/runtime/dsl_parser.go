package runtime

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// Parser parses YAML test plans into Scenario structs
type Parser struct {
	logger *zap.Logger
}

// NewParser creates a new DSL parser
func NewParser(logger *zap.Logger) *Parser {
	return &Parser{
		logger: logger,
	}
}

// ParseFile parses a YAML test plan file
func (p *Parser) ParseFile(filename string) (*Scenario, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return p.Parse(data)
}

// Parse parses YAML test plan data
func (p *Parser) Parse(data []byte) (*Scenario, error) {
	var yamlPlan YAMLTestPlan
	if err := yaml.Unmarshal(data, &yamlPlan); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return p.FromSpec(yamlPlan)
}

// FromSpec converts an already-decoded plan into a validated Scenario. YAML
// files and JSON submitted to the HTTP API both land here, so both are subject
// to identical variable substitution and validation.
func (p *Parser) FromSpec(yamlPlan YAMLTestPlan) (*Scenario, error) {
	scenario := &Scenario{
		Name:   yamlPlan.Name,
		Stages: []models.Stage{},
		HTTP: models.HTTPConfig{
			BaseURL:  yamlPlan.HTTP.BaseURL,
			Headers:  yamlPlan.HTTP.Headers,
			Requests: []models.Request{},
		},
		Vars: yamlPlan.Variables,
	}

	// Parse stages
	for _, yamlStage := range yamlPlan.Stages {
		duration, err := ParseDuration(yamlStage.Duration)
		if err != nil {
			return nil, fmt.Errorf("invalid stage duration: %w", err)
		}

		stage := models.Stage{
			Duration:  duration,
			TargetVUs: yamlStage.TargetVUs,
		}

		if yamlStage.RampUp != "" {
			rampUp, err := ParseDuration(yamlStage.RampUp)
			if err != nil {
				return nil, fmt.Errorf("invalid stage ramp_up: %w", err)
			}
			stage.RampUp = rampUp
		}

		scenario.Stages = append(scenario.Stages, stage)
	}

	// Parse HTTP timeout
	if yamlPlan.HTTP.Timeout != "" {
		timeout, err := ParseDuration(yamlPlan.HTTP.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid http timeout: %w", err)
		}
		scenario.HTTP.Timeout = timeout
	} else {
		scenario.HTTP.Timeout = 30 * time.Second // default
	}

	// Parse requests
	for _, yamlReq := range yamlPlan.HTTP.Requests {
		req, err := ParseRequest(yamlReq, scenario.Vars)
		if err != nil {
			return nil, fmt.Errorf("failed to parse request: %w", err)
		}

		// Substitute variables in path
		req.Path = substituteVars(req.Path, scenario.Vars)
		req.Method = strings.ToUpper(req.Method)

		scenario.HTTP.Requests = append(scenario.HTTP.Requests, req)
	}

	// Substitute variables in base URL
	scenario.HTTP.BaseURL = substituteVars(scenario.HTTP.BaseURL, scenario.Vars)

	// Validate scenario
	if err := scenario.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	return scenario, nil
}

// substituteVars substitutes variables in a string
func substituteVars(s string, vars map[string]string) string {
	result := s
	for key, value := range vars {
		placeholder := fmt.Sprintf("${%s}", key)
		result = strings.ReplaceAll(result, placeholder, value)
	}
	// Also substitute environment variables
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			key := parts[0]
			value := parts[1]
			placeholder := fmt.Sprintf("${%s}", key)
			result = strings.ReplaceAll(result, placeholder, value)
		}
	}
	return result
}
