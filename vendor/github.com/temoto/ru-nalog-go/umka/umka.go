package umka

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"github.com/juju/errors"
	ru_nalog "github.com/temoto/ru-nalog-go"
)

// RFC822Z +century,seconds or RFC1123Z -dayofweek
const TimeLayout = "02 Jan 2006 15:04:05 -0700"

type Umker interface {
	CalcReport() (*ru_nalog.Doc, error)
	CycleClose() (*ru_nalog.Doc, error)
	CycleOpen() (*ru_nalog.Doc, error)
	Danger_CloseFiscalStorage(sessionId string) (*ru_nalog.Doc, error)
	FiscalCheck(sessionId string, d *ru_nalog.Doc) (*ru_nalog.Doc, error)
	Fiscalize(sessionId string, d *ru_nalog.Doc) (*ru_nalog.Doc, error)
	GetDoc(number uint32) (*ru_nalog.Doc, error)
	Status() (*Status, error)
	XReport() (*ru_nalog.Doc, error)
}

type Umka struct {
	config *UmkaConfig
	rt     http.RoundTripper
	url    *url.URL
}
type UmkaConfig struct {
	BaseURL   string
	SecretFun func() (string, string)
	RT        http.RoundTripper
}

var _ /*type check*/ Umker = &Umka{}

func NewUmka(config *UmkaConfig) (Umker, error) {
	u := &Umka{
		config: config,
		rt:     config.RT,
	}
	if u.rt == nil {
		u.rt = http.DefaultTransport
	}
	if baseURL, err := url.Parse(config.BaseURL); err == nil {
		u.url = baseURL
	} else {
		err = errors.Trace(err)
		return nil, err
	}
	return u, nil
}

func (u *Umka) Status() (*Status, error) {
	const tag = "Umka.Status"
	body, err := u.request("GET", "/cashboxstatus.json", nil)
	if err != nil {
		return nil, errors.Annotate(err, tag)
	}
	var f Frame
	if err = f.parseJSON(body); err != nil {
		return nil, errors.Annotate(err, tag)
	}
	return f.CashboxStatus, nil
}

func (u *Umka) GetDoc(number uint32) (*ru_nalog.Doc, error) {
	return u.getDocJSON(fmt.Sprintf("/fiscaldoc.json?number=%d", number))
}

func (u *Umka) FiscalCheck(sessionId string, d *ru_nalog.Doc) (*ru_nalog.Doc, error) {
	f := Frame{Document: &Document{SessionID: sessionId}}
	if err := f.Document.Data.setDoc(d); err != nil {
		return nil, err
	}
	return u.requestDocJSON("POST", "/fiscalcheck.json", &f)
}

func (u *Umka) CycleOpen() (*ru_nalog.Doc, error)  { return u.getDocJSON("/cycleopen.json") }
func (u *Umka) CycleClose() (*ru_nalog.Doc, error) { return u.getDocJSON("/cycleclose.json") }

func (u *Umka) getDocJSON(path string) (*ru_nalog.Doc, error) {
	return u.requestDocJSON("GET", path, nil)
}

func (u *Umka) requestDocJSON(method, path string, req *Frame) (*ru_nalog.Doc, error) {
	f, err := u.requestJSON(method, path, req)
	if err != nil {
		return nil, errors.Annotate(err, path)
	}
	if f.Document.Result != 0 {
		return nil, errors.Errorf("umka.requestDocJSON result=%d resultDescription=%s req=%s f=%s", f.Document.Result, f.Document.Message.Description, req.String(), f.String())
	}
	doc, err := f.Document.Data.ToDoc()
	if err != nil {
		err = errors.Annotatef(err, "umka.requestDocJSON/ToDoc req=%s f=%#v", req.String(), f.String())
	}
	return doc, err
}

func (u *Umka) requestJSON(method, path string, req *Frame) (*Frame, error) {
	var respBody []byte
	var err error
	if req != nil {
		if respBody, err = json.Marshal(req); err != nil {
			return nil, errors.Annotatef(err, "umka.requestJSON/json.Marshal req=%#v", req)
		}
	}
	if respBody, err = u.request(method, path, respBody); err != nil {
		return nil, err
	}
	var resp Frame
	if err = json.Unmarshal(respBody, &resp); err != nil {
		return nil, errors.Annotatef(err, "umka.requestJSON/json.Unmarshal respBody=%x", respBody)
	}
	return &resp, nil
}

func (u *Umka) request(method, path string, body []byte) ([]byte, error) {
	url := *u.url
	url.Path = path
	br := io.Reader(nil)
	if body != nil {
		br = bytes.NewReader(body)
	}
	urlString := url.String()
	req, err := http.NewRequest(method, urlString, br)
	if err != nil {
		return nil, errors.Annotatef(err, "umka.request method=%s url=%s body=%x", method, urlString, body)
	}
	req.Header.Set("User-Agent", "")
	username, password := "", ""
	if u.config.SecretFun != nil {
		username, password = u.config.SecretFun()
	} else {
		username = req.URL.User.Username()
		password, _ = req.URL.User.Password()
	}
	req.SetBasicAuth(username, password)

	resp, err := u.rt.RoundTrip(req)
	if err != nil {
		return nil, errors.Annotatef(err, "umka.request request method=%s url=%s body=%x", method, urlString, body)
	}
	defer resp.Body.Close()
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Annotatef(err, "umka.request request method=%s url=%s body=%x response status=%d respBody=%x", method, urlString, body, resp.StatusCode, respBody)
	}
	if resp.StatusCode != 200 {
		return nil, errors.Errorf("umka.request request method=%s url=%s body=%x response status=%d respBody=%x", method, urlString, body, resp.StatusCode, respBody)
	}
	return respBody, nil
}

func EnsureCycleValid(u Umker, status *Status, maxAge time.Duration) (*ru_nalog.Doc, error) {
	if age, err := status.CycleAge(); err != nil {
		return nil, err
	} else if age < 0 {
		return u.CycleOpen()
	} else if age >= maxAge {
		if _, err := u.CycleClose(); err != nil {
			return nil, err
		}
		return u.CycleOpen()
	}
	return nil, nil
}
