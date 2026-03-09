package selector

import (
	"context"
	"errors"
	"hash/crc32"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gost/core/chain"
	"github.com/go-gost/core/logger"
	"github.com/go-gost/core/metadata"
	"github.com/go-gost/core/selector"
	ctxvalue "github.com/go-gost/x/ctx"
	xnet "github.com/go-gost/x/internal/net"
	mdutil "github.com/go-gost/x/metadata/util"
)

const (
	latencyProbeTimeout = 800 * time.Millisecond
	latencyProbeTTL     = 15 * time.Second
	latencyFailureTTL   = 5 * time.Second
)

type latencyProbeFunc func(context.Context, *chain.Node) (time.Duration, error)

type latencyProbeEntry struct {
	rtt       time.Duration
	expiresAt time.Time
	success   bool
}

type latencyProbeTarget struct {
	cacheKey string
	node     *chain.Node
}

type roundRobinStrategy[T any] struct {
	counter uint64
}

// RoundRobinStrategy is a strategy for node selector.
// The node will be selected by round-robin algorithm.
func RoundRobinStrategy[T any]() selector.Strategy[T] {
	return &roundRobinStrategy[T]{}
}

func (s *roundRobinStrategy[T]) Apply(ctx context.Context, vs ...T) (v T) {
	if len(vs) == 0 {
		return
	}

	n := atomic.AddUint64(&s.counter, 1) - 1
	return vs[int(n%uint64(len(vs)))]
}

type randomStrategy[T any] struct {
	rw *RandomWeighted[T]
	mu sync.Mutex
}

// RandomStrategy is a strategy for node selector.
// The node will be selected randomly.
func RandomStrategy[T any]() selector.Strategy[T] {
	return &randomStrategy[T]{
		rw: NewRandomWeighted[T](),
	}
}

func (s *randomStrategy[T]) Apply(ctx context.Context, vs ...T) (v T) {
	if len(vs) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.rw.Reset()
	for i := range vs {
		weight := 0
		if md, _ := any(vs[i]).(metadata.Metadatable); md != nil {
			weight = mdutil.GetInt(md.Metadata(), labelWeight)
		}
		if weight <= 0 {
			weight = 1
		}
		s.rw.Add(vs[i], weight)
	}

	return s.rw.Next()
}

type fifoStrategy[T any] struct{}

// FIFOStrategy is a strategy for node selector.
// The node will be selected from first to last,
// and will stick to the selected node until it is failed.
func FIFOStrategy[T any]() selector.Strategy[T] {
	return &fifoStrategy[T]{}
}

// Apply applies the fifo strategy for the nodes.
func (s *fifoStrategy[T]) Apply(ctx context.Context, vs ...T) (v T) {
	if len(vs) == 0 {
		return
	}
	return vs[0]
}

type latencyStrategy struct {
	fallback selector.Strategy[*chain.Node]
	probe    latencyProbeFunc

	mu      sync.Mutex
	cache   map[string]latencyProbeEntry
	probing map[string]struct{}
}

// LatencyStrategy selects the node with the lowest recently-observed TCP connect RTT.
// It keeps a short-lived probe cache and falls back to round-robin when no measurement
// is available yet or when all probes fail.
func LatencyStrategy() selector.Strategy[*chain.Node] {
	return newLatencyStrategy(nil)
}

func newLatencyStrategy(probe latencyProbeFunc) selector.Strategy[*chain.Node] {
	if probe == nil {
		probe = defaultLatencyProbe
	}
	return &latencyStrategy{
		fallback: RoundRobinStrategy[*chain.Node](),
		probe:    probe,
		cache:    make(map[string]latencyProbeEntry),
		probing:  make(map[string]struct{}),
	}
}

