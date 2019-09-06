package state

import (
	"context"
	"strings"
	"testing"

	"github.com/juju/errors"
	"github.com/stretchr/testify/assert"
	"github.com/temoto/vender/log2"
)

func TestReadConfig(t *testing.T) {
	t.Parallel()

	type Case struct {
		name      string
		input     string
		check     func(testing.TB, context.Context)
		expectErr string
	}
	cases := []Case{
		{"include-normalize", `
money { scale = 1 }
include "./empty" {}`,
			nil, ""},

		{"include-optional", `
include "money-scale-7" {}
include "non-exist" { optional = true }`,
			func(t testing.TB, ctx context.Context) {
				g := GetGlobal(ctx)
				assert.Equal(t, 7, g.Config.Money.Scale)
			}, ""},

		{"include-overwrites", `
money { scale = 1 }
include "money-scale-7" {}`,
			func(t testing.TB, ctx context.Context) {
				g := GetGlobal(ctx)
				assert.Equal(t, 7, g.Config.Money.Scale)
			}, ""},

		// {"tele", `tele{enable=true}`,
		// 	func(t testing.TB, ctx context.Context) {
		// 		g := GetGlobal(ctx)
		// 		assert.NoError(t, g.Tele.Close())
		// 	}, ""},

		{"error-syntax", `hello`, nil, "key 'hello' expected start of object"},
		{"error-include-loop", `include "include-loop" {}`, nil, "config include loop: from=include-loop include=include-loop"},
	}
	mkCheck := func(c Case) func(*testing.T) {
		return func(t *testing.T) {
			// log := log2.NewStderr(log2.LDebug) // helps with panics
			log := log2.NewTest(t, log2.LDebug)
			ctx, g := NewContext("test", log)
			fs := NewMockFullReader(map[string]string{
				"test-inline":   c.input,
				"empty":         "",
				"money-scale-7": "money{scale=7}",
				"error-syntax":  "hello",
				"include-loop":  `include "include-loop" {}`,
			})
			cfg, err := ReadConfig(log, fs, "test-inline")
			if err == nil {
				err = g.Init(ctx, cfg)
			}
			if c.expectErr == "" {
				if err != nil {
					t.Fatalf("error expected=nil actual='%v'", errors.ErrorStack(err))
				}
				if c.check != nil {
					c.check(t, ctx)
				}
			} else {
				if !strings.Contains(err.Error(), c.expectErr) {
					t.Fatalf("error expected='%s' actual='%v'", c.expectErr, err)
				}
			}
		}
	}
	for _, c := range cases {
		t.Run(c.name, mkCheck(c))
	}
}

func TestFunctionalBundled(t *testing.T) {
	// not Parallel
	t.Logf("this test needs OS open|read|stat access to file `../../venderctl.hcl`")

	log := log2.NewTest(t, log2.LDebug)
	MustReadConfig(log, NewOsFullReader(), "../../venderctl.hcl")
}
