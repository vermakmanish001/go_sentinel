package runtime

import (
	"time"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
)

// ScenarioToProto converts a parsed scenario into the wire format the
// orchestrator accepts. Shared by every client that submits a plan so the CLI
// and the HTTP API cannot drift apart.
func ScenarioToProto(scenario *Scenario, testID string) *pborchestrator.TestPlan {
	stages := make([]*pborchestrator.Stage, len(scenario.Stages))
	for i, s := range scenario.Stages {
		stage := &pborchestrator.Stage{
			Duration:  s.Duration.String(),
			TargetVus: s.TargetVUs,
		}
		if s.RampUp > 0 {
			stage.RampUp = s.RampUp.String()
		}
		stages[i] = stage
	}

	requests := make([]*pborchestrator.Request, len(scenario.HTTP.Requests))
	for i, r := range scenario.HTTP.Requests {
		requests[i] = &pborchestrator.Request{
			Method:      r.Method,
			Path:        r.Path,
			Headers:     r.Headers,
			Body:        r.Body,
			Assertions:  assertionsToProto(r.Assertions),
			ThinkTimeMs: int32(r.ThinkTime / time.Millisecond),
		}
	}

	var peakVUs int32
	for _, s := range scenario.Stages {
		if s.TargetVUs > peakVUs {
			peakVUs = s.TargetVUs
		}
	}

	return &pborchestrator.TestPlan{
		Id:     testID,
		Name:   scenario.Name,
		Stages: stages,
		Http: &pborchestrator.HTTPConfig{
			BaseUrl:   scenario.HTTP.BaseURL,
			Requests:  requests,
			Headers:   scenario.HTTP.Headers,
			TimeoutMs: int32(scenario.HTTP.Timeout / time.Millisecond),
		},
		Variables:         scenario.Vars,
		TotalVirtualUsers: peakVUs,
	}
}

func assertionsToProto(assertions []models.Assertion) []*pborchestrator.Assertion {
	out := make([]*pborchestrator.Assertion, 0, len(assertions))

	for _, a := range assertions {
		var protoAssert *pborchestrator.Assertion

		switch a.Type {
		case models.AssertionTypeStatusCode:
			var code int32
			switch v := a.Value.(type) {
			case int:
				code = int32(v)
			case int32:
				code = v
			}
			protoAssert = &pborchestrator.Assertion{
				Assertion: &pborchestrator.Assertion_StatusCode{
					StatusCode: &pborchestrator.StatusCodeAssertion{Expected: code},
				},
			}

		case models.AssertionTypeResponseTime:
			percentile, _ := a.Value.(string)
			threshold, _ := a.Threshold.(time.Duration)
			protoAssert = &pborchestrator.Assertion{
				Assertion: &pborchestrator.Assertion_ResponseTime{
					ResponseTime: &pborchestrator.ResponseTimeAssertion{
						Percentile: percentile,
						MaxMs:      int32(threshold / time.Millisecond),
					},
				},
			}

		case models.AssertionTypeBodyContains:
			substr, _ := a.Value.(string)
			protoAssert = &pborchestrator.Assertion{
				Assertion: &pborchestrator.Assertion_BodyContains{
					BodyContains: &pborchestrator.BodyContainsAssertion{Substring: substr},
				},
			}
		}

		if protoAssert != nil {
			out = append(out, protoAssert)
		}
	}

	return out
}
