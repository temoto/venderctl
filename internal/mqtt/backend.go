package mqtt

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/256dpi/gomqtt/client/future"
	"github.com/256dpi/gomqtt/packet"
	"github.com/256dpi/gomqtt/transport"
	"github.com/juju/errors"
	"github.com/temoto/alive/v2"
	"github.com/temoto/venderctl/internal/toolpool"
)

type BackendOptions struct {
	CtxData interface{}
	URL     string
	TLS     *tls.Config

	AckTimeout     time.Duration
	NetworkTimeout time.Duration // conn receive timeout
}

// Server side connection state
// protocol aware transport.Conn wrapper
type backend struct {
	alive    *alive.Alive
	acks     *future.Store
	conn     transport.Conn
	connmu   sync.RWMutex
	ctx      context.Context
	err      toolpool.AtomicError
	id       string
	opt      *BackendOptions
	parent   *Server
	username string
	will     *packet.Message
	willmu   sync.Mutex
}

func newBackend(ctx context.Context, conn transport.Conn, opt *BackendOptions, s *Server, pktConnect *packet.Connect) *backend {
	b := &backend{
		alive:    alive.NewAlive(),
		conn:     conn,
		ctx:      ctx,
		id:       pktConnect.ClientID,
		opt:      opt,
		parent:   s,
		username: pktConnect.Username,
		will:     pktConnect.Will,
	}
	if opt.AckTimeout == 0 {
		opt.AckTimeout = 2 * opt.NetworkTimeout
	}
	b.acks = future.NewStore()
	return b
}

func (b *backend) Close() error {
	b.alive.Stop()
	b.alive.Wait()
	err, _ := b.err.Load()
	return err
}

func (b *backend) Options() BackendOptions { return *b.opt }

func (b *backend) ExpectAck(ctx context.Context, id packet.ID) *future.Future {
	f, ok := b.ackNew(id)
	if !ok {
		return f
	}

	// TODO acks lock
	if ex := b.acks.Get(id); ex != nil {
		ex.Cancel()
		err := errors.Errorf("CRITICAL ExpectAck overwriting id=%d", id)
		b.parent.log.Error(err)
		go func() {
			// _ = b.Close()
			ackCancel(f, err)
		}()
		return f
	}
	b.acks.Put(id, f)
	return f
}

// High level publish flow with QOS.
// TODO qos1 ack-timeout retry
func (b *backend) Publish(ctx context.Context, id packet.ID, msg *packet.Message) error {
	if !b.alive.Add(1) {
		return ErrClosing
	}
	defer b.alive.Done()

	pub := packet.NewPublish()
	pub.ID = id
	pub.Message = *msg
	switch msg.QOS {
	case packet.QOSAtMostOnce:
		pub.ID = 0
		return b.Send(pub)

	case packet.QOSAtLeastOnce:
		if pub.ID == 0 {
			return errors.Errorf("backend.doPublish QOSAtLeastOnce requires non-zero packet.ID message=%s", pub.Message.String())
		}
		f := b.ExpectAck(ctx, pub.ID)
		// TODO retry
		if err := b.Send(pub); err != nil {
			ackCancel(f, err)
		}
		if err := ackWait(f); err != nil {
			err = errors.Annotatef(err, "expect puback id=%d", pub.ID)
			return b.die(err)
		}
		return nil

	default:
		panic("code error QOS > 1 is not supported")
	}
}

func (b *backend) Receive() (packet.Generic, error) {
	conn := b.getConn()
	if conn == nil {
		return nil, ErrClosing
	}
	pkt, err := conn.Receive()
	b.parent.log.Debugf("mqtt recv addr=%s id=%s pkt=%v err=%v", addrString(conn.RemoteAddr()), b.id, pkt, err)
	switch err {
	case nil:
		return pkt, nil

	case io.EOF: // remote properly closed connection
		_ = b.die(err)
		return nil, err

	default:
		if !b.alive.IsRunning() && isClosedConn(err) {
			// conn.Close was used to interrupt blocking Send/Receive
			return nil, ErrClosing
		}
		_ = b.die(err)
		return nil, err
	}
}

func (b *backend) Send(pkt packet.Generic) error {
	conn := b.getConn()
	if conn == nil {
		return ErrClosing
	}
	b.parent.log.Debugf("mqtt send id=%s pkt=%s", b.id, pkt.String())
	if err := b.conn.Send(pkt, false); err != nil {
		if !b.alive.IsRunning() && isClosedConn(err) {
			// conn.Close was used to interrupt blocking Send/Receive
			return ErrClosing
		}
		err = errors.Annotatef(err, "clientid=%s", b.id)
		return b.die(err)
	}
	return nil
}

// success counterpart to ExpectAck
func (b *backend) FulfillAck(id packet.ID) error {
	f := b.acks.Get(id)
	if f == nil {
		return fmt.Errorf("not expected ack for id=%d", id)
	}
	f.Complete()
	return nil
}

func (b *backend) RemoteAddr() net.Addr {
	if conn := b.getConn(); conn != nil {
		return conn.RemoteAddr()
	}
	return nil
}

func (b *backend) ackNew(id packet.ID) (*future.Future, bool) {
	f := future.New()
	f.Data.Store("err", error(nil))
	f.Data.Store("id", id)
	f.Data.Store("timeout", b.opt.AckTimeout)
	if !b.alive.Add(1) {
		ackCancel(f, ErrClosing)
		return f, false
	}
	go b.ackWorker(f)
	return f, true
}

// TODO lock
func (b *backend) ackWorker(f *future.Future) {
	defer b.alive.Done()
	x, _ := f.Data.Load("id")
	id := x.(packet.ID)
	if err := ackWait(f); err == future.ErrTimeout {
		ackCancel(f, future.ErrTimeout)
	}
	b.acks.Delete(id)
}

func (b *backend) die(e error) error {
	err, found := b.err.StoreOnce(e)
	if found {
		return err
	}
	b.parent.log.Debugf("mqtt die id=%s e=%v", b.id, e)
	b.alive.Stop()
	toolpool.WithLock(&b.connmu, func() {
		if b.conn != nil {
			b.parent.log.Debugf("mqtt die +close id=%s addr=%s", b.id, addrString(b.conn.RemoteAddr()))
			_ = b.conn.Close()
			b.conn = nil
		}
	})
	return err
}

func (b *backend) getConn() transport.Conn {
	b.connmu.RLock()
	c := b.conn
	b.connmu.RUnlock()
	return c
}

func isClosedConn(e error) bool {
	return e != nil && strings.HasSuffix(e.Error(), "use of closed network connection")
}

func ackCancel(f *future.Future, e error) {
	f.Data.Store("err", e)
	f.Cancel()
}

func ackWait(f *future.Future) error {
	x, _ := f.Data.Load("timeout")
	timeout := x.(time.Duration)
	return f.Wait(timeout)
}
