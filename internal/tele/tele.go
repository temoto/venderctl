// Server side of vender tele.
// Goals:
// - receive telemetry
// - receive state
// - send command
// - while hiding transport protocol (MQTT)
package tele

import (
	"context"

	"github.com/juju/errors"
	"github.com/temoto/vender/log2"
	tele_config "github.com/temoto/venderctl/internal/tele/config"
)

type Tele struct {
	log       *log2.Log
	transport transporter
}

func (self *Tele) Init(ctx context.Context, log *log2.Log, teleConfig tele_config.Config) error {
	self.log = log.Clone(log2.LDebug)
	// test code sets .transport
	if self.transport == nil { // production path
		self.transport = &transportMqtt{}
	}
	if err := self.transport.Init(ctx, log, teleConfig); err != nil {
		return errors.Annotate(err, "tele.Init")
	}
	return nil
}

func (self *Tele) Close() error { return self.transport.Close() }

func (self *Tele) Chan() <-chan Packet { return self.transport.RecvChan() }

// func (self *Tele) SendCommand(vmid int32, c *tele.Command) error {
// 	payload, err := proto.Marshal(c)
// 	if err != nil {
// 		return errors.Trace(err)
// 	}
// 	p := Packet{Kind: PacketCommand, VmId: vmid, Payload: payload}
// 	return self.transport.Send(p)
// }
