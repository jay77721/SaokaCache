package store

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// noExpiration 永不过期标记，使用最大 int64 值
const noExpiration int64 = math.MaxInt64

type lru2Store struct {
	locks       []sync.Mutex
	caches      [][2]*cache
	onEvicted   func(key string, value Value)
	cleanupTick *time.Ticker
	mask        int32
}

func newLRU2Cache(opts Options) *lru2Store {
	if opts.BucketCount == 0 {
		opts.BucketCount = 16
	}
	if opts.CapPerBucket == 0 {
		opts.CapPerBucket = 1024
	}
	if opts.Level2Cap == 0 {
		opts.Level2Cap = 1024
	}
	if opts.CleanupInterval <= 0 {
		opts.CleanupInterval = time.Minute
	}

	mask := maskOfNextPowOf2(opts.BucketCount)
	s := &lru2Store{
		locks:       make([]sync.Mutex, mask+1),
		caches:      make([][2]*cache, mask+1),
		onEvicted:   opts.OnEvicted,
		cleanupTick: time.NewTicker(opts.CleanupInterval),
		mask:        int32(mask),
	}

	for i := range s.caches {
		s.caches[i][0] = Create(opts.CapPerBucket)
		s.caches[i][1] = Create(opts.Level2Cap)
	}

	if opts.CleanupInterval > 0 {
		go s.cleanupLoop()
	}

	return s
}

func (s *lru2Store) Get(key string) (Value, bool) {
	idx := hashBKRD(key) & s.mask
	s.locks[idx].Lock()
	defer s.locks[idx].Unlock()

	currentTime := Now()

	// 首先检查一级缓存
	n1, status1, expireAt := s.caches[idx][0].del(key)
	if status1 > 0 {
		// 从一级缓存找到项目，检查是否过期
		if expireAt > 0 && expireAt != noExpiration && currentTime >= expireAt {
			return nil, false
		}

		// 项目有效，将其移至二级缓存
		ev := s.caches[idx][1].put(key, n1.v, expireAt)
		// 如果二级缓存也淘汰了项，触发回调
		if ev.key != "" && s.onEvicted != nil {
			s.onEvicted(ev.key, ev.val)
		}
		return n1.v, true
	}

	// 一级缓存未找到，检查二级缓存
	n2, status2 := s._get(key, idx, 1)
	if status2 > 0 && n2 != nil {
		if n2.expireAt > 0 && n2.expireAt != noExpiration && currentTime >= n2.expireAt {
			s.delete(key, idx)
			return nil, false
		}

		return n2.v, true
	}

	return nil, false
}

func (s *lru2Store) Set(key string, value Value) error {
	return s.SetWithExpiration(key, value, 0)
}

func (s *lru2Store) SetWithExpiration(key string, value Value, expiration time.Duration) error {
	// 计算过期时间：0 表示永不过期
	expireAt := noExpiration
	if expiration > 0 {
		expireAt = Now() + expiration.Nanoseconds()
	}

	idx := hashBKRD(key) & s.mask
	s.locks[idx].Lock()
	defer s.locks[idx].Unlock()

	// 放入一级缓存，获取被淘汰的项
	ev := s.caches[idx][0].put(key, value, expireAt)

	// 将从一级缓存淘汰的项晋升到二级缓存
	if ev.key != "" && ev.val != nil && ev.expireAt > 0 {
		ev2 := s.caches[idx][1].put(ev.key, ev.val, ev.expireAt)
		// 如果二级缓存也淘汰了项，触发回调
		if ev2.key != "" && s.onEvicted != nil {
			s.onEvicted(ev2.key, ev2.val)
		}
	}

	return nil
}

// Delete 实现Store接口
func (s *lru2Store) Delete(key string) bool {
	idx := hashBKRD(key) & s.mask
	s.locks[idx].Lock()
	defer s.locks[idx].Unlock()

	return s.delete(key, idx)
}

// Clear 实现Store接口
func (s *lru2Store) Clear() {
	keySet := make(map[string]struct{})

	for i := range s.caches {
		s.locks[i].Lock()

		s.caches[i][0].walk(func(key string, value Value, expireAt int64) bool {
			keySet[key] = struct{}{}
			return true
		})
		s.caches[i][1].walk(func(key string, value Value, expireAt int64) bool {
			keySet[key] = struct{}{}
			return true
		})

		s.locks[i].Unlock()
	}

	for key := range keySet {
		s.Delete(key)
	}
}