func (s *latencyStrategy) Apply(ctx context.Context, vs ...*chain.Node) (v *chain.Node) {
	if len(vs) == 0 {
		return nil
	}
	if target := ctxvalue.TargetPathFromContext(ctx); target != nil && !isTCPProbeNetwork(target.Network) {
		return s.fallback.Apply(ctx, vs...)
	}

	now := time.Now()
	if node, needsRefresh := s.pickBest(ctx, vs, now, false); node != nil {
		if needsRefresh {
			s.refreshAsync(ctx, vs)
		}
		return node
	}
	if node, _ := s.pickBest(ctx, vs, now, true); node != nil {
		s.refreshAsync(ctx, vs)
		return node
	}

	s.probeNodes(ctx, vs)
	if node, _ := s.pickBest(ctx, vs, time.Now(), false); node != nil {
		return node
	}
	if node, _ := s.pickBest(ctx, vs, time.Now(), true); node != nil {
		return node
	}
	return s.fallback.Apply(ctx, vs...)
}

func (s *latencyStrategy) pickBest(ctx context.Context, vs []*chain.Node, now time.Time, allowStale bool) (*chain.Node, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		best         *chain.Node
		bestRTT      time.Duration
		hasBest      bool
		needsRefresh bool
	)

	for _, node := range vs {
		if node == nil {
			continue
		}
		cacheKey := latencyCacheKey(ctx, node)
		if cacheKey == "" {
			continue
		}

		entry, ok := s.cache[cacheKey]
		if !ok {
			needsRefresh = true
			continue
		}
		if !entry.success {
			if now.After(entry.expiresAt) {
				needsRefresh = true
			}
			continue
		}

		isFresh := now.Before(entry.expiresAt)
		if !isFresh {
			needsRefresh = true
			if !allowStale {
				continue
			}
		}

		if !hasBest || entry.rtt < bestRTT {
			best = node
			bestRTT = entry.rtt
			hasBest = true
		}
	}

	return best, needsRefresh
}

func (s *latencyStrategy) refreshAsync(ctx context.Context, vs []*chain.Node) {
	toProbe := s.markForProbe(ctx, vs)
	if len(toProbe) == 0 {
		return
	}

	go func() {
		probeCtx := context.Background()
		if target := ctxvalue.TargetPathFromContext(ctx); target != nil {
			probeCtx = ctxvalue.ContextWithTargetPath(probeCtx, target)
		}
		s.probeMarked(probeCtx, toProbe)
	}()
}

func (s *latencyStrategy) markForProbe(ctx context.Context, vs []*chain.Node) []latencyProbeTarget {
	now := time.Now()
	preserve := make(map[string]struct{}, len(vs))
	for _, node := range vs {
		cacheKey := latencyCacheKey(ctx, node)
		if cacheKey == "" {
			continue
		}
		preserve[cacheKey] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupExpiredLocked(now, preserve)

	out := make([]latencyProbeTarget, 0, len(vs))
	for _, node := range vs {
		if node == nil {
			continue
		}
		cacheKey := latencyCacheKey(ctx, node)
		if cacheKey == "" {
			continue
		}

		entry, ok := s.cache[cacheKey]
		if ok && now.Before(entry.expiresAt) {
			continue
		}
		if _, probing := s.probing[cacheKey]; probing {
			continue
		}

		s.probing[cacheKey] = struct{}{}
		out = append(out, latencyProbeTarget{
			cacheKey: cacheKey,
			node:     node,
		})
	}

	return out
}

func (s *latencyStrategy) probeNodes(ctx context.Context, vs []*chain.Node) map[string]latencyProbeEntry {
	targets := s.markForProbe(ctx, vs)
	if len(targets) == 0 {
		return s.snapshot(ctx, vs)
	}
	s.probeMarked(ctx, targets)
	return s.snapshot(ctx, vs)
}

func (s *latencyStrategy) probeMarked(ctx context.Context, targets []latencyProbeTarget) {
	timeout := latencyProbeTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		timeout = latencyProbeTimeout
	}

	var wg sync.WaitGroup

	for _, target := range targets {
		if target.node == nil || target.cacheKey == "" {
			continue
		}

		wg.Add(1)
		go func(target latencyProbeTarget) {
			defer wg.Done()

			probeCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			if tp := ctxvalue.TargetPathFromContext(ctx); tp != nil {
				probeCtx = ctxvalue.ContextWithTargetPath(probeCtx, tp)
			}

			rtt, err := s.probe(probeCtx, target.node)
			now := time.Now()
			entry := latencyProbeEntry{
				rtt:       rtt,
				success:   err == nil,
				expiresAt: now.Add(latencyProbeTTL),
			}
			if err != nil {
				entry.expiresAt = now.Add(latencyFailureTTL)
			}
			s.finishProbe(target.cacheKey, entry)
		}(target)
	}

	wg.Wait()
}

