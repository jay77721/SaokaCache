package saokacache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	groupsMu sync.RWMutex
	groups   = make(map[string]*Group)
)

// contextKey 自定义 context key 类型，避免与其他包冲突
type contextKey string

// fromPeerKey 用于标记请求来自其他节点
const fromPeerKey contextKey = "from_peer"

// ErrKeyRequired 键不能为空错误
var ErrKeyRequired = errors.New("key is required")

// ErrValueRequired 值不能为空错误
var ErrValueRequired = errors.New("value is required")

// ErrGroupClosed 组已关闭错误
var ErrGroupClosed = errors.New("cache group is closed")

// Getter 加载键值的回调函数接口
type Getter interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

// GetterFunc 函数类型实现 Getter 接口
type GetterFunc func(ctx context.Context, key string) ([]byte, error)

// Get 实现 Getter 接口
func (f GetterFunc) Get(ctx context.Context, key string) ([]byte, error) {
	return f(ctx, key)
}

// peerAwareGetter 包装 peer 查找 + 原始 getter，作为 CachePolicy 的 loader
type peerAwareGetter struct {
	group *Group
}

// Get 先尝试从远程节点获取，失败则回退到原始 getter
func (p *peerAwareGetter) Get(ctx context.Context, key string) ([]byte, error) {
	// 尝试从远程节点获取
	if p.group.peers != nil {
		peer, ok, isSelf := p.group.peers.PickPeer(key)
		if ok && !isSelf {
			bytes, err := peer.Get(p.group.name, key)
			if err == nil {
				atomic.AddInt64(&p.group.stats.peerHits, 1)
				return bytes, nil
			}
			atomic.AddInt64(&p.group.stats.peerMisses, 1)
			logrus.Warnf("[SaokaCache] 从 peer 获取失败: %v", err)
		}
	}

	// 回退到原始 getter（如数据库）
	bytes, err := p.group.getter.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get data: %w", err)
	}

	atomic.AddInt64(&p.group.stats.loaderHits, 1)
	return cloneBytes(bytes), nil
}

// Group 是一个缓存命名空间
type Group struct {
	name       string
	getter     Getter
	policy     *CachePolicy   // 缓存策略层（封装存储 + 防穿透/击穿/雪崩）
	peers      PeerPicker
	expiration time.Duration  // 缓存过期时间，0表示永不过期
	closed     int32          // 原子变量，标记组是否已关闭
	stats      groupStats     // 统计信息
	syncWG     sync.WaitGroup // 跟踪同步 goroutine
}

// groupStats 保存组的统计信息
type groupStats struct {
	loads        int64 // 加载次数
	localHits    int64 // 本地缓存命中次数
	localMisses  int64 // 本地缓存未命中次数
	peerHits     int64 // 从对等节点获取成功次数
	peerMisses   int64 // 从对等节点获取失败次数
	loaderHits   int64 // 从加载器获取成功次数
	loaderErrors int64 // 从加载器获取失败次数
	loadDuration int64 // 加载总耗时（纳秒）
}

// GroupOption 定义Group的配置选项
type GroupOption func(*Group)

// WithExpiration 设置缓存过期时间
func WithExpiration(d time.Duration) GroupOption {
	return func(g *Group) {
		g.expiration = d
	}
}

// WithPeers 设置分布式节点
func WithPeers(peers PeerPicker) GroupOption {
	return func(g *Group) {
		g.peers = peers
	}
}

// WithCacheOptions 设置缓存选项
func WithCacheOptions(opts CacheOptions) GroupOption {
	return func(g *Group) {
		cache := NewCache(opts)
		g.policy = NewCachePolicy(cache, &peerAwareGetter{group: g}, opts)
	}
}

