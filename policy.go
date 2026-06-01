package saokacache

import (
	"context"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/youngyangyang04/SaokaCache/bloom"
	"github.com/youngyangyang04/SaokaCache/singleflight"
)

// CachePolicy 缓存策略层，负责防穿透、防击穿、防雪崩。
// 包装 Cache（纯存储），在其之上实现缓存策略逻辑。
type CachePolicy struct {
	cache  *Cache              // 底层存储
	bf     *bloom.BloomFilter  // 布隆过滤器（防穿透）
	sfg    *singleflight.Group // 请求合并（防击穿）
	loader Getter              // 回源接口
	opts   CacheOptions        // 配置选项
}

// NewCachePolicy 创建缓存策略层
func NewCachePolicy(cache *Cache, loader Getter, opts CacheOptions) *CachePolicy {
	return &CachePolicy{
		cache:  cache,
		bf:     bloom.NewBloomFilter(opts.ExpectedElements, opts.FalsePositiveRate),
		sfg:    &singleflight.Group{},
		loader: loader,
		opts:   opts,
	}
}

// Get 从缓存获取值，集成防穿透、击穿、雪崩逻辑
func (p *CachePolicy) Get(ctx context.Context, key string) (ByteView, error) {
	// 1. 【防穿透】布隆过滤器第一道防线
	if p.bf != nil && !p.bf.Contains(key) {
		return ByteView{}, ErrBloomRejected
	}

	// 2. 尝试从底层存储获取
	val, err := p.cache.Get(key)
	if err == nil {
		return val, nil
	}

	// 3. 缓存未命中，使用 singleflight 合并请求并回源（防击穿）
	return p.loadFromSource(ctx, key)
}

// Set 写入缓存（带默认 TTL + 雪崩抖动）
func (p *CachePolicy) Set(key string, value ByteView) {
	if p.opts.DefaultTTL > 0 {
		expiration := p.addJitter(p.opts.DefaultTTL)
		p.cache.SetWithExpiration(key, value, time.Now().Add(expiration))
	} else {
		p.cache.Set(key, value)
	}
}

// SetWithExpiration 写入缓存（带指定过期时间 + 雪崩抖动）
func (p *CachePolicy) SetWithExpiration(key string, value ByteView, expirationTime time.Time) {
	expiration := time.Until(expirationTime)
	if expiration <= 0 {
		logrus.Debugf("[SaokaCache] key %s 已过期，跳过写入", key)
		return
	}
	expiration = p.addJitter(expiration)
	p.cache.SetWithExpiration(key, value, time.Now().Add(expiration))
}

// Delete 从缓存删除
func (p *CachePolicy) Delete(key string) bool {
	return p.cache.Delete(key)
}

// Clear 清空缓存
func (p *CachePolicy) Clear() {
	p.cache.Clear()
}

// Close 关闭策略层和底层缓存
func (p *CachePolicy) Close() {
	p.cache.Close()
}

// Stats 返回统计信息
func (p *CachePolicy) Stats() map[string]interface{} {
	return p.cache.Stats()
}

// loadFromSource 使用 singleflight 合并并发请求，从数据源加载
func (p *CachePolicy) loadFromSource(ctx context.Context, key string) (ByteView, error) {
	viewi, err := p.sfg.Do(key, func() (interface{}, error) {
		// Double Check: 再次检查缓存，防止排队等待期间别人已经写好了
		if val, found := p.cache.store.Get(key); found {
			if bv, ok := val.(ByteView); ok && bv.b != nil {
				return bv, nil
			}
		}

		// 调用回源函数
		bytes, err := p.loader.Get(ctx, key)
		if err != nil {
			// 【防穿透】数据库也没有，缓存空值占位符，短 TTL
			nullView := ByteView{b: nil}
			p.cache.SetWithExpiration(key, nullView, time.Now().Add(time.Minute))
			return nil, ErrLoaderFailed
		}

		data := ByteView{b: bytes}

		// 【防雪崩】写入缓存，SetWithExpiration 会自动计算随机抖动
		p.Set(key, data)

		// 同步更新布隆过滤器
		if p.bf != nil {
			p.bf.Add(key)
		}

		return data, nil
	})

	if err != nil {
		atomic.AddInt64(&p.cache.misses, 1)
		return ByteView{}, err
	}

	atomic.AddInt64(&p.cache.hits, 1)
	return viewi.(ByteView), nil
}

// addJitter 为过期时间添加随机抖动（±10%），防止雪崩
func (p *CachePolicy) addJitter(duration time.Duration) time.Duration {
	if duration <= time.Second {
		return duration
	}
	jitterRange := int64(duration) / 10
	jitter := time.Duration(rand.Int63n(jitterRange*2) - jitterRange)
	return duration + jitter
}
