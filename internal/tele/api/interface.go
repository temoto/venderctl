package tele_api

import (
	"context"

	"github.com/AlexTransit/vender/log2"
	vender_api "github.com/AlexTransit/vender/tele"
	tele_config "github.com/temoto/venderctl/internal/tele/config"
)

type Teler interface {
	Init(context.Context, *log2.Log, tele_config.Config) error
	Close() error
	Addrs() []string
	Chan() <-chan Packet
	CommandTx(vmid int32, c *vender_api.Command) (*vender_api.Response, error)
	SendCommand(vmid int32, c *vender_api.Command) error
}

type stub struct{}

func NewStub() Teler { return stub{} }

func (stub) Init(context.Context, *log2.Log, tele_config.Config) error { return nil }

func (stub) Close() error { return nil }

func (stub) Addrs() []string { return nil }

func (stub) Chan() <-chan Packet { return nil }

func (stub) CommandTx(vmid int32, c *vender_api.Command) (*vender_api.Response, error) {
	return nil, nil
}
func (stub) SendCommand(vmid int32, c *vender_api.Command) error { return nil }
