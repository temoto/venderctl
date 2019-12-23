package mqtt

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/256dpi/gomqtt/broker"
	"github.com/256dpi/gomqtt/client/future"
	"github.com/256dpi/gomqtt/packet"
	"github.com/256dpi/gomqtt/topic"
	"github.com/256dpi/gomqtt/transport"
	"github.com/juju/errors"
	"github.com/temoto/alive/v2"
	"github.com/temoto/vender/helpers"
	"github.com/temoto/vender/log2"
	"github.com/temoto/venderctl/internal/toolpool"
)

const defaultReadLimit = 1 << 20

var (
	ErrSameClient    = fmt.Errorf("clientid overtake")
	ErrClosing       = fmt.Errorf("server is closing")
	ErrNoSubscribers = fmt.Errorf("no subscribers")
)

type ServerOptions struct {
	Log       *log2.Log
	OnAccept  AcceptFunc
	OnAuth    AuthFunc
	OnClosed  ClosedFunc
	OnPublish MessageFunc
}

type AcceptFunc = func(context.Context, transport.Conn, *BackendOptions) (*backend, error)
type AuthFunc = func(context.Context, string, string, *BackendOptions, packet.Generic) (bool, error)
type ClosedFunc = func(*backend, bool, *packet.Message)
type MessageFunc = func(context.Context, *packet.Message, *future.Future) error

// Server.subs is prefix tree of pattern -> []{client, qos}
type subscription struct {
	pattern string
	client  string
	qos     packet.QOS
}

type Server struct { //nolint:maligned
	sync.RWMutex

	alive    *alive.Alive
	backends struct {
		sync.RWMutex
		m map[string]*backend
	}
	ctx       context.Context
	listens   map[string]*transport.NetServer
	log       *log2.Log
	nextid    uint32 // atomic packet.ID
	onAccept  AcceptFunc
	onAuth    AuthFunc
	onClosed  ClosedFunc
	onPublish MessageFunc
	retain    *topic.Tree // *packet.Message
	subs      *topic.Tree // *subscription
}

func NewServer(opt *ServerOptions) *Server {
	s := &Server{
		alive:  alive.NewAlive(),
		retain: topic.NewStandardTree(),
		subs:   topic.NewStandardTree(),
	}
	s.onAccept = s.defaultAccept
	s.onAuth = defaultAuthDenyAll
	s.onClosed = s.defaultClosed
	if opt != nil && opt.Log != nil {
		s.log = opt.Log
	}
	if opt != nil && opt.OnAccept != nil {
		s.onAccept = opt.OnAccept
	}
	if opt != nil && opt.OnAuth != nil {
		s.onAuth = opt.OnAuth
	}
	if opt != nil && opt.OnClosed != nil {
		s.onClosed = opt.OnClosed
	}
	if opt != nil && opt.OnPublish != nil {
		s.onPublish = opt.OnPublish
	}
	s.backends.m = make(map[string]*backend)
	return s
}

func (s *Server) Addrs() []string {
	s.RLock()
	defer s.RUnlock()
	addrs := make([]string, 0, len(s.listens))
	for _, l := range s.listens {
		addrs = append(addrs, l.Addr().String())
	}
	return addrs
}

func (s *Server) Close() error {
	// serialize well with acceptLoop
	s.alive.Stop()
	errs := make([]error, 0)
	toolpool.WithLock(s, func() {
		for key, ns := range s.listens {
			if err := ns.Close(); err != nil {
				errs = append(errs, err)
			}
			delete(s.listens, key)
		}
	})
	toolpool.WithLock(s.backends.RLocker(), func() {
		for _, b := range s.backends.m {
			switch err := b.die(nil); err {
			case nil, ErrClosing, io.EOF:

			default:
				errs = append(errs, err)
			}
		}
	})
	s.alive.Wait()
	return helpers.FoldErrors(errs)
}

