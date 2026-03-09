package selector

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-gost/core/chain"
	ctxvalue "github.com/go-gost/x/ctx"
)

func TestLatencyStrategyPrefersLowestRTT(t *testing.T) {
	t.Helper()

	var mu sync.Mutex
	calls := map[string]int{}
	probe := func(ctx context.Context, node *chain.Node) (time.Duration, error) {
		mu.Lock()
		calls[node.Addr]++
		mu.Unlock()

		switch node.Addr {
		case "slow:443":
			return 90 * time.Millisecond, nil
		case "fast:443":
			return 15 * time.Millisecond, nil
		default:
			return 0, errors.New("unexpected address")
		}
	}

	strategy := newLatencyStrategy(probe)
	nodes := []*chain.Node{
		chain.NewNode("slow", "slow:443"),
		chain.NewNode("fast", "fast:443"),
	}

	selected := strategy.Apply(context.Background(), nodes...)
	if selected == nil || selected.Addr != "fast:443" {
		t.Fatalf("expected fastest node fast:443, got %+v", selected)
	}

	selected = strategy.Apply(context.Background(), nodes...)
	if selected == nil || selected.Addr != "fast:443" {
		t.Fatalf("expected cached fastest node fast:443, got %+v", selected)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls["fast:443"] != 1 || calls["slow:443"] != 1 {
		t.Fatalf("expected exactly one probe per node, got %+v", calls)
	}
}

func TestLatencyStrategyRefreshesStaleEntriesAsync(t *testing.T) {
	t.Helper()

	var mu sync.Mutex
	calls := map[string]int{}

	probe := func(ctx context.Context, node *chain.Node) (time.Duration, error) {
		mu.Lock()
		calls[node.Addr]++
		call := calls[node.Addr]
		mu.Unlock()

		switch node.Addr {
		case "fast:443":
			if call == 1 {
				return 10 * time.Millisecond, nil
			}
			return 80 * time.Millisecond, nil
		case "slow:443":
			if call == 1 {
				return 40 * time.Millisecond, nil
			}
			return 5 * time.Millisecond, nil
		default:
			return 0, errors.New("unexpected address")
		}
	}

	strategy := newLatencyStrategy(probe).(*latencyStrategy)
	nodes := []*chain.Node{
		chain.NewNode("fast", "fast:443"),
		chain.NewNode("slow", "slow:443"),
	}

	selected := strategy.Apply(context.Background(), nodes...)
	if selected == nil || selected.Addr != "fast:443" {
		t.Fatalf("expected initial fastest node fast:443, got %+v", selected)
	}

	strategy.mu.Lock()
	for cacheKey, entry := range strategy.cache {
		entry.expiresAt = time.Now().Add(-time.Second)
		strategy.cache[cacheKey] = entry
	}
	strategy.mu.Unlock()

	selected = strategy.Apply(context.Background(), nodes...)
	if selected == nil || selected.Addr != "fast:443" {
		t.Fatalf("expected stale cache to keep routing through fast:443 before refresh, got %+v", selected)
	}

	// Wait for the async refresh to propagate to the cache (up to 1 second).
	// After the refresh slow:443 becomes faster (5 ms) than fast:443 (80 ms).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		selected = strategy.Apply(context.Background(), nodes...)
		if selected != nil && selected.Addr == "slow:443" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if selected == nil || selected.Addr != "slow:443" {
		t.Fatalf("expected refreshed cache to switch to slow:443, got %+v", selected)
	}
}

func TestLatencyStrategyFallsBackWhenAllProbesFail(t *testing.T) {
	t.Helper()

	probe := func(ctx context.Context, node *chain.Node) (time.Duration, error) {
		return 0, errors.New("dial failed")
	}

	strategy := newLatencyStrategy(probe)
	nodes := []*chain.Node{
		chain.NewNode("first", "first:443"),
		chain.NewNode("second", "second:443"),
	}

	selected := strategy.Apply(context.Background(), nodes...)
	if selected == nil {
		t.Fatal("expected fallback selection, got nil")
	}
	if selected.Addr != "first:443" {
		t.Fatalf("expected round-robin fallback to pick first node on initial call, got %q", selected.Addr)
	}
}

func TestLatencyStrategySkipsProbeForUDP(t *testing.T) {
	t.Helper()

	called := false
	probe := func(ctx context.Context, node *chain.Node) (time.Duration, error) {
		called = true
		return 10 * time.Millisecond, nil
	}

	strategy := newLatencyStrategy(probe)
	nodes := []*chain.Node{
		chain.NewNode("first", "first:443"),
		chain.NewNode("second", "second:443"),
	}

	ctx := ctxvalue.ContextWithTargetPath(context.Background(), &ctxvalue.TargetPath{
		Network: "udp",
		Address: "8.8.8.8:53",
	})
	selected := strategy.Apply(ctx, nodes...)
	if selected == nil || selected.Addr != "first:443" {
		t.Fatalf("expected UDP flow to fall back to first node, got %+v", selected)
	}
	if called {
		t.Fatal("expected UDP flow to bypass latency probe")
	}
}

func TestLatencyStrategyPrunesExpiredTargets(t *testing.T) {
	t.Helper()

	strategy := newLatencyStrategy(func(ctx context.Context, node *chain.Node) (time.Duration, error) {
		return 10 * time.Millisecond, nil
	}).(*latencyStrategy)

	node := chain.NewNode("node", "node:443")
	currentCtx := ctxvalue.ContextWithTargetPath(context.Background(), &ctxvalue.TargetPath{
		Network: "tcp",
		Address: "current.example:443",
	})
	oldCtx := ctxvalue.ContextWithTargetPath(context.Background(), &ctxvalue.TargetPath{
		Network: "tcp",
		Address: "old.example:443",
	})

	currentKey := latencyCacheKey(currentCtx, node)
	oldKey := latencyCacheKey(oldCtx, node)

	strategy.mu.Lock()
	strategy.cache[currentKey] = latencyProbeEntry{
		rtt:       10 * time.Millisecond,
		success:   true,
		expiresAt: time.Now().Add(-time.Second),
	}
	strategy.cache[oldKey] = latencyProbeEntry{
		rtt:       20 * time.Millisecond,
		success:   true,
		expiresAt: time.Now().Add(-time.Second),
	}
	strategy.mu.Unlock()

	targets := strategy.markForProbe(currentCtx, []*chain.Node{node})
	if len(targets) != 1 || targets[0].cacheKey != currentKey {
		t.Fatalf("expected current target to be scheduled for probing, got %+v", targets)
	}

	strategy.mu.Lock()
	defer strategy.mu.Unlock()

	if _, ok := strategy.cache[currentKey]; !ok {
		t.Fatal("expected current expired target to stay cached for stale fallback")
	}
	if _, ok := strategy.cache[oldKey]; ok {
		t.Fatal("expected unrelated expired target cache entry to be pruned")
	}
}

