package selector

import (
	"context"
	"net"
	"testing"

	"github.com/go-gost/core/chain"
	"github.com/go-gost/x/config"
)

func TestParseNodeSelector_LatencyPrefersReachableNode(t *testing.T) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	sel := ParseNodeSelector(&config.SelectorConfig{Strategy: "latency"})
	if sel == nil {
		t.Fatal("expected selector")
	}

	nodes := []*chain.Node{
		chain.NewNode("bad", "127.0.0.1:1"),
		chain.NewNode("good", ln.Addr().String()),
	}
	selected := sel.Select(context.Background(), nodes...)
	if selected == nil {
		t.Fatal("expected reachable node to be selected")
	}
	if selected.Addr != ln.Addr().String() {
		t.Fatalf("expected reachable node %q, got %q", ln.Addr().String(), selected.Addr)
	}

	<-done
}
