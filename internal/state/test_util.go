package state

import (
	"context"
	"testing"

	"github.com/temoto/vender/log2"
)

func NewTestContext(t testing.TB, confString string /* logLevel log2.Level*/) (context.Context, *Global) {
	fs := NewMockFullReader(map[string]string{
		"test-inline": confString,
	})

	log := log2.NewTest(t, log2.LDebug)
	// log := log2.NewStderr(log2.LDebug) // useful with panics
	log.SetFlags(log2.LTestFlags)
	ctx, g := NewContext("test", log)
	g.MustInit(ctx, MustReadConfig(log, fs, "test-inline"))

	return ctx, g
}
