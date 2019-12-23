package state

import (
	"log"

	"github.com/temoto/vender/log2"
)

// TODO func (*log2.Log) Stdlib() *log.Logger
func Log2stdlib(l2 *log2.Log) *log.Logger {
	return log.New(log2stdWrap{l2}, "", 0)
}

type log2stdWrap struct{ *log2.Log }

func (l log2stdWrap) Write(b []byte) (int, error) {
	l.Printf(string(b))
	return len(b), nil
}
