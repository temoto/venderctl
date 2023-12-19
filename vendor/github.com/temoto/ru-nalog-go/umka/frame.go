package umka

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/juju/errors"
	ru_nalog "github.com/temoto/ru-nalog-go"
)

// Request/response container for marshalling
type Frame struct {
	Protocol      int       `json:"protocol,omitempty"` // 1=JSON 3=XML
	Version       string    `json:"version,omitempty"`  // "1.0"
	CashboxStatus *Status   `json:"cashboxStatus,omitempty"`
	Document      *Document `json:"document,omitempty"`
}

type Document struct {
	Result  int      `json:"result,omitempty"` // 0=OK
	Message struct { // when result!=0
		Description string `json:"resultDescription,omitempty"`
	} `json:"message,omitempty"`
	Data      docdata `json:"data"`
	SessionID string  `json:"sessionId"`
	Print     int     `json:"print"` // bool, 1=print
}

type docdata struct {
	DocNumber uint32           `json:"docNumber,omitempty"`
	DocType   ru_nalog.DocType `json:"docType,omitempty"`
	Name      string           `json:"name,omitempty"`
	MoneyType int              `json:"moneyType"` //ТИП ОПЛАТЫ (1. Наличным, 2. Электронными, 3. Предоплата, 4. Постоплата, 5. Встречное предоставление)
	Sum       uint64           `json:"sum"`       // Сумма закрытия чека (может быть 0, если без сдачи)
	Type      int              `json:"type"`      // Тип документа (1. Продажа,2.Возврат продажи, 4. Покупка, 5. Возврат покупки, 7. Коррекция прихода, 9. Коррекция расхода)
	Props     []Prop           `json:"fiscprops"`
}

type Prop struct {
	Caption   string       `json:"caption,omitempty"`
	Printable string       `json:"printable,omitempty"`
	Props     []Prop       `json:"fiscprops,omitempty"`
	Tag       ru_nalog.Tag `json:"tag"`
	Value     interface{}  `json:"value,omitempty"`
}

func (f *Frame) String() string {
	if f == nil {
		return ""
	}
	s := fmt.Sprintf("(protocol=%d version=%s", f.Protocol, f.Version)
	if f.CashboxStatus != nil {
		s += fmt.Sprintf("%#v", f.CashboxStatus)
	}
	if f.Document != nil {
		s += fmt.Sprintf("%#v data=%s", f.Document, f.Document.Data.String())
	}
	s += ")"
	return s
}

func (f *Frame) parseJSON(b []byte) error {
	if err := json.Unmarshal(b, f); err != nil {
		err = errors.Trace(err)
		return err
	}
	if !(f.Protocol == 1 && f.Version == "1.0") {
		return errors.Errorf("unknown protocol=%d version='%s'", f.Protocol, f.Version)
	}
	return nil
}

func (f *Frame) checkDocumentResult() error {
	if f.Document == nil {
		return errors.Errorf("no document")
	}
	if f.Document.Result != 0 {
		return errors.Errorf("result=%d message=%s", f.Document.Result, f.Document.Message.Description)
	}
	return nil
}

func ParseResponseDoc(b []byte) (*ru_nalog.Doc, error) {
	var f Frame
	if err := f.parseJSON(b); err != nil {
		return nil, err
	}
	if err := f.checkDocumentResult(); err != nil {
		return nil, err
	}
	return f.Document.Data.ToDoc()
}

func (d *docdata) String() string {
	if d == nil {
		return ""
	}
	doc, _ := d.ToDoc()
	if doc == nil {
		return ""
	}
	return doc.String()
}

func (d *docdata) ToDoc() (*ru_nalog.Doc, error) {
	fd := ru_nalog.NewDoc(d.DocNumber, d.DocType)
	errs := make([]error, 0, 8)
	for _, p := range d.Props {
		t, err := p.toTLV()
		if err == nil {
			fd.Props.Append(t)
		} else {
			errs = append(errs, err)
		}
	}
	return fd, foldErrors(errs)
}

func (d *docdata) setDoc(doc *ru_nalog.Doc) error {
	d.Type = 1      // FIXME from doc
	d.MoneyType = 1 // FIXME from doc
	// d.Sum = 0       // FIXME from doc/gross
	d.Props = make([]Prop, 0, 64) // TODO d.Len()
	for _, t := range doc.Props.Children() {
		if p, err := propFromTLV(t); err != nil {
			return err
		} else {
			d.Props = append(d.Props, p)
		}
	}
	return nil
}

