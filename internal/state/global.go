package state

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/go-pg/pg/v9"
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

func (g *Global) InitDB(cmdName string) error {
	pingTimeout := helpers.IntMillisecondDefault(g.Config.DB.PingTimeoutMs, 5*time.Second)

	dbOpt, err := pg.ParseURL(g.Config.DB.URL)
	if err != nil {
		cleanUrl, _ := url.Parse(g.Config.DB.URL)
		if cleanUrl.User != nil {
			cleanUrl.User = url.UserPassword("_hidden_", "_hidden_")
		}
		return errors.Annotatef(err, "config db.url=%s", cleanUrl.String())
	}
	dbOpt.MinIdleConns = 1
	dbOpt.IdleTimeout = -1
	dbOpt.IdleCheckFrequency = -1
	dbOpt.ApplicationName = "venderctl/" + cmdName
	// MaxRetries:1,
	// PoolSize:2,

	g.DB = pg.Connect(dbOpt)
	g.DB.AddQueryHook(queryHook{g})
	_, err = g.DB.WithTimeout(pingTimeout).Exec(`select 1`)
	return errors.Annotate(err, "db ping")
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

type queryHook struct{ g *Global }

func (q queryHook) BeforeQuery(ctx context.Context, e *pg.QueryEvent) (context.Context, error) {
	s, err := e.FormattedQuery()
	q.g.Log.Debugf("sql q=%s err=%v", s, err)
	return ctx, nil
}

func (queryHook) AfterQuery(context.Context, *pg.QueryEvent) error { return nil }
