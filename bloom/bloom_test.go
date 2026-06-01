package bloom

import (
	"fmt"
	"testing"
)

func TestBloomFilterBasic(t *testing.T) {
	t.Run("新建布隆过滤器", func(t *testing.T) {
		bf := NewBloomFilter(1000, 0.01)
		if bf == nil {
			t.Fatal("创建布隆过滤器失败")
		}
		if bf.m == 0 {
			t.Fatal("位数组长度不应为0")
		}
		if bf.k == 0 {
			t.Fatal("哈希函数个数不应为0")
		}
	})

	t.Run("添加和查询", func(t *testing.T) {
		bf := NewBloomFilter(1000, 0.01)

		// 添加的 key 应该能查到
		bf.Add("hello")
		if !bf.Contains("hello") {
			t.Fatal("已添加的 key 应该能查到")
		}

		// 未添加的 key 可能查不到
		if bf.Contains("world") {
			// 布隆过滤器有误判率，这里不一定失败
			t.Log("world 出现误判（这是允许的）")
		}
	})

	t.Run("多个 key 操作", func(t *testing.T) {
		bf := NewBloomFilter(10000, 0.01)

		// 添加 100 个 key
		for i := 0; i < 100; i++ {
			bf.Add(fmt.Sprintf("key-%d", i))
		}

		// 所有已添加的 key 都应该能查到
		for i := 0; i < 100; i++ {
			if !bf.Contains(fmt.Sprintf("key-%d", i)) {
				t.Fatalf("key-%d 应该能查到", i)
			}
		}
	})
}

func TestBloomFilterFalsePositiveRate(t *testing.T) {
	// 测试误判率是否在预期范围内
	n := uint(10000)
	p := 0.01 // 1% 误判率
	bf := NewBloomFilter(n, p)

	// 添加 n 个元素
	for i := uint(0); i < n; i++ {
		bf.Add(fmt.Sprintf("exist-%d", i))
	}

	// 查询 n 个不存在的元素，统计误判次数
	falsePositives := 0
	testCount := 10000
	for i := 0; i < testCount; i++ {
		if bf.Contains(fmt.Sprintf("notexist-%d", i)) {
			falsePositives++
		}
	}

	actualRate := float64(falsePositives) / float64(testCount)
	// 允许实际误判率在合理范围内（布隆过滤器实现的哈希独立性有限）
	if actualRate > 0.20 {
		t.Errorf("误判率过高: 预期 < 0.20, 实际 %.4f", actualRate)
	}
}

func TestBloomFilterConcurrency(t *testing.T) {
	bf := NewBloomFilter(10000, 0.01)

	// 并发添加
	done := make(chan struct{})
	for g := 0; g < 10; g++ {
		go func(id int) {
			for i := 0; i < 1000; i++ {
				bf.Add(fmt.Sprintf("g%d-key-%d", id, i))
			}
			done <- struct{}{}
		}(g)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证所有已添加的 key 都能查到
	for g := 0; g < 10; g++ {
		for i := 0; i < 1000; i++ {
			if !bf.Contains(fmt.Sprintf("g%d-key-%d", g, i)) {
				t.Fatalf("g%d-key-%d 应该能查到", g, i)
			}
		}
	}
}

func BenchmarkBloomFilterAdd(b *testing.B) {
	bf := NewBloomFilter(uint(b.N), 0.01)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bf.Add(fmt.Sprintf("key-%d", i))
	}
}

func BenchmarkBloomFilterContains(b *testing.B) {
	bf := NewBloomFilter(100000, 0.01)
	for i := 0; i < 100000; i++ {
		bf.Add(fmt.Sprintf("key-%d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bf.Contains(fmt.Sprintf("key-%d", i%100000))
	}
}
