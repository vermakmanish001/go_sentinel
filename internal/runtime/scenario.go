package runtime

import (
	"fmt"
	"time"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// Scenario represents a parsed test scenario
type Scenario struct {
	Name   string
	Stages []models.Stage
	HTTP   models.HTTPConfig
	Vars   map[string]string
}

// Validate validates the scenario
func (s *Scenario) Validate() error {
	if len(s.Stages) == 0 {
		return fmt.Errorf("scenario must have at least one stage")
	}

	for i, stage := range s.Stages {
		if stage.TargetVUs <= 0 {
			return fmt.Errorf("stage %d: target_vus must be positive", i)
		}
		if stage.Duration <= 0 {
			return fmt.Errorf("stage %d: duration must be positive", i)
		}
	}

	if s.HTTP.BaseURL == "" {
		return fmt.Errorf("http.base_url is required")
	}

	if len(s.HTTP.Requests) == 0 {
		return fmt.Errorf("http.requests cannot be empty")
	}

	for i, req := range s.HTTP.Requests {
		if req.Method == "" {
			return fmt.Errorf("request %d: method is required", i)
		}
		if req.Path == "" {
			return fmt.Errorf("request %d: path is required", i)
		}
	}

	return nil
}

// YAMLStage represents a stage in YAML format
type YAMLStage struct {
	Duration  string `yaml:"duration" json:"duration"`
	TargetVUs int32  `yaml:"target_vus" json:"target_vus"`
	RampUp    string `yaml:"ramp_up,omitempty" json:"ramp_up,omitempty"`
}

// YAMLHTTPConfig represents HTTP config in YAML format
type YAMLHTTPConfig struct {
	BaseURL  string            `yaml:"base_url" json:"base_url"`
	Requests []YAMLRequest     `yaml:"requests" json:"requests"`
	Headers  map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Timeout  string            `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// YAMLRequest represents a request in YAML format
type YAMLRequest struct {
	Method     string            `yaml:"method" json:"method"`
	Path       string            `yaml:"path" json:"path"`
	Headers    map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body       string            `yaml:"body,omitempty" json:"body,omitempty"`
	Assertions []YAMLAssertion   `yaml:"assertions,omitempty" json:"assertions,omitempty"`
	ThinkTime  string            `yaml:"think_time,omitempty" json:"think_time,omitempty"`
}

// YAMLAssertion represents an assertion in YAML format
type YAMLAssertion struct {
	StatusCode      *int32  `yaml:"status_code,omitempty" json:"status_code,omitempty"`
	ResponseTimeP99 *int32  `yaml:"response_time_p99_ms,omitempty" json:"response_time_p99_ms,omitempty"`
	ResponseTimeP95 *int32  `yaml:"response_time_p95_ms,omitempty" json:"response_time_p95_ms,omitempty"`
	ResponseTimeP50 *int32  `yaml:"response_time_p50_ms,omitempty" json:"response_time_p50_ms,omitempty"`
	BodyContains    *string `yaml:"body_contains,omitempty" json:"body_contains,omitempty"`
}

// YAMLTestPlan represents the root YAML structure
type YAMLTestPlan struct {
	Name      string            `yaml:"name,omitempty" json:"name,omitempty"`
	Stages    []YAMLStage       `yaml:"stages" json:"stages"`
	HTTP      YAMLHTTPConfig    `yaml:"http" json:"http"`
	Variables map[string]string `yaml:"variables,omitempty" json:"variables,omitempty"`
}

// ParseDuration parses a duration string (e.g., "30s", "1m")
func ParseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("duration cannot be empty")
	}
	return time.ParseDuration(s)
}

// ParseRequest converts YAMLRequest to models.Request
func ParseRequest(yamlReq YAMLRequest, vars map[string]string) (models.Request, error) {
	req := models.Request{
		Method:     yamlReq.Method,
		Path:       yamlReq.Path,
		Headers:    yamlReq.Headers,
		Body:       []byte(yamlReq.Body),
		Assertions: []models.Assertion{},
	}

	// Parse think time
	if yamlReq.ThinkTime != "" {
		thinkTime, err := ParseDuration(yamlReq.ThinkTime)
		if err != nil {
			return req, fmt.Errorf("invalid think_time: %w", err)
		}
		req.ThinkTime = thinkTime
	}

	// Parse assertions
	for _, yamlAssert := range yamlReq.Assertions {
		assertion := models.Assertion{}

		if yamlAssert.StatusCode != nil {
			assertion.Type = models.AssertionTypeStatusCode
			assertion.Value = *yamlAssert.StatusCode
		} else if yamlAssert.ResponseTimeP99 != nil {
			assertion.Type = models.AssertionTypeResponseTime
			assertion.Value = "P99"
			assertion.Threshold = time.Duration(*yamlAssert.ResponseTimeP99) * time.Millisecond
		} else if yamlAssert.ResponseTimeP95 != nil {
			assertion.Type = models.AssertionTypeResponseTime
			assertion.Value = "P95"
			assertion.Threshold = time.Duration(*yamlAssert.ResponseTimeP95) * time.Millisecond
		} else if yamlAssert.ResponseTimeP50 != nil {
			assertion.Type = models.AssertionTypeResponseTime
			assertion.Value = "P50"
			assertion.Threshold = time.Duration(*yamlAssert.ResponseTimeP50) * time.Millisecond
		} else if yamlAssert.BodyContains != nil {
			assertion.Type = models.AssertionTypeBodyContains
			assertion.Value = *yamlAssert.BodyContains
		}

		if assertion.Type != "" {
			req.Assertions = append(req.Assertions, assertion)
		}
	}

	return req, nil
}
