package tele

import (
	"context"
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/juju/errors"
	tele_api "github.com/temoto/vender/head/tele/api"
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

func (p *Packet) CommandResponse() (*tele_api.Response, error) {
	r := tele_api.Response{}
	err := proto.Unmarshal(p.Payload, &r)
	if err != nil {
		err = errors.Annotatef(err, "raw=%x", p.Payload)
		return nil, err
	}
	return &r, err
}

func (p *Packet) State() (tele_api.State, error) {
	if len(p.Payload) == 1 {
		s := tele_api.State(p.Payload[0])
		if _, ok := tele_api.State_name[int32(s)]; ok {
			return s, nil
		}
	}
	return tele_api.State_Invalid, errors.Errorf("tele invalid state payload='%x'", p.Payload)
}

func (p *Packet) Telemetry() (*tele_api.Telemetry, error) {
	t := tele_api.Telemetry{}
	err := proto.Unmarshal(p.Payload, &t)
	if err != nil {
		err = errors.Annotatef(err, "raw=%x", p.Payload)
		return nil, err
	}
	return &t, err
}
