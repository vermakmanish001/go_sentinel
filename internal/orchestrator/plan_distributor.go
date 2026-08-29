package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"go.uber.org/zap"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// PlanDistributor distributes test plans across worker nodes
type PlanDistributor struct {
	nodeManager *NodeManager
	logger      *zap.Logger
	mu          sync.RWMutex
	activeTests map[string]*ActiveTest
}

// ActiveTest represents an active test
type ActiveTest struct {
	TestID  string
	Plan    *models.TestPlan
	Workers map[string]int32 // worker_id -> assigned VUs
	Status  models.TestStatus
	// done records which workers have reported that they finished the test.
	done map[string]bool
	mu   sync.RWMutex
}

// Snapshot returns a consistent copy of the test's mutable state.
func (at *ActiveTest) Snapshot() (models.TestStatus, map[string]int32) {
	at.mu.RLock()
	defer at.mu.RUnlock()

	workers := make(map[string]int32, len(at.Workers))
	for id, vus := range at.Workers {
		workers[id] = vus
	}
	return at.Status, workers
}

// NewPlanDistributor creates a new plan distributor
func NewPlanDistributor(nodeManager *NodeManager, logger *zap.Logger) *PlanDistributor {
	return &PlanDistributor{
		nodeManager: nodeManager,
		logger:      logger,
		activeTests: make(map[string]*ActiveTest),
	}
}

// DistributePlan distributes a test plan across available workers
func (pd *PlanDistributor) DistributePlan(ctx context.Context, testID string, plan *models.TestPlan) (map[string]int32, error) {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	// Get available nodes
	nodes := pd.nodeManager.GetNodes()
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no worker nodes available")
	}

	// Calculate total VUs needed
	totalVUs := int32(0)
	for _, stage := range plan.Stages {
		if stage.TargetVUs > totalVUs {
			totalVUs = stage.TargetVUs
		}
	}

	// Distribute VUs across nodes using consistent hashing
	distribution := make(map[string]int32)
	remainingVUs := totalVUs

	// Calculate capacity
	totalCapacity := int32(0)
	for _, node := range nodes {
		totalCapacity += node.MaxVUs
	}

	if totalCapacity < totalVUs {
		return nil, fmt.Errorf("insufficient worker capacity: need %d, have %d", totalVUs, totalCapacity)
	}

	// Distribute proportionally based on capacity
	for _, node := range nodes {
		if remainingVUs <= 0 {
			break
		}

		// Calculate proportional share
		share := (float64(node.MaxVUs) / float64(totalCapacity)) * float64(totalVUs)
		assigned := int32(share)

		// Ensure we don't exceed node capacity or remaining VUs
		if assigned > node.MaxVUs {
			assigned = node.MaxVUs
		}
		if assigned > remainingVUs {
			assigned = remainingVUs
		}

		if assigned > 0 {
			distribution[node.ID] = assigned
			remainingVUs -= assigned
		}
	}

	// Distribute any remaining VUs
	if remainingVUs > 0 {
		for _, node := range nodes {
			if remainingVUs <= 0 {
				break
			}

			available := node.MaxVUs - distribution[node.ID]
			if available > 0 {
				assigned := remainingVUs
				if assigned > available {
					assigned = available
				}
				distribution[node.ID] += assigned
				remainingVUs -= assigned
			}
		}
	}

	// Store active test
	pd.activeTests[testID] = &ActiveTest{
		TestID:  testID,
		Plan:    plan,
		Workers: distribution,
		Status:  models.TestStatusRunning,
		done:    make(map[string]bool),
	}

	pd.logger.Info("plan distributed",
		zap.String("test_id", testID),
		zap.Int32("total_vus", totalVUs),
		zap.Int("workers", len(distribution)),
	)

	return distribution, nil
}

