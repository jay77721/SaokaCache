package consistenthash

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Map 一致性哈希实现
type Map struct {
	mu sync.RWMutex
	// 配置信息
	config *Config
	// 哈希环
	keys []int
	// 哈希环到节点的映射
	hashMap map[int]string
	// 节点到虚拟节点数量的映射
	nodeReplicas map[string]int
	// 节点负载统计（使用 sync.Map 避免读锁下的写操作竞态）
	nodeCounts sync.Map // map[string]*atomic.Int64
	// 总请求数
	totalRequests atomic.Int64
}

// New 创建一致性哈希实例
func New(opts ...Option) *Map {
	m := &Map{
		config:       DefaultConfig,
		hashMap:      make(map[int]string),
		nodeReplicas: make(map[string]int),
	}

	for _, opt := range opts {
		opt(m)
	}

	m.startBalancer() // 启动负载均衡器
	return m
}

// Option 配置选项
type Option func(*Map)

// WithConfig 设置配置
func WithConfig(config *Config) Option {
	return func(m *Map) {
		m.config = config
	}
}

// Add 添加节点
func (m *Map) Add(nodes ...string) error {
	if len(nodes) == 0 {
		return errors.New("no nodes provided")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, node := range nodes {
		if node == "" {
			continue
		}

		// 为节点添加虚拟节点
		m.addNode(node, m.config.DefaultReplicas)
	}

	// 重新排序
	sort.Ints(m.keys)
	return nil
}

// Remove 移除节点
func (m *Map) Remove(node string) error {
	if node == "" {
		return errors.New("invalid node")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.removeNode(node)
}

// removeNode 内部移除节点方法，调用前必须持有写锁
func (m *Map) removeNode(node string) error {
	replicas := m.nodeReplicas[node]
	if replicas == 0 {
		return fmt.Errorf("node %s not found", node)
	}

	// 移除节点的所有虚拟节点
	for i := 0; i < replicas; i++ {
		hash := int(m.config.HashFunc([]byte(fmt.Sprintf("%s-%d", node, i))))
		delete(m.hashMap, hash)
		for j := 0; j < len(m.keys); j++ {
			if m.keys[j] == hash {
				m.keys = append(m.keys[:j], m.keys[j+1:]...)
				break
			}
		}
	}

	delete(m.nodeReplicas, node)
	m.nodeCounts.Delete(node)
	return nil
}

// Get 获取节点
func (m *Map) Get(key string) string {
	if key == "" {
		return ""
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.keys) == 0 {
		return ""
	}

	hash := int(m.config.HashFunc([]byte(key)))
	// 二分查找
	idx := sort.Search(len(m.keys), func(i int) bool {
		return m.keys[i] >= hash
	})

	// 处理边界情况
	if idx == len(m.keys) {
		idx = 0
	}

	node := m.hashMap[m.keys[idx]]

	// 使用 atomic 更新负载统计，避免读锁下的写操作竞态
	val, _ := m.nodeCounts.LoadOrStore(node, &atomic.Int64{})
	val.(*atomic.Int64).Add(1)
	m.totalRequests.Add(1)

	return node
}

// addNode 添加节点的虚拟节点
func (m *Map) addNode(node string, replicas int) {
	for i := 0; i < replicas; i++ {
		hash := int(m.config.HashFunc([]byte(fmt.Sprintf("%s-%d", node, i))))
		m.keys = append(m.keys, hash)
		m.hashMap[hash] = node
	}
	m.nodeReplicas[node] = replicas
}

// checkAndRebalance 检查并重新平衡虚拟节点
func (m *Map) checkAndRebalance() {
	total := m.totalRequests.Load()
	if total < 10000 {
		return // 样本太少，不进行调整
	}

	// 计算负载情况（需要读锁保护 nodeReplicas）
	m.mu.RLock()
	nodeCount := len(m.nodeReplicas)
	m.mu.RUnlock()
	if nodeCount == 0 {
		return
	}
	avgLoad := float64(total) / float64(nodeCount)
	var maxDiff float64

	m.nodeCounts.Range(func(key, value any) bool {
		count := value.(*atomic.Int64).Load()
		diff := math.Abs(float64(count) - avgLoad)
		if diff/avgLoad > maxDiff {
			maxDiff = diff / avgLoad
		}
		return true
	})

	// 如果负载不均衡度超过阈值，调整虚拟节点
	if maxDiff > m.config.LoadBalanceThreshold {
		m.rebalanceNodes()
	}
}

// rebalanceNodes 重新平衡节点
func (m *Map) rebalanceNodes() {
	m.mu.Lock()
	defer m.mu.Unlock()

	total := m.totalRequests.Load()
	nodeCount := len(m.nodeReplicas)
	if nodeCount == 0 {
		return
	}
	avgLoad := float64(total) / float64(nodeCount)

	// 调整每个节点的虚拟节点数量
	m.nodeCounts.Range(func(key, value any) bool {
		node := key.(string)
		count := value.(*atomic.Int64).Load()
		currentReplicas := m.nodeReplicas[node]
		loadRatio := float64(count) / avgLoad

		var newReplicas int
		if loadRatio > 1 {
			// 负载过高，减少虚拟节点
			newReplicas = int(float64(currentReplicas) / loadRatio)
		} else {
			// 负载过低，增加虚拟节点
			newReplicas = int(float64(currentReplicas) * (2 - loadRatio))
		}

		// 确保在限制范围内
		if newReplicas < m.config.MinReplicas {
			newReplicas = m.config.MinReplicas
		}
		if newReplicas > m.config.MaxReplicas {
			newReplicas = m.config.MaxReplicas
		}

		if newReplicas != currentReplicas {
			// 重新添加节点的虚拟节点（使用 removeNode 避免死锁）
			if err := m.removeNode(node); err != nil {
				return true // 如果移除失败，跳过这个节点
			}
			m.addNode(node, newReplicas)
		}
		return true
	})

	// 重置计数器
	m.nodeCounts.Range(func(key, value any) bool {
		value.(*atomic.Int64).Store(0)
		return true
	})
	m.totalRequests.Store(0)

	// 重新排序
	sort.Ints(m.keys)
}

// GetStats 获取负载统计信息
func (m *Map) GetStats() map[string]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make(map[string]float64)
	total := m.totalRequests.Load()
	if total == 0 {
		return stats
	}

	m.nodeCounts.Range(func(key, value any) bool {
		node := key.(string)
		count := value.(*atomic.Int64).Load()
		stats[node] = float64(count) / float64(total)
		return true
	})
	return stats
}

// 将checkAndRebalance移到单独的goroutine中
func (m *Map) startBalancer() {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			m.checkAndRebalance()
		}
	}()
}