// NewGroup 创建一个新的 Group 实例
func NewGroup(name string, cacheBytes int64, getter Getter, opts ...GroupOption) *Group {
	if getter == nil {
		panic("nil Getter")
	}

	// 创建默认缓存选项
	cacheOpts := DefaultCacheOptions()
	cacheOpts.MaxBytes = cacheBytes

	g := &Group{
		name:   name,
		getter: getter,
	}

	// 创建缓存策略层（peerAwareGetter 引用 g，所以先创建 g）
	cache := NewCache(cacheOpts)
	g.policy = NewCachePolicy(cache, &peerAwareGetter{group: g}, cacheOpts)

	// 应用选项
	for _, opt := range opts {
		opt(g)
	}

	// 注册到全局组映射
	groupsMu.Lock()
	defer groupsMu.Unlock()

	if _, exists := groups[name]; exists {
		logrus.Warnf("Group with name %s already exists, will be replaced", name)
	}

	groups[name] = g
	logrus.Infof("[SaokaCache] 创建缓存组 [%s], cacheBytes=%d, expiration=%v", name, cacheBytes, g.expiration)

	return g
}

// GetGroup 获取指定名称的组
func GetGroup(name string) *Group {
	groupsMu.RLock()
	defer groupsMu.RUnlock()
	return groups[name]
}

// Get 从缓存获取数据
func (g *Group) Get(ctx context.Context, key string) (ByteView, error) {
	if atomic.LoadInt32(&g.closed) == 1 {
		return ByteView{}, ErrGroupClosed
	}
	if key == "" {
		return ByteView{}, ErrKeyRequired
	}

	// 通过策略层获取（包含 bloom + store + singleflight + peer + getter）
	startTime := time.Now()
	view, err := g.policy.Get(ctx, key)
	loadDuration := time.Since(startTime).Nanoseconds()

	if err != nil {
		atomic.AddInt64(&g.stats.localMisses, 1)
		atomic.AddInt64(&g.stats.loaderErrors, 1)
		return ByteView{}, err
	}

	atomic.AddInt64(&g.stats.localHits, 1)
	atomic.AddInt64(&g.stats.loads, 1)
	atomic.AddInt64(&g.stats.loadDuration, loadDuration)
	return view, nil
}

// Set 设置缓存值
func (g *Group) Set(ctx context.Context, key string, value []byte) error {
	if atomic.LoadInt32(&g.closed) == 1 {
		return ErrGroupClosed
	}
	if key == "" {
		return ErrKeyRequired
	}
	if len(value) == 0 {
		return ErrValueRequired
	}

	isPeerRequest := ctx.Value(fromPeerKey) != nil
	view := ByteView{b: cloneBytes(value)}

	// 通过策略层写入（带 TTL 抖动）
	if g.expiration > 0 {
		g.policy.SetWithExpiration(key, view, time.Now().Add(g.expiration))
	} else {
		g.policy.Set(key, view)
	}

	// 如果不是 peer 同步请求，且启用了分布式模式，同步到其他节点
	if !isPeerRequest && g.peers != nil {
		go g.syncToPeers(ctx, "set", key, value)
	}

	return nil
}

// Delete 删除缓存值
func (g *Group) Delete(ctx context.Context, key string) error {
	if atomic.LoadInt32(&g.closed) == 1 {
		return ErrGroupClosed
	}
	if key == "" {
		return ErrKeyRequired
	}

	g.policy.Delete(key)

	isPeerRequest := ctx.Value(fromPeerKey) != nil
	if !isPeerRequest && g.peers != nil {
		go g.syncToPeers(ctx, "delete", key, nil)
	}

	return nil
}

// syncToPeers 同步操作到其他节点
func (g *Group) syncToPeers(ctx context.Context, op string, key string, value []byte) {
	if g.peers == nil {
		return
	}

	peers := g.peers.PickAllPeers()
	if len(peers) == 0 {
		return
	}

	syncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	syncCtx = context.WithValue(syncCtx, fromPeerKey, true)

	for _, peer := range peers {
		g.syncWG.Add(1)
		go func(p Peer) {
			defer g.syncWG.Done()

			var err error
			switch op {
			case "set":
				err = p.Set(syncCtx, g.name, key, value)
			case "delete":
				_, err = p.Delete(g.name, key)
			}

			if err != nil {
				logrus.Errorf("[SaokaCache] 同步 %s 到 peer 失败: %v", op, err)
			}
		}(peer)
	}
}

