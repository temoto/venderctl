package state_test

import (
	"context"
	"strings"
	"testing"

	"github.com/AlexTransit/vender/log2"
	"github.com/juju/errors"
	"github.com/stretchr/testify/assert"
	"github.com/temoto/venderctl/internal/state"
	state_new "github.com/temoto/venderctl/internal/state/new"
	tele_api "github.com/temoto/venderctl/internal/tele/api"
	tele_config "github.com/temoto/venderctl/internal/tele/config"
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
				g := state.GetGlobal(ctx)
				assert.Equal(t, 7, g.Config.Money.Scale)
			}, ""},

		{"include-overwrites", `
money { scale = 1 }
include "money-scale-7" {}`,
			func(t testing.TB, ctx context.Context) {
				g := state.GetGlobal(ctx)
				assert.Equal(t, 7, g.Config.Money.Scale)
			}, ""},

		{"tele", `
tele {
	listen "tls://127.0.0.1:1884" {
		allow_roles = ["_all"]
		tls { ca_file = "/ca.pem" }
	}
}`,
			func(t testing.TB, ctx context.Context) {
				g := state.GetGlobal(ctx)
				expect := []tele_config.Listen{
					{URL: "tls://127.0.0.1:1884", AllowRoles: []string{"_all"}, TLS: tele_config.TLS{CaFile: "/ca.pem"}},
				}
				assert.Equal(t, expect, g.Config.Tele.Listens)
			}, ""},

		{"error-syntax", `hello`, nil, "key 'hello' expected start of object"},
		{"error-include-loop", `include "include-loop" {}`, nil, "config include loop: from=include-loop include=include-loop"},
	}
	mkCheck := func(c Case) func(*testing.T) {
		return func(t *testing.T) {
			// log := log2.NewStderr(log2.LDebug) // helps with panics
			log := log2.NewTest(t, log2.LDebug)
			ctx, g := state_new.NewContext("test", log, tele_api.NewStub())
			fs := state.NewMockFullReader(map[string]string{
				"test-inline":   c.input,
				"empty":         "",
				"money-scale-7": "money{scale=7}",
				"error-syntax":  "hello",
				"include-loop":  `include "include-loop" {}`,
			})
			cfg, err := state.ReadConfig(log, fs, "test-inline")
			if err == nil {
				g.Config = cfg
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
	state.MustReadConfig(log, state.NewOsFullReader(), "../../venderctl.hcl")
}
