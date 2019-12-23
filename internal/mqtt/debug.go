package mqtt

import (
	"fmt"

	"github.com/256dpi/gomqtt/packet"
)

// Minor readability improvement
// PUBLISH payload as hex, no duplicate "Message=<Message"
func packetString(p packet.Generic) string {
	if p == nil {
		return "(nil)"
	}
	if pub, ok := p.(*packet.Publish); ok {
		return fmt.Sprintf("<Publish ID=%d Dup=%t Topic=%q QOS=%d Retain=%t Payload=%x>",
			pub.ID, pub.Dup, pub.Message.Topic, pub.Message.QOS, pub.Message.Retain, pub.Message.Payload)
	}
	return p.String()
}
