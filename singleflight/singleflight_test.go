package singleflight

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGroupDo(t *testing.T) {
	t.Run("基本功能", func(t *testing.T) {
		var g Group
		v, err := g.Do("key1", func() (interface{}, error) {
			return "value1", nil
		})
		if err != nil {
			t.Fatalf("Do 返回错误: %v", err)
		}
		if v != "value1" {
			t.Fatalf("期望 value1, 实际 %v", v)
		}
	})

	t.Run("错误传播", func(t *testing.T) {
		var g Group
		expectedErr := errors.New("test error")
		_, err := g.Do("key1", func() (interface{}, error) {
			return nil, expectedErr
		})
		if err != expectedErr {
			t.Fatalf("期望 %v, 实际 %v", expectedErr, err)
		}
	})
}

func TestGroupDo合并请求(t *testing.T) {
	var g Group
	var callCount int64

	// 使用 barrier 确保所有 goroutine 几乎同时发起请求
	var wg sync.WaitGroup
	barrier := make(chan struct{})
	results := make([]interface{}, 10)
	errs := make([]error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-barrier // 等待信号，确保同时开始
			results[idx], errs[idx] = g.Do("same-key", func() (interface{}, error) {
				atomic.AddInt64(&callCount, 1)
				time.Sleep(50 * time.Millisecond) // 模拟耗时操作
				return "result", nil
			})
		}(i)
	}

	close(barrier) // 释放所有 goroutine
	wg.Wait()

	// fn 只应该被调用一次（或少数几次，取决于时序）
	actualCalls := atomic.LoadInt64(&callCount)
	if actualCalls > 3 {
		t.Fatalf("fn 调用次数过多: %d", actualCalls)
	}

	// 所有请求都应该拿到相同的结果
	for i := 0; i < 10; i++ {
		if results[i] != "result" {
			t.Fatalf("第 %d 个请求结果不正确: %v", i, results[i])
		}
		if errs[i] != nil {
			t.Fatalf("第 %d 个请求不应有错误: %v", i, errs[i])
		}
	}
}

func TestGroupDo不同Key(t *testing.T) {
	var g Group
	var callCount int64

	// 不同的 key 应该独立执行
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			g.Do(fmt.Sprintf("key-%d", idx), func() (interface{}, error) {
				atomic.AddInt64(&callCount, 1)
				return idx, nil
			})
		}(i)
	}

	wg.Wait()

	// 每个 key 的 fn 都应该被调用一次
	if atomic.LoadInt64(&callCount) != 5 {
		t.Fatalf("fn 应该被调用 5 次, 实际 %d 次", callCount)
	}
}

func TestGroupDoPanic恢复(t *testing.T) {
	var g Group

	// fn panic 时，Do 应该返回 error 而不是崩溃
	_, err := g.Do("panic-key", func() (interface{}, error) {
		panic("test panic")
	})

	if err == nil {
		t.Fatal("panic 时 Do 应该返回 error")
	}

	// panic 后，同一个 key 的后续请求应该能正常工作
	v, err := g.Do("panic-key", func() (interface{}, error) {
		return "recovered", nil
	})
	if err != nil {
		t.Fatalf("panic 后 Do 应该能正常工作: %v", err)
	}
	if v != "recovered" {
		t.Fatalf("期望 recovered, 实际 %v", v)
	}
}
