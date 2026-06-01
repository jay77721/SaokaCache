package saokacache

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/youngyangyang04/SaokaCache/store"
)

// Cache 层错误定义
var (
	ErrCacheClosed   = errors.New("cache is closed")
	ErrCacheNotInit  = errors.New("cache not initialized")
	ErrKeyNotFound   = errors.New("key not found")
	ErrBloomRejected = errors.New("key rejected by bloom filter")
	ErrLoaderFailed  = errors.New("loader failed")
	ErrTypeAssertion = errors.New("value type assertion failed")
)

// Cache 是纯存储层封装，负责 lazy 初始化、存储操作和统计。
// 防穿透/击穿/雪崩逻辑由 CachePolicy 层处理。
type Cache struct {
	mu          sync.RWMutex
	store       store.Store  // 底层存储实现
	opts        CacheOptions // 缓存配置选项
	hits        int64        // 缓存命中次数
	misses      int64        // 缓存未命中次数
	initialized int32        // 原子变量，标记缓存是否已初始化
	closed      int32        // 原子变量，标记缓存是否已关闭
}

// CacheOptions 缓存配置选项
type CacheOptions struct {
	CacheType         store.CacheType                     // 缓存类型: LRU, LRU2 等
	MaxBytes          int64                               // 最大内存使用量
	BucketCount       uint16                              // 缓存桶数量 (用于 LRU2)
	CapPerBucket      uint16                              // 每个缓存桶的容量 (用于 LRU2)
	Level2Cap         uint16                              // 二级缓存桶的容量 (用于 LRU2)
	CleanupTime       time.Duration                       // 清理间隔
	OnEvicted         func(key string, value store.Value) // 驱逐回调
	DefaultTTL        time.Duration                       // 默认生存时间
	ExpectedElements  uint                                // n: 预期存储的元素量
	FalsePositiveRate float64                             // p: 允许的误判率
}

// DefaultCacheOptions 返回默认的缓存配置
func DefaultCacheOptions() CacheOptions {
	return CacheOptions{
		CacheType:         store.LRU2,
		MaxBytes:          8 * 1024 * 1024, // 8MB
		BucketCount:       16,
		CapPerBucket:      512,
		Level2Cap:         256,
		CleanupTime:       time.Minute,
		OnEvicted:         nil,
		DefaultTTL:        time.Minute,
		ExpectedElements:  1000000,
		FalsePositiveRate: 0.01,
	}
}

// NewCache 创建一个纯存储缓存实例
func NewCache(opts CacheOptions) *Cache {
	return &Cache{
		opts: opts,
	}
}

// ensureInitialized 确保缓存已初始化（lazy init）
func (c *Cache) ensureInitialized() {
	if atomic.LoadInt32(&c.initialized) == 1 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.initialized == 0 {
		storeOpts := store.Options{
			MaxBytes:        c.opts.MaxBytes,
			BucketCount:     c.opts.BucketCount,
			CapPerBucket:    c.opts.CapPerBucket,
			Level2Cap:       c.opts.Level2Cap,
			CleanupInterval: c.opts.CleanupTime,
			OnEvicted:       c.opts.OnEvicted,
		}

		c.store = store.NewStore(c.opts.CacheType, storeOpts)
		atomic.StoreInt32(&c.initialized, 1)
		logrus.Infof("[SaokaCache] 存储初始化: type=%s, maxBytes=%d", c.opts.CacheType, c.opts.MaxBytes)
	}
}

// Get 从存储中获取值（纯存储操作，无策略逻辑）
func (c *Cache) Get(key string) (ByteView, error) {
	if atomic.LoadInt32(&c.closed) == 1 {
		return ByteView{}, ErrCacheClosed
	}
	if atomic.LoadInt32(&c.initialized) == 0 {
		atomic.AddInt64(&c.misses, 1)
		return ByteView{}, ErrCacheNotInit
	}

	val, found := c.store.Get(key)
	if !found {
		atomic.AddInt64(&c.misses, 1)
		return ByteView{}, ErrKeyNotFound
	}

	bv, ok := val.(ByteView)
	if !ok {
		return ByteView{}, ErrTypeAssertion
	}

	// 检查空值占位符
	if bv.b == nil {
		atomic.AddInt64(&c.misses, 1)
		return ByteView{}, ErrKeyNotFound
	}

	atomic.AddInt64(&c.hits, 1)
	return bv, nil
}

// Set 向存储写入值
func (c *Cache) Set(key string, value ByteView) {
	if atomic.LoadInt32(&c.closed) == 1 {
		logrus.Warnf("[SaokaCache] 尝试写入已关闭的缓存: %s", key)
		return
	}
	c.ensureInitialized()

	if err := c.store.Set(key, value); err != nil {
		logrus.Warnf("[SaokaCache] 写入失败 key=%s: %v", key, err)
	}
}

// SetWithExpiration 向存储写入带过期时间的值
func (c *Cache) SetWithExpiration(key string, value ByteView, expirationTime time.Time) {
	if atomic.LoadInt32(&c.closed) == 1 {
		logrus.Warnf("[SaokaCache] 尝试写入已关闭的缓存: %s", key)
		return
	}
	c.ensureInitialized()

	expiration := time.Until(expirationTime)
	if expiration <= 0 {
		logrus.Debugf("[SaokaCache] key %s 已过期，跳过写入", key)
		return
	}

	if err := c.store.SetWithExpiration(key, value, expiration); err != nil {
		logrus.Warnf("[SaokaCache] 写入失败 key=%s: %v", key, err)
	}
}

// Delete 从存储中删除值
func (c *Cache) Delete(key string) bool {
	if atomic.LoadInt32(&c.closed) == 1 || atomic.LoadInt32(&c.initialized) == 0 {
		return false
	}
	return c.store.Delete(key)
}

// Clear 清空存储
func (c *Cache) Clear() {
	if atomic.LoadInt32(&c.closed) == 1 || atomic.LoadInt32(&c.initialized) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store.Clear()
	atomic.StoreInt64(&c.hits, 0)
	atomic.StoreInt64(&c.misses, 0)
}

// Len 返回存储项数量
func (c *Cache) Len() int {
	if atomic.LoadInt32(&c.closed) == 1 || atomic.LoadInt32(&c.initialized) == 0 {
		return 0
	}
	return c.store.Len()
}

// Close 关闭缓存，释放资源
func (c *Cache) Close() {
	if !atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.store != nil {
		if closer, ok := c.store.(interface{ Close() }); ok {
			closer.Close()
		}
		c.store = nil
	}
	atomic.StoreInt32(&c.initialized, 0)
	logrus.Debugf("[SaokaCache] 缓存关闭, hits=%d, misses=%d", atomic.LoadInt64(&c.hits), atomic.LoadInt64(&c.misses))
}

// Stats 返回统计信息
func (c *Cache) Stats() map[string]interface{} {
	stats := map[string]interface{}{
		"initialized": atomic.LoadInt32(&c.initialized) == 1,
		"closed":      atomic.LoadInt32(&c.closed) == 1,
		"hits":        atomic.LoadInt64(&c.hits),
		"misses":      atomic.LoadInt64(&c.misses),
	}

	if atomic.LoadInt32(&c.initialized) == 1 {
		stats["size"] = c.Len()
		totalRequests := stats["hits"].(int64) + stats["misses"].(int64)
		if totalRequests > 0 {
			stats["hit_rate"] = float64(stats["hits"].(int64)) / float64(totalRequests)
		} else {
			stats["hit_rate"] = 0.0
		}
	}
	return stats
}
