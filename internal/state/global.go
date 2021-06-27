package state

import (
	"context"
	"fmt"
	"github.com/go-pg/pg/v9"
	"github.com/juju/errors"
	"github.com/temoto/alive/v2"
	"github.com/temoto/vender/helpers"
	"github.com/temoto/vender/log2"
	vender_api "github.com/temoto/vender/tele"
	tele_api "github.com/temoto/venderctl/internal/tele/api"
	"net/url"
	"os"
	"time"
)

type Global struct {
	Alive        *alive.Alive
	BuildVersion string
	Config       *Config
	DB           *pg.DB
	Log          *log2.Log
	Tele         tele_api.Teler
	Vmc          map[int32]vmcStruct
}

type vmcStruct struct {
	Connect bool
	State   vender_api.State
}

const ContextKey = "run/state-global"

func GetGlobal(ctx context.Context) *Global {
	v := ctx.Value(ContextKey)
	if v == nil {
		panic(fmt.Sprintf("context['%s'] is nil", ContextKey))
	}
	if g, ok := v.(*Global); ok {
		return g
	}
	panic(fmt.Sprintf("context['%s'] expected type *Global actual=%#v", ContextKey, v))
}

func (g *Global) CtlStop(ctx context.Context) {
	g.Tele.Close()
	g.Log.Infof("venderctl stop")
	os.Exit(0)
}

func (g *Global) InitDB(cmdName string) error {
	g.Vmc = make(map[int32]vmcStruct)
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