func (s *Server) Listen(ctx context.Context, lopts []*BackendOptions) error {
	s.Lock()
	defer s.Unlock()

	if !s.alive.IsRunning() {
		return errors.Errorf("Listen after Close")
	}
	s.ctx = ctx
	s.listens = make(map[string]*transport.NetServer, len(lopts))

	errs := make([]error, 0)
	for _, opt := range lopts {
		s.log.Debugf("listen url=%s", opt.URL)
		ns, err := s.listen(opt)
		if err != nil {
			err = errors.Annotatef(err, "mqtt listen url=%s", opt.URL)
			errs = append(errs, err)
			continue
		}
		if !s.alive.Add(1) {
			break
		}
		s.listens[opt.URL] = ns
		go s.acceptLoop(ns, opt)
	}
	return helpers.FoldErrors(errs)
}

func (s *Server) NextID() packet.ID {
	u32 := atomic.AddUint32(&s.nextid, 1)
	return packet.ID(u32 % (1 << 16))
}

func (s *Server) Publish(ctx context.Context, msg *packet.Message) error {
	s.log.Debugf("Server.Publish msg=%s", msg.String())
	id := s.NextID()

	if msg.Retain {
		if len(msg.Payload) != 0 {
			s.retain.Set(msg.Topic, msg.Copy())
		} else {
			s.retain.Empty(msg.Topic)
		}
	}

	var _a [8]*subscription
	subs := _a[:0]
	for _, x := range s.subs.Match(msg.Topic) {
		subs = append(subs, x.(*subscription))
	}
	n := len(subs)
	s.log.Debugf("Server.Publish len(subs)=%d", n)
	if n == 0 {
		return ErrNoSubscribers
	}

	errch := make(chan error, n)
	successCount := uint32(0)
	wg := sync.WaitGroup{}
	toolpool.WithLock(s.backends.RLocker(), func() {
		for _, sub := range subs {
			b, ok := s.backends.m[sub.client]
			if !ok {
				continue
			}
			wg.Add(1)
			bmsg := msg.Copy()
			bmsg.QOS = sub.qos
			go func() {
				defer wg.Done()
				if err := b.Publish(ctx, id, bmsg); err != nil {
					errch <- err
				} else {
					atomic.AddUint32(&successCount, 1)
				}
			}()
		}
	})
	wg.Wait()
	close(errch)
	atomic.LoadUint32(&successCount)
	return helpers.FoldErrChan(errch)
}

func (s *Server) Retain() []*packet.Message {
	xs := s.retain.All()
	if len(xs) == 0 {
		return nil
	}
	ms := make([]*packet.Message, len(xs))
	for i, x := range xs {
		ms[i] = x.(*packet.Message)
	}
	return ms
}

func (s *Server) listen(opt *BackendOptions) (*transport.NetServer, error) {
	u, err := url.ParseRequestURI(opt.URL)
	if err != nil {
		return nil, errors.Annotate(err, "parse url")
	}

	var ns *transport.NetServer
	switch u.Scheme {
	case "tls":
		if ns, err = transport.CreateSecureNetServer(u.Host, opt.TLS); err != nil {
			return nil, errors.Annotate(err, "CreateSecureNetServer")
		}

	case "tcp", "unix":
		listen, err := net.Listen(u.Scheme, u.Host)
		if err != nil {
			return nil, errors.Annotatef(err, "net.Listen network=%s address=%s", u.Scheme, u.Host)
		}
		ns = transport.NewNetServer(listen)
	}
	if ns == nil {
		return nil, errors.Errorf("unsupported listen url=%s", opt.URL)
	}
	return ns, nil
}

func (s *Server) acceptLoop(ns *transport.NetServer, opt *BackendOptions) {
	defer s.alive.Done() // one alive subtask for each listener
	for {
		conn, err := ns.Accept()
		if !s.alive.IsRunning() {
			return
		}
		if err != nil {
			err = errors.Annotatef(err, "accept listen=%s", opt.URL)
			// TODO for extra cheese, this error must propagate to s.Close() return value
			s.log.Error(err)
			s.alive.Stop()
			return
		}

		if !s.alive.Add(1) { // and one alive subtask for each connection
			return
		}
		go s.processConn(conn, opt)
	}
}