// Len 实现Store接口
func (s *lru2Store) Len() int {
	count := 0

	for i := range s.caches {
		s.locks[i].Lock()

		s.caches[i][0].walk(func(key string, value Value, expireAt int64) bool {
			count++
			return true
		})
		s.caches[i][1].walk(func(key string, value Value, expireAt int64) bool {
			count++
			return true
		})

		s.locks[i].Unlock()
	}

	return count
}

// Close 关闭缓存相关资源
func (s *lru2Store) Close() {
	if s.cleanupTick != nil {
		s.cleanupTick.Stop()
	}
}

// 内部时钟，减少 time.Now() 调用造成的 GC 压力
var clock, p, n = time.Now().UnixNano(), uint16(0), uint16(1)

// 返回 clock 变量的当前值。atomic.LoadInt64 是原子操作，用于保证在多线程/协程环境中安全地读取 clock 变量的值
func Now() int64 { return atomic.LoadInt64(&clock) }

func init() {
	go func() {
		for {
			atomic.StoreInt64(&clock, time.Now().UnixNano()) // 每秒校准一次
			for i := 0; i < 9; i++ {
				time.Sleep(100 * time.Millisecond)
				atomic.AddInt64(&clock, int64(100*time.Millisecond)) // 保持 clock 在一个精确的时间范围内，同时避免频繁的系统调用
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

// 实现了 BKDR 哈希算法，用于计算键的哈希值
func hashBKRD(s string) (hash int32) {
	for i := 0; i < len(s); i++ {
		hash = hash*131 + int32(s[i])
	}

	return hash
}

// maskOfNextPowOf2 计算大于或等于输入值的最近 2 的幂次方减一作为掩码值
func maskOfNextPowOf2(cap uint16) uint16 {
	if cap > 0 && cap&(cap-1) == 0 {
		return cap - 1
	}

	// 通过多次右移和按位或操作，将二进制中最高的 1 位右边的所有位都填充为 1
	cap |= cap >> 1
	cap |= cap >> 2
	cap |= cap >> 4

	return cap | (cap >> 8)
}

type node struct {
	k        string
	v        Value
	expireAt int64 // 过期时间戳，noExpiration 表示永不过期
	deleted  bool  // 是否已删除（语义清晰，不复用 expireAt）
}

// 内部缓存核心实现，包含双向链表和节点存储
type cache struct {
	// dlnk[0]是哨兵节点，记录链表头尾，dlnk[0][p]存储尾部索引，dlnk[0][n]存储头部索引
	dlnk [][2]uint16       // 双向链表，0 表示前驱，1 表示后继
	m    []node            // 预分配内存存储节点
	hmap map[string]uint16 // 键到节点索引的映射
	last uint16            // 最后一个节点元素的索引
}

func Create(cap uint16) *cache {
	return &cache{
		dlnk: make([][2]uint16, cap+1),
		m:    make([]node, cap),
		hmap: make(map[string]uint16, cap),
		last: 0,
	}
}

// evicted 表示被淘汰的缓存项
type evicted struct {
	key      string
	val      Value
	expireAt int64
}

// put 向缓存中添加项。
// 返回被淘汰的项信息（如果有），由调用者决定如何处理淘汰项。
func (c *cache) put(key string, val Value, expireAt int64) (ev evicted) {
	if idx, ok := c.hmap[key]; ok {
		// 更新已存在的项，重置 deleted 标记
		c.m[idx-1].v = val
		c.m[idx-1].expireAt = expireAt
		c.m[idx-1].deleted = false
		c.adjust(idx, p, n) // 刷新到链表头部
		return evicted{}
	}

	if c.last == uint16(cap(c.m)) {
		tail := &c.m[c.dlnk[0][p]-1]
		// 记录被淘汰的项（未删除的有效项才会被淘汰后晋升）
		if !(*tail).deleted {
			ev = evicted{key: (*tail).k, val: (*tail).v, expireAt: (*tail).expireAt}
		}

		delete(c.hmap, (*tail).k)
		(*tail).k = key
		(*tail).v = val
		(*tail).expireAt = expireAt
		(*tail).deleted = false
		c.hmap[key] = c.dlnk[0][p]
		c.adjust(c.dlnk[0][p], p, n)

		return ev
	}

	c.last++
	if len(c.hmap) <= 0 {
		c.dlnk[0][p] = c.last
	} else {
		c.dlnk[c.dlnk[0][n]][p] = c.last
	}

	// 初始化新节点并更新链表指针
	c.m[c.last-1].k = key
	c.m[c.last-1].v = val
	c.m[c.last-1].expireAt = expireAt
	c.m[c.last-1].deleted = false
	c.dlnk[c.last] = [2]uint16{0, c.dlnk[0][n]}
	c.hmap[key] = c.last
	c.dlnk[0][n] = c.last

	return evicted{}
}

// 从缓存中获取键对应的节点和状态
func (c *cache) get(key string) (*node, int) {
	if idx, ok := c.hmap[key]; ok {
		c.adjust(idx, p, n)
		return &c.m[idx-1], 1
	}
	return nil, 0
}

// 从缓存中删除键对应的项
func (c *cache) del(key string) (*node, int, int64) {
	if idx, ok := c.hmap[key]; ok && !c.m[idx-1].deleted {
		e := c.m[idx-1].expireAt
		c.m[idx-1].deleted = true // 标记为已删除
		c.adjust(idx, n, p)       // 移动到链表尾部
		return &c.m[idx-1], 1, e
	}

	return nil, 0, 0
}

// 遍历缓存中的所有有效项
func (c *cache) walk(walker func(key string, value Value, expireAt int64) bool) {
	for idx := c.dlnk[0][n]; idx != 0; idx = c.dlnk[idx][n] {
		if !c.m[idx-1].deleted && !walker(c.m[idx-1].k, c.m[idx-1].v, c.m[idx-1].expireAt) {
			return
		}
	}
}

// 调整节点在链表中的位置
// 当 f=0, t=1 时，移动到链表头部；否则移动到链表尾部
func (c *cache) adjust(idx, f, t uint16) {
	if c.dlnk[idx][f] != 0 {
		c.dlnk[c.dlnk[idx][t]][f] = c.dlnk[idx][f]
		c.dlnk[c.dlnk[idx][f]][t] = c.dlnk[idx][t]
		c.dlnk[idx][f] = 0
		c.dlnk[idx][t] = c.dlnk[0][t]
		c.dlnk[c.dlnk[0][t]][f] = idx
		c.dlnk[0][t] = idx
	}
}

func (s *lru2Store) _get(key string, idx, level int32) (*node, int) {
	if n, st := s.caches[idx][level].get(key); st > 0 && n != nil {
		// 已删除的项直接返回未命中
		if n.deleted {
			return nil, 0
		}
		// 永不过期的项直接返回
		if n.expireAt == noExpiration {
			return n, st
		}
		// 检查是否过期
		if n.expireAt <= 0 || Now() >= n.expireAt {
			return nil, 0
		}
		return n, st
	}

	return nil, 0
}

func (s *lru2Store) delete(key string, idx int32) bool {
	n1, s1, _ := s.caches[idx][0].del(key)
	n2, s2, _ := s.caches[idx][1].del(key)
	deleted := s1 > 0 || s2 > 0

	if deleted && s.onEvicted != nil {
		if n1 != nil && n1.v != nil {
			s.onEvicted(key, n1.v)
		} else if n2 != nil && n2.v != nil {
			s.onEvicted(key, n2.v)
		}
	}

	return deleted
}

func (s *lru2Store) cleanupLoop() {
	for range s.cleanupTick.C {
		currentTime := Now()

		for i := range s.caches {
			s.locks[i].Lock()

			// 检查并清理过期项目
			var expiredKeys []string

			s.caches[i][0].walk(func(key string, value Value, expireAt int64) bool {
				if expireAt > 0 && currentTime >= expireAt {
					expiredKeys = append(expiredKeys, key)
				}
				return true
			})

			s.caches[i][1].walk(func(key string, value Value, expireAt int64) bool {
				if expireAt > 0 && currentTime >= expireAt {
					for _, k := range expiredKeys {
						if key == k {
							// 避免重复
							return true
						}
					}
					expiredKeys = append(expiredKeys, key)
				}
				return true
			})

			for _, key := range expiredKeys {
				s.delete(key, int32(i))
			}

			s.locks[i].Unlock()
		}
	}
}
