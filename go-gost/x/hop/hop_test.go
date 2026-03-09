package hop

import (
	"context"
	"testing"

	"github.com/go-gost/core/chain"
	corehop "github.com/go-gost/core/hop"
	"github.com/go-gost/core/logger"
	ctxvalue "github.com/go-gost/x/ctx"
)

type noopLogger struct{}

func (noopLogger) WithFields(map[string]any) logger.Logger { return noopLogger{} }
func (noopLogger) Trace(args ...any)                       {}
func (noopLogger) Tracef(format string, args ...any)       {}
func (noopLogger) Debug(args ...any)                       {}
func (noopLogger) Debugf(format string, args ...any)       {}
func (noopLogger) Info(args ...any)                        {}
func (noopLogger) Infof(format string, args ...any)        {}
func (noopLogger) Warn(args ...any)                        {}
func (noopLogger) Warnf(format string, args ...any)        {}
func (noopLogger) Error(args ...any)                       {}
func (noopLogger) Errorf(format string, args ...any)       {}
func (noopLogger) Fatal(args ...any)                       {}
func (noopLogger) Fatalf(format string, args ...any)       {}
func (noopLogger) GetLevel() logger.LogLevel               { return logger.InfoLevel }
func (noopLogger) IsLevelEnabled(level logger.LogLevel) bool {
	return false
}

type captureSelector struct {
	target *ctxvalue.TargetPath
}

func (s *captureSelector) Select(ctx context.Context, vs ...*chain.Node) *chain.Node {
	s.target = ctxvalue.TargetPathFromContext(ctx)
	if len(vs) == 0 {
		return nil
	}
	return vs[0]
}

func TestChainHopSelectPassesTargetPathToSelector(t *testing.T) {
	t.Helper()

	sel := &captureSelector{}
	h := NewHop(
		NodeOption(chain.NewNode("node", "node:443")),
		SelectorOption(sel),
		LoggerOption(noopLogger{}),
	)

	selected := h.Select(
		context.Background(),
		corehop.NetworkSelectOption("udp"),
		corehop.AddrSelectOption("8.8.8.8:53"),
	)
	if selected == nil || selected.Addr != "node:443" {
		t.Fatalf("expected hop to keep routable node available, got %+v", selected)
	}
	if sel.target == nil {
		t.Fatal("expected target path to be passed to selector")
	}
	if sel.target.Network != "udp" || sel.target.Address != "8.8.8.8:53" {
		t.Fatalf("unexpected target path %+v", sel.target)
	}
}