func (s *Server) defaultAccept(ctx context.Context, conn transport.Conn, opt *BackendOptions) (*backend, error) {
	// Receive first packet without backend
	pkt, err := conn.Receive()
	if err != nil {
		return nil, errors.Trace(err)
	}

	pktConnect, ok := pkt.(*packet.Connect)
	if !ok {
		err = broker.ErrUnexpectedPacket
		return nil, errors.Trace(err)
	}

	connack := packet.NewConnack()
	connack.SessionPresent = false

	// Server MAY allow a Client to supply a ClientId that has a length of zero bytes,
	// however if it does so the Server MUST treat this as a special case and assign a unique ClientId to that Client
	// if pktConnect.ClientID == "" && pktConnect.CleanSession { clientID = randomSaltPlusConnPort() }
	if pktConnect.ClientID == "" {
		connack.ReturnCode = packet.IdentifierRejected
		_ = conn.Send(connack, false)
		err = errors.Annotatef(broker.ErrNotAuthorized, "invalid clientid=%s", pktConnect.ClientID)
		return nil, errors.Trace(err)
	}

	ok, err = s.onAuth(ctx, pktConnect.ClientID, pktConnect.Username, opt, pktConnect)
	if err != nil {
		return nil, errors.Trace(err)
	}
	if !ok {
		connack.ReturnCode = packet.NotAuthorized
		_ = conn.Send(connack, false)
		err = broker.ErrNotAuthorized
		return nil, errors.Trace(err)
	}

	connack.ReturnCode = packet.ConnectionAccepted
	requestedKeepAlive := time.Duration(pktConnect.KeepAlive) * time.Second
	if requestedKeepAlive == 0 || requestedKeepAlive > opt.NetworkTimeout {
		requestedKeepAlive = opt.NetworkTimeout
	}
	conn.SetReadTimeout(requestedKeepAlive + time.Duration(requestedKeepAlive/2)*time.Second)
	err = conn.Send(connack, false)
	if err != nil {
		return nil, errors.Trace(err)
	}

	b := newBackend(ctx, conn, opt, s, pktConnect)
	return b, nil
}

func defaultAuthDenyAll(ctx context.Context, id, username string, opt *BackendOptions, pkt packet.Generic) (bool, error) {
	return false, fmt.Errorf("default auth callback is deny-all, please supply ServerOptions.OnAuth")
}

func (s *Server) defaultSubscribe(b *backend, pkt *packet.Subscribe) error {
	if len(pkt.Subscriptions) == 0 {
		return b.die(fmt.Errorf("subscribe request with empty sub list"))
	}
	suback := packet.NewSuback()
	suback.ID = pkt.ID
	suback.ReturnCodes = make([]packet.QOS, len(pkt.Subscriptions))
	for i, sub := range pkt.Subscriptions {
		sub2 := &subscription{
			pattern: sub.Topic,
			client:  b.id,
			qos:     sub.QOS,
		}
		if sub2.qos > packet.QOSAtLeastOnce {
			sub2.qos = packet.QOSAtLeastOnce
		}
		s.subs.Add(sub.Topic, sub2)
		suback.ReturnCodes[i] = sub2.qos

		values := s.retain.Search(sub.Topic)
		for _, v := range values {
			pid := s.NextID()
			msg := v.(*packet.Message)
			go func() {
				_ = b.Publish(s.ctx, pid, msg)
			}()
		}
	}
	err := b.Send(suback)
	err = errors.Annotate(err, "defaultSubscribe")
	return err
}

func (s *Server) defaultClosed(b *backend, clean bool, will *packet.Message) {
	s.log.Infof("mqtt defaultClose id=%s clean=%t will=%v", b.id, clean, will)
	if !clean && will != nil {
		_ = s.Publish(s.ctx, will)
	}
}

