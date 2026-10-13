package headerfwd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const sentinel = "SENTINEL-s3cr3t-value"

func TestSnapshotNeverPrintsValue(t *testing.T) {
	s := newSnapshot(map[string]string{"X-User-Id": sentinel})
	outs := []string{
		fmt.Sprintf("%v", s), fmt.Sprintf("%+v", s), fmt.Sprintf("%#v", s), fmt.Sprintf("%s", s),
		fmt.Sprintf("%v", &s), fmt.Sprintf("%v", struct{ S Snapshot }{s}), fmt.Sprint(s), fmt.Sprintf("%q", s),
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	outs = append(outs, string(b))
	b, _ = json.Marshal(map[string]any{"k": s})
	outs = append(outs, string(b))

	var sb bytes.Buffer
	slog.New(slog.NewTextHandler(&sb, nil)).Info("x", "snap", s)
	outs = append(outs, sb.String())

	core, logs := observer.New(zapcore.DebugLevel)
	l := zap.New(core)
	l.Info("x", zap.Any("a", s), zap.Object("o", s), zap.Reflect("r", s))
	for _, e := range logs.All() {
		outs = append(outs, fmt.Sprint(e.Context))
	}
	var jb bytes.Buffer
	jl := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&jb), zapcore.DebugLevel))
	jl.Info("x", zap.Any("a", s), zap.Object("o", s), zap.Reflect("r", s))
	outs = append(outs, jb.String())

	for i, o := range outs {
		if strings.Contains(o, sentinel) {
			t.Errorf("output %d leaked value: %s", i, o)
		}
	}
	if got := s.String(); got != "forwarded_headers{names=[X-User-Id] n=1}" {
		t.Errorf("String = %q", got)
	}
}

func TestContextKeysAreSeparate(t *testing.T) {
	s := newSnapshot(map[string]string{"X-A": "v"})
	ctx := WithSnapshot(context.Background(), s)
	if _, ok := OutboundFrom(ctx); ok {
		t.Fatal("key A leaked into key B")
	}
	if got, ok := SnapshotFrom(ctx); !ok || got.Len() != 1 {
		t.Fatal("key A missing")
	}
	ctx = WithOutbound(context.Background(), s)
	if _, ok := SnapshotFrom(ctx); ok {
		t.Fatal("key B leaked into key A")
	}
}
