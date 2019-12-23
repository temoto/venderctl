package tele_api

import (
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/juju/errors"
	vender_api "github.com/temoto/vender/head/tele/api"
)

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

func (p *Packet) CommandResponse() (*vender_api.Response, error) {
	r := vender_api.Response{}
	err := proto.Unmarshal(p.Payload, &r)
	if err != nil {
		err = errors.Annotatef(err, "raw=%x", p.Payload)
		return nil, err
	}
	return &r, err
}

func (p *Packet) State() (vender_api.State, error) {
	if len(p.Payload) == 1 {
		s := vender_api.State(p.Payload[0])
		if _, ok := vender_api.State_name[int32(s)]; ok {
			return s, nil
		}
	}
	return vender_api.State_Invalid, errors.Errorf("tele invalid state payload='%x'", p.Payload)
}

func (p *Packet) Telemetry() (*vender_api.Telemetry, error) {
	t := vender_api.Telemetry{}
	err := proto.Unmarshal(p.Payload, &t)
	if err != nil {
		err = errors.Annotatef(err, "raw=%x", p.Payload)
		return nil, err
	}
	return &t, err
}
