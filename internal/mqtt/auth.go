package mqtt

import (
	"context"
	"fmt"
	"sync"

	"github.com/256dpi/gomqtt/packet"
)

func AuthFromMap(m map[string]string, locker sync.Locker) AuthFunc {
	return func(ctx context.Context, id string, username string, opt *BackendOptions, pkt packet.Generic) (bool, error) {
		if locker != nil {
			locker.Lock()
			defer locker.Unlock()
		}
		switch p := pkt.(type) {
		case *packet.Connect:
			if secret, ok := m[p.Username]; ok {
				return p.Password == secret, nil
			}
			return false, nil

		default:
			if id == "" {
				return false, fmt.Errorf("code error OnAuth pkt!=CONNECT but client=(empty)")
			}
			_, ok := m[username]
			return ok, nil
		}
	}
}