// Clear 清空缓存
func (g *Group) Clear() {
	if atomic.LoadInt32(&g.closed) == 1 {
		return
	}
	g.policy.Clear()
	logrus.Infof("[SaokaCache] 清空缓存组 [%s]", g.name)
}

// Close 关闭组并释放资源
func (g *Group) Close() error {
	if !atomic.CompareAndSwapInt32(&g.closed, 0, 1) {
		return nil
	}

	// 等待所有同步 goroutine 完成（最多 10 秒）
	done := make(chan struct{})
	go func() {
		g.syncWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		logrus.Warnf("[SaokaCache] group [%s] close: sync goroutines timed out", g.name)
	}

	if g.policy != nil {
		g.policy.Close()
	}

	groupsMu.Lock()
	delete(groups, g.name)
	groupsMu.Unlock()

	logrus.Infof("[SaokaCache] 关闭缓存组 [%s]", g.name)
	return nil
}

// RegisterPeers 注册PeerPicker
func (g *Group) RegisterPeers(peers PeerPicker) {
	if g.peers != nil {
		panic("RegisterPeers called more than once")
	}
	g.peers = peers
	logrus.Infof("[SaokaCache] 注册 peers 到组 [%s]", g.name)
}

// Stats 返回缓存统计信息
func (g *Group) Stats() map[string]interface{} {
	stats := map[string]interface{}{
		"name":          g.name,
		"closed":        atomic.LoadInt32(&g.closed) == 1,
		"expiration":    g.expiration,
		"loads":         atomic.LoadInt64(&g.stats.loads),
		"local_hits":    atomic.LoadInt64(&g.stats.localHits),
		"local_misses":  atomic.LoadInt64(&g.stats.localMisses),
		"peer_hits":     atomic.LoadInt64(&g.stats.peerHits),
		"peer_misses":   atomic.LoadInt64(&g.stats.peerMisses),
		"loader_hits":   atomic.LoadInt64(&g.stats.loaderHits),
		"loader_errors": atomic.LoadInt64(&g.stats.loaderErrors),
	}

	totalGets := stats["local_hits"].(int64) + stats["local_misses"].(int64)
	if totalGets > 0 {
		stats["hit_rate"] = float64(stats["local_hits"].(int64)) / float64(totalGets)
	}

	totalLoads := stats["loads"].(int64)
	if totalLoads > 0 {
		stats["avg_load_time_ms"] = float64(atomic.LoadInt64(&g.stats.loadDuration)) / float64(totalLoads) / float64(time.Millisecond)
	}

	if g.policy != nil {
		cacheStats := g.policy.Stats()
		for k, v := range cacheStats {
			stats["cache_"+k] = v
		}
	}

	return stats
}

// ListGroups 返回所有缓存组的名称
func ListGroups() []string {
	groupsMu.RLock()
	defer groupsMu.RUnlock()

	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	return names
}

// DestroyGroup 销毁指定名称的缓存组
func DestroyGroup(name string) bool {
	groupsMu.Lock()
	defer groupsMu.Unlock()

	if g, exists := groups[name]; exists {
		g.Close()
		delete(groups, name)
		logrus.Infof("[SaokaCache] 销毁缓存组 [%s]", name)
		return true
	}
	return false
}

// DestroyAllGroups 销毁所有缓存组
func DestroyAllGroups() {
	groupsMu.Lock()
	defer groupsMu.Unlock()

	for name, g := range groups {
		g.Close()
		delete(groups, name)
		logrus.Infof("[SaokaCache] 销毁缓存组 [%s]", name)
	}
}