func (s *Server) processConn(conn transport.Conn, opt *BackendOptions) {
	defer s.alive.Done()

	addrNew := addrString(conn.RemoteAddr())
	conn.SetMaxWriteDelay(0)
	conn.SetReadLimit(defaultReadLimit)
	conn.SetReadTimeout(opt.NetworkTimeout)
	b, err := s.onAccept(s.ctx, conn, opt)
	s.log.Debugf("mqtt new conn addr=%s onAccept err=%v", addrNew, err)
	if err != nil {
		_ = conn.Close()
		return
	}

	toolpool.WithLock(&s.backends, func() {
		// close existing client with same id
		if ex, ok := s.backends.m[b.id]; ok {
			addrEx := addrString(ex.RemoteAddr())
			addrNew := addrString(b.getConn().RemoteAddr())
			s.log.Infof("mqtt client overtake id=%s ex=%s new=%s", b.id, addrEx, addrNew)
			_ = ex.die(ErrSameClient)
		}
		s.backends.m[b.id] = b
	})

	// receive loop
	wg := sync.WaitGroup{}
	for {
		var pkt packet.Generic
		pkt, err = b.Receive()
		if !b.alive.IsRunning() || !s.alive.IsRunning() {
			_ = b.die(ErrClosing)
			break
		}
		if err != nil {
			break
		}
		wg.Add(1)
		go s.processPacket(b, pkt, &wg)
	}
	wg.Wait()

	graceTimeout := b.opt.NetworkTimeout
	_ = b.acks.Await(graceTimeout)
	b.acks.Clear()
	b.alive.WaitTasks()

	// mandatory cleanup on backend closed, call app specific onClose if set
	err = b.die(ErrClosing)
	clean := err == nil || err == ErrClosing
	b.willmu.Lock()
	will := b.will
	b.willmu.Unlock()
	toolpool.WithLock(&s.backends, func() {
		if ex := s.backends.m[b.id]; b == ex {
			s.log.Debugf("mqtt id=%s clean=%t will=%v", b.id, clean, will)
			delete(s.backends.m, b.id)
		}
		for _, value := range s.subs.All() {
			if sub := value.(*subscription); sub.client == b.id {
				s.subs.Remove(sub.pattern, value)
			}
		}
	})
	if s.onClosed != nil {
		s.onClosed(b, clean, will)
	}
}

// on each incoming packet
func (s *Server) processPacket(b *backend, pkt packet.Generic, finally interface{ Done() }) {
	defer finally.Done()
	err := toolpool.WithLockError(&s.backends, func() error {
		ex := s.backends.m[b.id]
		s.log.Debugf("mqtt processPacket backends[%s]=%s", b.id, addrString(ex.RemoteAddr()))
		if b != ex {
			panic("oh no no still shit")
			// s.log.Errorf("mqtt processPacket ignore from detached id=%s pkt=%s", b.id, pkt.String())
			// _ = b.die(ErrSameClient)
			// return ErrSameClient
		}
		return nil
	})
	if err != nil {
		return
	}

typeSwitch:
	switch pt := pkt.(type) {
	case *packet.Pingreq:
		err = b.Send(packet.NewPingresp())

	case *packet.Publish:
		if s.onPublish != nil {
			ack := future.New()
			err = s.onPublish(b.ctx, &pt.Message, ack)
			if err != nil {
				s.log.Errorf("mqtt onPublish msg=%s err=%v", pt.Message.String(), err)
				break typeSwitch
			}

			switch pt.Message.QOS {
			case packet.QOSAtMostOnce:
			case packet.QOSAtLeastOnce:
				switch ack.Wait(0) {
				case nil: // explicit ack
					pktPuback := packet.NewPuback()
					pktPuback.ID = pt.ID
					err = b.Send(pktPuback)

				case future.ErrCanceled: // explicit nack
					err = fmt.Errorf("publish rejected client=%s id=%d topic=%s message=%x", b.id, pt.ID, pt.Message.Topic, pt.Message.Payload)

				case future.ErrTimeout: // onPublish callback did not complete/cancel future
				}

			default:
				err = fmt.Errorf("qos %d is not supported", pt.Message.QOS)
			}
		}

	case *packet.Puback:
		err = b.FulfillAck(pt.ID)

	case *packet.Subscribe:
		err = s.defaultSubscribe(b, pt)
		if err != nil {
			s.log.Errorf("mqtt defaultSubscribe err=%v", err)
		}

	case *packet.Pubrec, *packet.Pubrel, *packet.Pubcomp:
		err = fmt.Errorf("qos2 not supported")

	case *packet.Disconnect:
		toolpool.WithLock(&b.willmu, func() {
			b.will = nil
		})
		// err := b.defaultDisconnect(pt)
		_ = b.die(nil)
		b.alive.Wait()
		return

	default:
		err = fmt.Errorf("code error packet is not handled pkt=%s", pkt.String())
	}
	if err != nil {
		_ = b.die(err)
	}
}

func addrString(a net.Addr) string {
	if a == nil {
		return ""
	}
	return a.String()
}
