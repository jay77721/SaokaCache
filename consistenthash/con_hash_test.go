package consistenthash

import (
	"fmt"
	"sync"
	"testing"
)

func TestMapBasic(t *testing.T) {
	t.Run("添加和获取节点", func(t *testing.T) {
		m := New()
		m.Add("node1", "node2", "node3")

		// 相同的 key 应该总是映射到同一个节点
		node := m.Get("test-key")
		if node == "" {
			t.Fatal("Get 不应返回空")
		}
		for i := 0; i < 10; i++ {
			if m.Get("test-key") != node {
				t.Fatal("相同 key 应该映射到相同节点")
			}
		}
	})

	t.Run("空 key 返回空", func(t *testing.T) {
		m := New()
		m.Add("node1")
		if m.Get("") != "" {
			t.Fatal("空 key 应该返回空")
		}
	})

	t.Run("无节点返回空", func(t *testing.T) {
		m := New()
		if m.Get("any-key") != "" {
			t.Fatal("无节点时应返回空")
		}
	})
}

func TestMap分布均匀性(t *testing.T) {
	m := New()
	nodes := []string{"node1", "node2", "node3", "node4"}
	m.Add(nodes...)

	// 统计每个节点的请求分布
	counts := make(map[string]int)
	total := 10000
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("key-%d", i)
		node := m.Get(key)
		counts[node]++
	}

	// 每个节点应该分到一些请求
	for _, node := range nodes {
		if counts[node] == 0 {
			t.Errorf("节点 %s 没有分配到任何请求", node)
		}
		// 理想情况下每个节点分到 25%，允许 10%-40% 的范围
		ratio := float64(counts[node]) / float64(total)
		if ratio < 0.10 || ratio > 0.40 {
			t.Errorf("节点 %s 分布不均匀: %.2f%%", node, ratio*100)
		}
	}
}

func TestMapRemove(t *testing.T) {
	m := New()
	m.Add("node1", "node2", "node3")

	// 记录 key1 映射到的节点
	_ = m.Get("key1")

	// 移除一个节点
	err := m.Remove("node2")
	if err != nil {
		t.Fatalf("移除节点失败: %v", err)
	}

	// 移除后，原来映射到 node2 的 key 应该映射到其他节点
	// 不直接检查 key1 的映射（可能本来就不在 node2）
	// 而是检查 node2 不再出现在结果中
	for i := 0; i < 1000; i++ {
		node := m.Get(fmt.Sprintf("key-%d", i))
		if node == "node2" {
			t.Fatal("移除后不应再映射到 node2")
		}
	}
}

func TestMapRemove不存在的节点(t *testing.T) {
	m := New()
	m.Add("node1")

	err := m.Remove("nonexistent")
	if err == nil {
		t.Fatal("移除不存在的节点应该返回错误")
	}
}

func TestMap添加空节点(t *testing.T) {
	m := New()
	err := m.Add()
	if err == nil {
		t.Fatal("不传入节点应该返回错误")
	}
}

func TestMap并发安全(t *testing.T) {
	m := New()
	m.Add("node1", "node2", "node3", "node4")

	var wg sync.WaitGroup
	goroutines := 10
	operations := 1000

	// 并发 Get
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < operations; i++ {
				key := fmt.Sprintf("g%d-key-%d", id, i)
				node := m.Get(key)
				if node == "" {
					t.Errorf("Get 返回空")
				}
			}
		}(g)
	}

	// 并发 Add/Remove
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			m.Add(fmt.Sprintf("extra-%d", i))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			m.Remove(fmt.Sprintf("extra-%d", i))
		}
	}()

	wg.Wait()
}

func TestMapGetStats(t *testing.T) {
	m := New()
	m.Add("node1", "node2")

	// 发一些请求
	for i := 0; i < 100; i++ {
		m.Get(fmt.Sprintf("key-%d", i))
	}

	stats := m.GetStats()
	if len(stats) == 0 {
		t.Fatal("统计信息不应为空")
	}

	// 统计比例之和应该约等于 1
	total := 0.0
	for _, ratio := range stats {
		total += ratio
	}
	if total < 0.99 || total > 1.01 {
		t.Errorf("统计比例之和应约等于 1, 实际 %.4f", total)
	}
}

func BenchmarkMapGet(b *testing.B) {
	m := New()
	for i := 0; i < 10; i++ {
		m.Add(fmt.Sprintf("node-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Get(fmt.Sprintf("key-%d", i))
	}
}