func (s *latencyStrategy) finishProbe(addr string, entry latencyProbeEntry) {
	if addr == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.probing, addr)
	s.cache[addr] = entry
}

func (s *latencyStrategy) snapshot(ctx context.Context, vs []*chain.Node) map[string]latencyProbeEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]latencyProbeEntry, len(vs))
	for _, node := range vs {
		if node == nil {
			continue
		}
		cacheKey := latencyCacheKey(ctx, node)
		if cacheKey == "" {
			continue
		}
		if entry, ok := s.cache[cacheKey]; ok {
			out[cacheKey] = entry
		}
	}
	return out
}

func (s *latencyStrategy) cleanupExpiredLocked(now time.Time, preserve map[string]struct{}) {
	for cacheKey, entry := range s.cache {
		if now.Before(entry.expiresAt) {
			continue
		}
		if preserve != nil {
			if _, ok := preserve[cacheKey]; ok {
				continue
			}
		}
		delete(s.cache, cacheKey)
	}
}

func latencyCacheKey(ctx context.Context, node *chain.Node) string {
	if node == nil {
		return ""
	}
	addr := normalizeProbeAddr(node.Addr)
	if addr == "" {
		return ""
	}
	target := ctxvalue.TargetPathFromContext(ctx)
	if target == nil || !isTCPProbeNetwork(target.Network) || strings.TrimSpace(target.Address) == "" {
		return addr
	}
	return target.Network + "|" + strings.TrimSpace(target.Address) + "|" + addr
}

func normalizeProbeAddr(addr string) string {
	return strings.TrimSpace(addr)
}

func isTCPProbeNetwork(network string) bool {
	network = strings.ToLower(strings.TrimSpace(network))
	return network == "" || strings.HasPrefix(network, "tcp")
}

func defaultLatencyProbe(ctx context.Context, node *chain.Node) (time.Duration, error) {
	if node == nil {
		return 0, errors.New("nil probe node")
	}

	target := ctxvalue.TargetPathFromContext(ctx)
	if target != nil && isTCPProbeNetwork(target.Network) && strings.TrimSpace(target.Address) != "" && node.Options() != nil && node.Options().Transport != nil {
		return probeNodeRouteToTarget(ctx, node, target.Network, strings.TrimSpace(target.Address))
	}

	addr := normalizeProbeAddr(node.Addr)
	if addr == "" {
		return 0, errors.New("empty probe address")
	}

	dialer := &net.Dialer{}
	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return time.Since(start), nil
}

func probeNodeRouteToTarget(ctx context.Context, node *chain.Node, network, address string) (time.Duration, error) {
	addr, err := xnet.Resolve(ctx, "ip", node.Addr, node.Options().Resolver, node.Options().HostMapper, logger.Default())
	if err != nil {
		return 0, err
	}

	start := time.Now()
	cc, err := node.Options().Transport.Dial(ctx, addr)
	if err != nil {
		return 0, err
	}
	defer cc.Close()

	cn, err := node.Options().Transport.Handshake(ctx, cc)
	if err != nil {
		return 0, err
	}
	defer cn.Close()

	targetConn, err := node.Options().Transport.Connect(ctx, cn, network, address)
	if err != nil {
		return 0, err
	}
	_ = targetConn.Close()
	return time.Since(start), nil
}

type hashStrategy[T any] struct {
	r  *rand.Rand
	mu sync.Mutex
}

func HashStrategy[T any]() selector.Strategy[T] {
	return &hashStrategy[T]{
		r: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *hashStrategy[T]) Apply(ctx context.Context, vs ...T) (v T) {
	if len(vs) == 0 {
		return
	}
	if h := ctxvalue.HashFromContext(ctx); h != nil {
		value := uint64(crc32.ChecksumIEEE([]byte(h.Source)))
		logger.Default().Tracef("hash %s %d", h.Source, value)
		return vs[value%uint64(len(vs))]
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return vs[s.r.Intn(len(vs))]
}
