package orchestrator

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// NodeManager manages worker node registration and consistent hashing
type NodeManager struct {
	etcdClient *clientv3.Client
	logger     *zap.Logger
	nodes      map[string]*models.WorkerNode
	ring       *ConsistentHashRing
	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	prefix     string
}

// ConsistentHashRing implements consistent hashing
type ConsistentHashRing struct {
	keys    []uint32
	hashMap map[uint32]string
	mu      sync.RWMutex
}

// NewNodeManager creates a new node manager
func NewNodeManager(etcdClient *clientv3.Client, prefix string, logger *zap.Logger) *NodeManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &NodeManager{
		etcdClient: etcdClient,
		logger:     logger,
		nodes:      make(map[string]*models.WorkerNode),
		ring:       NewConsistentHashRing(),
		ctx:        ctx,
		cancel:     cancel,
		prefix:     prefix,
	}
}

// RegisterNode registers a worker node
func (nm *NodeManager) RegisterNode(node *models.WorkerNode) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	nm.nodes[node.ID] = node
	nm.ring.Add(node.ID, node.Address)

	// Register in etcd
	key := fmt.Sprintf("%s/nodes/%s", nm.prefix, node.ID)
	value := fmt.Sprintf("%s|%d", node.Address, node.MaxVUs)
	
	_, err := nm.etcdClient.Put(nm.ctx, key, value)
	if err != nil {
		return fmt.Errorf("failed to register node in etcd: %w", err)
	}

	nm.logger.Info("node registered",
		zap.String("node_id", node.ID),
		zap.String("address", node.Address),
		zap.Int32("max_vus", node.MaxVUs),
	)

	return nil
}

// UnregisterNode unregisters a worker node
func (nm *NodeManager) UnregisterNode(nodeID string) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	delete(nm.nodes, nodeID)
	nm.ring.Remove(nodeID)

	// Remove from etcd
	key := fmt.Sprintf("%s/nodes/%s", nm.prefix, nodeID)
	_, err := nm.etcdClient.Delete(nm.ctx, key)
	if err != nil {
		return fmt.Errorf("failed to unregister node from etcd: %w", err)
	}

	nm.logger.Info("node unregistered", zap.String("node_id", nodeID))
	return nil
}

// GetNode returns a node by ID
func (nm *NodeManager) GetNode(nodeID string) (*models.WorkerNode, bool) {
	nm.mu.RLock()
	defer nm.mu.RUnlock()
	node, ok := nm.nodes[nodeID]
	return node, ok
}

// GetNodes returns all registered nodes
func (nm *NodeManager) GetNodes() []*models.WorkerNode {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	nodes := make([]*models.WorkerNode, 0, len(nm.nodes))
	for _, node := range nm.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// GetNodeForKey returns the node responsible for a given key using consistent hashing
func (nm *NodeManager) GetNodeForKey(key string) (*models.WorkerNode, error) {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	nodeID := nm.ring.Get(key)
	if nodeID == "" {
		return nil, fmt.Errorf("no nodes available")
	}

	node, ok := nm.nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", nodeID)
	}

	return node, nil
}

// StartHealthCheck starts health check goroutines for all nodes
func (nm *NodeManager) StartHealthCheck(interval time.Duration, timeout time.Duration) {
	go nm.healthCheckLoop(interval, timeout)
}

// healthCheckLoop periodically checks node health
func (nm *NodeManager) healthCheckLoop(interval time.Duration, timeout time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-nm.ctx.Done():
			return
		case <-ticker.C:
			nm.checkNodeHealth(timeout)
		}
	}
}

// checkNodeHealth checks health of all nodes
func (nm *NodeManager) checkNodeHealth(timeout time.Duration) {
	nm.mu.RLock()
	nodes := make([]*models.WorkerNode, 0, len(nm.nodes))
	for _, node := range nm.nodes {
		nodes = append(nodes, node)
	}
	nm.mu.RUnlock()

	for _, node := range nodes {
		// TODO: Implement actual health check (ping worker, check last heartbeat)
		// For now, just check if node hasn't been seen recently
		if time.Since(node.LastSeen) > timeout {
			nm.logger.Warn("node health check failed",
				zap.String("node_id", node.ID),
				zap.Duration("time_since_last_seen", time.Since(node.LastSeen)),
			)
			// Mark as unhealthy but don't remove yet
		}
	}
}

// Shutdown shuts down the node manager
func (nm *NodeManager) Shutdown() error {
	nm.cancel()
	return nil
}

// NewConsistentHashRing creates a new consistent hash ring
func NewConsistentHashRing() *ConsistentHashRing {
	return &ConsistentHashRing{
		keys:    make([]uint32, 0),
		hashMap: make(map[uint32]string),
	}
}

// Add adds a node to the ring
func (ring *ConsistentHashRing) Add(nodeID, address string) {
	ring.mu.Lock()
	defer ring.mu.Unlock()

	// Create multiple virtual nodes for better distribution
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("%s:%d", nodeID, i)
		hash := hashKey(key)
		ring.hashMap[hash] = nodeID
		ring.keys = append(ring.keys, hash)
	}

	sort.Slice(ring.keys, func(i, j int) bool {
		return ring.keys[i] < ring.keys[j]
	})
}

// Remove removes a node from the ring
func (ring *ConsistentHashRing) Remove(nodeID string) {
	ring.mu.Lock()
	defer ring.mu.Unlock()

	// Remove all virtual nodes
	newKeys := make([]uint32, 0)
	for hash, id := range ring.hashMap {
		if id == nodeID {
			delete(ring.hashMap, hash)
		} else {
			newKeys = append(newKeys, hash)
		}
	}

	ring.keys = newKeys
	sort.Slice(ring.keys, func(i, j int) bool {
		return ring.keys[i] < ring.keys[j]
	})
}

// Get returns the node ID for a given key
func (ring *ConsistentHashRing) Get(key string) string {
	ring.mu.RLock()
	defer ring.mu.RUnlock()

	if len(ring.keys) == 0 {
		return ""
	}

	hash := hashKey(key)
	
	// Find the first key >= hash
	idx := sort.Search(len(ring.keys), func(i int) bool {
		return ring.keys[i] >= hash
	})

	// Wrap around if needed
	if idx == len(ring.keys) {
		idx = 0
	}

	return ring.hashMap[ring.keys[idx]]
}

// hashKey hashes a key to a uint32
func hashKey(key string) uint32 {
	h := sha256.Sum256([]byte(key))
	return uint32(h[0])<<24 | uint32(h[1])<<16 | uint32(h[2])<<8 | uint32(h[3])
}
