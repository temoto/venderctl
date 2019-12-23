package passwd

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"

	"github.com/juju/errors"
	"github.com/temoto/venderctl/cmd/internal/cli"
	"github.com/temoto/venderctl/internal/state"
	"github.com/temoto/venderctl/internal/tele"
	"gopkg.in/hlandau/passlib.v1"
)

const argOffset = 1 // Arg(0)=.Name
const cmdUsage = "{gen | hash} USERNAME"

var Cmd = cli.Cmd{
	Name:   "passwd",
	Desc:   "tool for managing secrets for tele clients",
	Usage:  cmdUsage,
	Action: passwdMain,
}

func passwdMain(ctx context.Context, flags *flag.FlagSet) error {
	cmd := flags.Arg(argOffset + 0)
	g := state.GetGlobal(ctx)

	switch cmd {
	case "gen": // generate new password, write to secrets, stdout vender config piece
		return subGen(ctx, flags)

	case "hash": // hash string from stdin, dont touch files
		g.Log.Infof("reading password from stdin")
		return subHash(ctx, flags)

	default:
		return fmt.Errorf("unknown passwd command=%s", cmd)
	}
}

func subGen(ctx context.Context, flags *flag.FlagSet) error {
	username := flags.Arg(argOffset + 1)
	if username == "" {
		flags.Usage()
		return fmt.Errorf("username is empty")
	}
	g := state.GetGlobal(ctx)

	var secretBytes [16]byte
	if _, err := rand.Read(secretBytes[:]); err != nil {
		return errors.Annotate(err, "rand.Read")
	}
	secret := base64.RawStdEncoding.EncodeToString(secretBytes[:])

	configPath := flags.Lookup("config").Value.String()
	g.Config = state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)

	ss := tele.Secrets{}
	if err := ss.UnsafeReadFile(g.Config.Tele.SecretsPath); err != nil {
		return err
	}
	if err := ss.UnsafeSet(username, secret); err != nil {
		return err
	}
	if err := ss.WriteFile(g.Config.Tele.SecretsPath); err != nil {
		return err
	}
	fmt.Printf("mqtt_password = \"%s\"\n", secret)

	return nil
}

func subHash(ctx context.Context, flags *flag.FlagSet) error {
	username := flags.Arg(argOffset + 1)
	if username == "" {
		flags.Usage()
		return fmt.Errorf("username is empty")
	}
	var secret string
	if _, err := fmt.Scanln(&secret); secret == "" {
		return fmt.Errorf("password is empty")
	} else if err != nil {
		return errors.Annotate(err, "fscan")
	}
	hash, err := passlib.Hash(secret)
	if err != nil {
		return errors.Annotate(err, "passlib.Hash")
	}
	fmt.Printf("%s:%s\n", username, hash)
	return nil
}
