// Sorry, workaround to import cycles.
package state_new

import (
	"context"
	"os"
	"testing"

	"github.com/temoto/alive/v2"
	"github.com/temoto/vender/log2"
	"github.com/temoto/venderctl/internal/state"
	tele_api "github.com/temoto/venderctl/internal/tele/api"
)

func NewContext(tag string, log *log2.Log, teler tele_api.Teler) (context.Context, *state.Global) {
	if log == nil {
		panic("code error state.NewContext() log=nil")
	}

	g := &state.Global{
		Alive: alive.NewAlive(),
		Log:   log,
		Tele:  teler,
	}
	ctx := context.Background()
	ctx = context.WithValue(ctx, log2.ContextKey, log)
	ctx = context.WithValue(ctx, state.ContextKey, g)

	return ctx, g
}

func NewTestContext(t testing.TB, teler tele_api.Teler, confString string) (context.Context, *state.Global) {
	fs := state.NewMockFullReader(map[string]string{
		"test-inline": confString,
	})

	var log *log2.Log
	if os.Getenv("vender_test_log_stderr") == "1" {
		log = log2.NewStderr(log2.LDebug) // useful with panics
	} else {
		log = log2.NewTest(t, log2.LDebug)
	}
	log.SetFlags(log2.LTestFlags)

	if teler == nil {
		teler = tele_api.NewStub()
	}
	ctx, g := NewContext("test", log, teler)
	g.Config = state.MustReadConfig(log, fs, "test-inline")
	if g.Config.DB.URL == "" {
		g.Config.DB.URL = os.Getenv("venderctl_db_url")
	}

	return ctx, g
}