func propFromTLV(t ru_nalog.TLV) (Prop, error) {
	p := Prop{}
	p.Tag = t.Tag
	children := t.Children()
	switch {
	case t.Tag == 1023: // TODO t.Kind==FVLN ?
		p.Value = fmt.Sprintf("%.3f", t.Float64())

	case children != nil:
		p.Props = make([]Prop, 0, len(children))
		for _, it := range children {
			if ip, err := propFromTLV(it); err != nil {
				return p, err
			} else {
				p.Props = append(p.Props, ip)
			}
		}

	default:
		p.Value = t.Value()
	}
	return p, nil
}

func (p *Prop) toTLV() (*ru_nalog.TLV, error) {
	// log.Printf("prop=%#v", p)
	switch p.Tag {
	case 1196: // QR query string
		t := &ru_nalog.TLV{
			TagDesc: ru_nalog.TagDesc{
				Tag:    1196,
				Kind:   ru_nalog.DataKindString,
				Varlen: true,
			},
		}
		t.SetValue(p.Value.(string))
		return t, nil
	}
	t := ru_nalog.NewTLV(p.Tag)
	if t == nil {
		return nil, errors.Errorf("prop=%#v invalid tag", p)
	}
	switch t.Kind {
	case ru_nalog.DataKindSTLV:
		for _, child := range p.Props {
			subt, err := child.toTLV()
			if err != nil {
				err = errors.Annotatef(err, "prop=%#v", p)
				return nil, err
			}
			t.Append(subt)
		}
	case ru_nalog.DataKindTime:
		crude := p.Value.(string)
		tim, err := time.Parse(TimeLayout, crude)
		if err != nil {
			err = errors.Annotatef(err, "prop=%#v", p)
			return nil, err
		}
		t.SetValue(tim)
	case ru_nalog.DataKindUint:
		switch pt := p.Value.(type) {
		case bool:
			if p.Value.(bool) {
				t.SetValue(uint32(1))
			} else {
				t.SetValue(uint32(0))
			}
		case float64: // encoding/json approach to unmarshal JSON integer token into interface{}
			crude := p.Value.(float64)
			t.SetValue(uint32(crude))
		case uint32:
			t.SetValue(pt)
		default:
			panic(fmt.Sprintf("TODO tag=%d %[2]T %[2]v %#[2]v", p.Tag, p.Value))
		}
	case ru_nalog.DataKindBytes:
		switch p.Value.(type) {
		case string:
			t.SetValue(p.Value)
		case float64: // umka joke on byte[fixed] (1077), forced print format
			crude := p.Value.(float64)
			// TODO FIXME
			t.SetValue(uint64(crude))
		default:
			panic(fmt.Sprintf("TODO tag=%d %[2]T %[2]v %#[2]v", p.Tag, p.Value))
		}
	case ru_nalog.DataKindVLN:
		switch pt := p.Value.(type) {
		case float64: // encoding/json approach to unmarshal JSON integer token into interface{}
			crude := p.Value.(float64)
			t.SetValue(uint64(crude))
		case uint32:
			t.SetValue(pt)
		default:
			panic(fmt.Sprintf("TODO tag=%d %[2]T %[2]v %#[2]v", p.Tag, p.Value))
		}
	case ru_nalog.DataKindFVLN:
		switch pt := p.Value.(type) {
		case float64:
			t.SetValue(fmt.Sprintf("%.3f", pt))
		case string: // umka joke on FVLN (1023), forced print format
			crude := p.Value.(string)
			// "1 333,500" -> 1333500
			crude = strings.Replace(crude, " ", "", -1)
			crude = strings.Replace(crude, ",", ".", 1)
			t.SetValue(crude)
			if err := t.Err(); err != nil {
				err = errors.Annotatef(err, "prop=%#v", p)
				return nil, err
			}
		default:
			panic(fmt.Sprintf("TODO tag=%d %[2]T %[2]v %#[2]v", p.Tag, p.Value))
		}
	default:
		t.SetValue(p.Value)
	}
	t.Caption = p.Caption
	t.Printable = p.Printable
	return t, nil
}

func foldErrors(errs []error) error {
	// common fast path
	if len(errs) == 0 {
		return nil
	}

	ss := make([]string, 0, 1+len(errs))
	for _, e := range errs {
		if e != nil {
			// ss = append(ss, e.Error())
			ss = append(ss, errors.ErrorStack(e))
			// ss = append(ss, errors.Details(e))
		}
	}
	switch len(ss) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf(ss[0])
	default:
		ss = append(ss, "")
		copy(ss[1:], ss[0:])
		ss[0] = "multiple errors:"
		return fmt.Errorf(strings.Join(ss, "\n- "))
	}
}
