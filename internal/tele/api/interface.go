package tele_api

import (
	"context"
	"time"

	vender_api "github.com/temoto/vender/head/tele/api"
	"github.com/temoto/vender/log2"
	tele_config "github.com/temoto/venderctl/internal/tele/config"
)

type Teler interface {
	Init(context.Context, *log2.Log, tele_config.Config) error
	Close() error
	Addrs() []string
	Chan() <-chan Packet
	CommandTx(vmid int32, c *vender_api.Command, timeout time.Duration) (*vender_api.Response, error)
}

type stub struct{}

func NewStub() Teler { return stub{} }

func (stub) Init(context.Context, *log2.Log, tele_config.Config) error { return nil }

func (stub) Close() error { return nil }

func (stub) Addrs() []string { return nil }

func (stub) Chan() <-chan Packet { return nil }

func (stub) CommandTx(vmid int32, c *vender_api.Command, timeout time.Duration) (*vender_api.Response, error) {
	return nil, nil
}
