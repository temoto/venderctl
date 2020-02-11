package state

import (
	"net/http"
	"path/filepath"
	"sync"

	"github.com/hashicorp/hcl"
	"github.com/juju/errors"
	"github.com/temoto/vender/currency"
	"github.com/temoto/vender/helpers"
	"github.com/temoto/vender/log2"
	tele_config "github.com/temoto/venderctl/internal/tele/config"
)

type Config struct {
	// includeSeen contains absolute paths to prevent include loops
	includeSeen map[string]struct{}
	// only used for Unmarshal, do not access
	XXX_Include []ConfigSource `hcl:"include"`

	DB struct {
		PingTimeoutMs int    `hcl:"ping_timeout_ms"`
		URL           string `hcl:"url"`
	}
	Money struct {
		Scale int `hcl:"scale"`
	}
	Tax struct {
		Ru2019 struct {
			Tag1009 string // payment address
			Tag1187 string // payment place
			Tag1018 string // business INN
			Tag1055 int    // uint32 tax form
			Tag1199 int    // uint32 tax rate
			Umka    struct {
				BaseURL string `hcl:"base_url"`

				XXX_testRT http.RoundTripper `hcl:"-"`
			}
		}
	}
	Tele tele_config.Config

	_copy_guard sync.Mutex //nolint:unused
}

type ConfigSource struct {
	Name     string `hcl:"name,key"`
	Optional bool   `hcl:"optional"`
}

func (c *Config) ScaleI(i int) currency.Amount {
	return currency.Amount(i) * currency.Amount(c.Money.Scale)
}
func (c *Config) ScaleU(u uint32) currency.Amount          { return currency.Amount(u * uint32(c.Money.Scale)) }
func (c *Config) ScaleA(a currency.Amount) currency.Amount { return a * currency.Amount(c.Money.Scale) }

func (c *Config) read(log *log2.Log, fs FullReader, source ConfigSource, errs *[]error) {
	norm := fs.Normalize(source.Name)
	if _, ok := c.includeSeen[norm]; ok {
		log.Fatalf("config duplicate source=%s", source.Name)
	} else {
		log.Debugf("config reading source='%s' path=%s", source.Name, norm)
	}
	c.includeSeen[source.Name] = struct{}{}
	c.includeSeen[norm] = struct{}{}

	bs, err := fs.ReadAll(norm)
	if bs == nil && err == nil {
		if !source.Optional {
			err = errors.NotFoundf("config required name=%s path=%s", source.Name, norm)
			*errs = append(*errs, err)
			return
		}
	}
	if err != nil {
		*errs = append(*errs, errors.Annotatef(err, "config source=%s", source.Name))
		return
	}

	err = hcl.Unmarshal(bs, c)
	if err != nil {
		err = errors.Annotatef(err, "config unmarshal source=%s content='%s'", source.Name, string(bs))
		*errs = append(*errs, err)
		return
	}

	var includes []ConfigSource
	includes, c.XXX_Include = c.XXX_Include, nil
	for _, include := range includes {
		includeNorm := fs.Normalize(include.Name)
		if _, ok := c.includeSeen[includeNorm]; ok {
			err = errors.Errorf("config include loop: from=%s include=%s", source.Name, include.Name)
			*errs = append(*errs, err)
			continue
		}
		c.read(log, fs, include, errs)
	}
}

func ReadConfig(log *log2.Log, fs FullReader, names ...string) (*Config, error) {
	if len(names) == 0 {
		log.Fatal("code error [Must]ReadConfig() without names")
	}

	if osfs, ok := fs.(*OsFullReader); ok {
		dir, name := filepath.Split(names[0])
		osfs.SetBase(dir)
		names[0] = name
	}
	c := &Config{
		includeSeen: make(map[string]struct{}),
	}
	errs := make([]error, 0, 8)
	for _, name := range names {
		c.read(log, fs, ConfigSource{Name: name}, &errs)
	}
	return c, helpers.FoldErrors(errs)
}

func MustReadConfig(log *log2.Log, fs FullReader, names ...string) *Config {
	c, err := ReadConfig(log, fs, names...)
	if err != nil {
		log.Fatal(errors.ErrorStack(err))
	}
	return c
}
