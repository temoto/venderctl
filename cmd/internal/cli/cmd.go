package cli

import (
	"context"
	"flag"
	"log"

	"github.com/coreos/go-systemd/daemon"
	"github.com/juju/errors"
)

type Cmd struct {
	Name   string
	Desc   string
	Usage  string
	Action func(context.Context, *flag.FlagSet) error
}

func IsHelp(s string) bool {
	switch s {
	case "-h", "-help", "--help", "h", "help":
		return true
	}
	return false
}

func SdNotify(s string) bool {
	ok, err := daemon.SdNotify(false, s)
	if err != nil {
		log.Fatal("sdnotify: ", errors.ErrorStack(err))
	}
	return ok
}
