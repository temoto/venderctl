package state

import (
	"context"
	"log"
	"os"
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
	config := MustReadConfig(log, fs, "test-inline")
	if config.DB.URL == "" {
		config.DB.URL = os.Getenv("venderctl_db_url")
	}
	g.MustInit(ctx, config)

	return ctx, g
}

// TODO func (*log2.Log) Stdlib() *log.Logger
func Log2stdlib(l2 *log2.Log) *log.Logger {
	return log.New(log2stdWrap{l2}, "", 0)
}

type log2stdWrap struct{ *log2.Log }

func (l log2stdWrap) Write(b []byte) (int, error) {
	l.Printf(string(b))
	return len(b), nil
}
