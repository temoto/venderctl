// Server side of vender tele.
// Goals:
// - receive telemetry
// - receive state
// - send command
// - while hiding transport protocol (MQTT)
package tele

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/juju/errors"
	tele_api "github.com/temoto/vender/head/tele/api"
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

func (self *Tele) SendCommand(vmid int32, c *tele_api.Command) error {
	if c.Id == 0 {
		c.Id = rand.Uint32()
	}
	if c.ReplyTopic == "" {
		c.ReplyTopic = fmt.Sprintf("cr/%d", c.Id)
	}

	payload, err := proto.Marshal(c)
	if err != nil {
		return errors.Trace(err)
	}
	p := Packet{Kind: PacketCommand, VmId: vmid, Payload: payload}
	err = self.transport.Send(p)
	return errors.Annotate(err, "tele.SendCommand")
}

// Will consume and drop irrelevant packets from pch.
func (self *Tele) CommandTx(vmid int32, c *tele_api.Command, timeout time.Duration) (*tele_api.Response, error) {
	if c.Deadline == 0 {
		c.Deadline = time.Now().Add(timeout).UnixNano()
	}
	if err := self.SendCommand(vmid, c); err != nil {
		return nil, errors.Annotate(err, "CommandTx")
	}

	tmr := time.NewTimer(timeout)
	defer tmr.Stop()
	for {
		select {
		case p := <-self.Chan():
			// if p.Kind == tele.PacketCommandReply {
			if r, err := p.CommandResponse(); err == nil {
				if r.CommandId == c.Id {
					if r.Error == "" {
						return r, nil
					}
					return r, fmt.Errorf(r.Error)
				} else {
					self.log.Errorf("current command.id=%d unexpected response=%#v", c.Id, r)
				}
			} else {
				self.log.Errorf("unexpected packet=%#v", p)
			}

		case <-tmr.C:
			return nil, errors.Timeoutf("response")
		}
	}
}