// allocateStageVUs splits one stage's target VU count across the workers holding
// a test, in proportion to each worker's share of the test's peak load.
//
// Largest-remainder rounding is used so the per-worker counts sum to exactly
// targetVUs. Letting each worker round its own share independently would make
// the fleet overshoot or undershoot every stage target.
func allocateStageVUs(distribution map[string]int32, targetVUs int32) map[string]int32 {
	allocation := make(map[string]int32, len(distribution))
	if targetVUs <= 0 || len(distribution) == 0 {
		return allocation
	}

	// Sort the worker IDs so ties between equal remainders break the same way on
	// every stage and every run.
	workerIDs := make([]string, 0, len(distribution))
	totalShare := int32(0)
	for id, share := range distribution {
		workerIDs = append(workerIDs, id)
		totalShare += share
	}
	if totalShare <= 0 {
		return allocation
	}
	sort.Strings(workerIDs)

	type remainder struct {
		workerID string
		frac     float64
	}

	remainders := make([]remainder, 0, len(workerIDs))
	assigned := int32(0)
	for _, id := range workerIDs {
		exact := float64(targetVUs) * float64(distribution[id]) / float64(totalShare)
		whole := int32(exact)
		allocation[id] = whole
		assigned += whole
		remainders = append(remainders, remainder{workerID: id, frac: exact - float64(whole)})
	}

	// Hand out the VUs lost to truncation, largest fractional part first.
	sort.SliceStable(remainders, func(i, j int) bool {
		return remainders[i].frac > remainders[j].frac
	})
	for i := 0; assigned < targetVUs && i < len(remainders); i++ {
		id := remainders[i].workerID
		// A worker can only run the VUs it was allocated for the whole test.
		if allocation[id] >= distribution[id] {
			continue
		}
		allocation[id]++
		assigned++
	}

	return allocation
}

// GetActiveTest returns an active test
func (pd *PlanDistributor) GetActiveTest(testID string) (*ActiveTest, bool) {
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	test, ok := pd.activeTests[testID]
	return test, ok
}

// SetStatus records a terminal status for a test. The test is kept rather than
// deleted so its final state and metrics remain queryable.
func (pd *PlanDistributor) SetStatus(testID string, status models.TestStatus) {
	pd.mu.RLock()
	test, ok := pd.activeTests[testID]
	pd.mu.RUnlock()
	if !ok {
		return
	}

	test.mu.Lock()
	test.Status = status
	test.mu.Unlock()

	pd.logger.Info("test status changed",
		zap.String("test_id", testID),
		zap.String("status", string(status)),
	)
}

// MarkWorkerDone records that a worker finished a test and reports whether
// every assigned worker has now finished. Workers signal completion through the
// test_active flag on their metric batches, so this is idempotent.
func (pd *PlanDistributor) MarkWorkerDone(testID, workerID string) bool {
	pd.mu.RLock()
	test, ok := pd.activeTests[testID]
	pd.mu.RUnlock()
	if !ok {
		return false
	}

	test.mu.Lock()
	defer test.mu.Unlock()

	if _, assigned := test.Workers[workerID]; !assigned {
		return false
	}
	if test.done == nil {
		test.done = make(map[string]bool)
	}
	test.done[workerID] = true

	if test.Status != models.TestStatusRunning {
		return false // already terminal; don't re-fire completion
	}
	return len(test.done) >= len(test.Workers)
}

// RemoveActiveTest removes an active test
func (pd *PlanDistributor) RemoveActiveTest(testID string) {
	pd.mu.Lock()
	defer pd.mu.Unlock()
	delete(pd.activeTests, testID)
}

// RebalanceTest rebalances a test when a worker fails
func (pd *PlanDistributor) RebalanceTest(ctx context.Context, testID string, failedWorkerID string) error {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	test, ok := pd.activeTests[testID]
	if !ok {
		return fmt.Errorf("test not found: %s", testID)
	}

	// Get VUs that were on the failed worker
	reassignedVUs := test.Workers[failedWorkerID]
	delete(test.Workers, failedWorkerID)

	// Redistribute to remaining workers
	nodes := pd.nodeManager.GetNodes()
	availableNodes := make([]*models.WorkerNode, 0)
	for _, node := range nodes {
		if node.ID != failedWorkerID {
			availableNodes = append(availableNodes, node)
		}
	}

	if len(availableNodes) == 0 {
		return fmt.Errorf("no available workers for rebalancing")
	}

	// Distribute proportionally
	remaining := reassignedVUs
	for _, node := range availableNodes {
		if remaining <= 0 {
			break
		}

		available := node.MaxVUs - test.Workers[node.ID]
		if available > 0 {
			assigned := remaining
			if assigned > available {
				assigned = available
			}
			test.Workers[node.ID] += assigned
			remaining -= assigned
		}
	}

	pd.logger.Info("test rebalanced",
		zap.String("test_id", testID),
		zap.String("failed_worker", failedWorkerID),
		zap.Int32("reassigned_vus", reassignedVUs),
	)

	return nil
}
