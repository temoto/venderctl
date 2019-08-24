package state

import (
	"context"
	"fmt"

	"github.com/go-pg/pg"
	"github.com/juju/errors"
	"github.com/temoto/alive"
	"github.com/temoto/vender/helpers"
	"github.com/temoto/vender/log2"
	"github.com/temoto/venderctl/internal/tele"
)

type Global struct {
	Alive  *alive.Alive
	Config *Config
	DB     *pg.DB
	Log    *log2.Log
	Tele   *tele.Tele
}

const contextKey = "run/state-global"

func NewContext(tag string, log *log2.Log) (context.Context, *Global) {
	if log == nil {
		panic("code error state.NewContext() log=nil")
	}

	g := &Global{
		Alive: alive.NewAlive(),
		Log:   log,
		Tele:  new(tele.Tele),
	}
	ctx := context.Background()
	ctx = context.WithValue(ctx, log2.ContextKey, log)
	ctx = context.WithValue(ctx, contextKey, g)

	g.DB = pg.Connect(&pg.Options{
		User:            "vender_dev", // TODO config
		Password:        "dev",        // TODO config
		Database:        "vender_dev", // TODO config
		ApplicationName: "venderctl/" + tag,
		// MaxRetries:1,
		// PoolSize:2,
		MinIdleConns:       1,
		IdleTimeout:        -1,
		IdleCheckFrequency: -1,
	})

	return ctx, g
}

func GetGlobal(ctx context.Context) *Global {
	v := ctx.Value(contextKey)
	if v == nil {
		panic(fmt.Sprintf("context['%s'] is nil", contextKey))
	}
	if g, ok := v.(*Global); ok {
		return g
	}
	panic(fmt.Sprintf("context['%s'] expected type *Global actual=%#v", contextKey, v))
}

// If `Init` fails, consider `Global` is in broken state.
func (g *Global) Init(ctx context.Context, cfg *Config) error {
	g.Config = cfg

	if g.Config.Persist.Root == "" {
		g.Config.Persist.Root = "./tmp-vender-db"
		g.Log.Errorf("config: persist.root=empty changed=%s", g.Config.Persist.Root)
		// return errors.Errorf("config: persist.root=empty")
	}
	g.Log.Debugf("config: persist.root=%s", g.Config.Persist.Root)

	errs := make([]error, 0)
	if cfg.Tele.Enable {
		errs = append(errs, g.Tele.Init(ctx, g.Log, cfg.Tele))
	}

	return helpers.FoldErrors(errs)
}

func (g *Global) MustInit(ctx context.Context, cfg *Config) {
	err := g.Init(ctx, cfg)
	if err != nil {
		g.Log.Fatal(errors.ErrorStack(err))
	}
}

func (g *Global) Error(err error, args ...interface{}) {
	if err != nil {
		if len(args) != 0 {
			msg := args[0].(string)
			args = args[1:]
			err = errors.Annotatef(err, msg, args...)
		}
		g.Log.Errorf(errors.ErrorStack(err))
	}
}
