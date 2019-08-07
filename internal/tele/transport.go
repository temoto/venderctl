package tele

import (
	"context"
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/juju/errors"
	"github.com/temoto/vender/head/tele"
	"github.com/temoto/vender/log2"
	tele_config "github.com/temoto/venderctl/internal/tele/config"
)

type transporter interface {
	Init(context.Context, *log2.Log, tele_config.Config) error
	Close() error
	RecvChan() <-chan Packet
	Send(Packet) error
}

//go:generate stringer -type=PacketKind -trimprefix=Packet
type PacketKind uint8

const (
	PacketInvalid PacketKind = iota
	PacketState
	PacketTelemetry
	PacketCommand
	PacketCommandReply
)

type Packet struct {
	Payload []byte
	VmId    int32
	Kind    PacketKind
}

func (p *Packet) String() string {
	return fmt.Sprintf("tele.Packet(Kind=%s VmId=%d Payload=%x)", p.Kind.String(), p.VmId, p.Payload)
}

func (p *Packet) State() (tele.State, error) {
	if len(p.Payload) == 1 {
		s := tele.State(p.Payload[0])
		if _, ok := tele.State_name[int32(s)]; ok {
			return s, nil
		}
	}
	return tele.State_Invalid, errors.Errorf("tele invalid state payload='%x'", p.Payload)
}

func (p *Packet) Telemetry() (*tele.Telemetry, error) {
	t := tele.Telemetry{}
	err := proto.Unmarshal(p.Payload, &t)
	if err != nil {
		err = errors.Annotatef(err, "raw=%x", p.Payload)
		return nil, err
	}
	return &t, err
}
