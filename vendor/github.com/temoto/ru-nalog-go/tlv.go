package ru_nalog

//go:generate ./script/generate

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Tag uint16

type TagDesc struct {
	Kind   DataKind
	Tag    Tag
	Length uint16
	Varlen bool
}

var userTags []TagDesc

// Will stable sort argument, return previous value.
// Not thread-safe.
func RegisterTags(ts []TagDesc) (prev []TagDesc) {
	prev, userTags = userTags, ts
	sort.SliceStable(userTags, func(i, j int) bool { return userTags[i].Tag < userTags[j].Tag })
	return prev
}

func searchSortedTagDesc(tag Tag, xs []TagDesc) *TagDesc {
	// please forgive optimisation junkie
	// it's 2x faster than sort.Search() because f() is inlined and early return
	// copy from go stdlib sort.Search
	i, j := uint32(0), uint32(len(xs))
	for i < j {
		h := (i + j) >> 1
		item := &xs[h]
		if item.Tag == tag {
			return item
		} else if item.Tag < tag {
			i = h + 1
		} else {
			j = h
		}
	}
	return nil
}

// Searches in user tags (RegisterTags) first, then in builtin tags.
// Not thread-safe.
func FindTag(tag Tag) *TagDesc {
	// very likely shortcut
	if userTags != nil {
		if u := searchSortedTagDesc(tag, userTags); u != nil {
			return u
		}
	}
	return searchSortedTagDesc(tag, builtinTags[:])
}

type TLV struct {
	TagDesc
	Caption   string
	Printable string
	value     interface{}
}

func NewTLV(tag Tag) *TLV {
	desc := FindTag(tag)
	if desc == nil {
		return nil
	}
	tlv := &TLV{TagDesc: *desc}
	if tlv.Kind == DataKindSTLV {
		tlv.value = make([]TLV, 0, 8)
	}
	return tlv
}

func (self *TLV) Children() []TLV {
	if self == nil {
		return nil
	}
	if self.TagDesc.Kind != DataKindSTLV {
		return nil
	}
	return self.value.([]TLV)
}

func (self *TLV) Err() error {
	if self == nil {
		return fmt.Errorf("TLV(nil).Err()")
	}
	if e, ok := self.value.(error); ok {
		return e
	}
	return nil
}

func (self *TLV) Value() interface{} { return self.value }

func (self *TLV) Append(n *TLV) *TLV {
	if self == nil {
		return nil
	}
	if self.TagDesc.Kind != DataKindSTLV {
		return nil
	}
	list := self.value.([]TLV)
	list = append(list, *n)
	self.value = list
	n2 := &list[len(list)-1]
	return n2
}

func (self *TLV) AppendNew(tag Tag, value interface{}) *TLV {
	n := NewTLV(tag)
	if value != nil {
		n.SetValue(value)
	}
	return self.Append(n)
}

func (self *TLV) SetValue(value interface{}) {
	self.value = nil
	switch self.TagDesc.Kind {
	case DataKindBool:
		self.value = value.(bool)
	case DataKindBytes:
		if x, ok := value.([]byte); ok {
			self.value = x
		}
		if x, ok := value.(string); ok {
			self.value = []byte(x)
		}
	case DataKindFVLN:
		switch x := value.(type) {
		case float32:
			self.value = float64(x)
		case float64:
			self.value = x
		case string:
			if f, err := strconv.ParseFloat(x, 64); err != nil {
				self.value = err
			} else {
				self.value = f
			}
		default:
			if n, ok := toUint64(value); ok {
				self.value = float64(n)
			}
		}
	case DataKindSTLV:
		if n, ok := value.([]TLV); ok {
			self.value = n
		}
	case DataKindString:
		self.value = toString(value)
	case DataKindTime:
		self.value = toDt(value)
	case DataKindUint:
		if n, ok := toUint64(value); ok {
			self.value = uint32(n)
		}
	case DataKindVLN:
		self.value = toVLN(value, self.TagDesc.Length)
	}
	if self.value == nil {
		self.value = fmt.Errorf("SetValue unhandled kind=%s value=%#v", self.TagDesc.Kind.String(), value)
		panic(self.value)
	}
}

func (self *TLV) Bytes() []byte {
	return self.value.([]byte)
}

func (self *TLV) GoString() string {
	b := strings.Builder{}
	fmt.Fprintf(&b, "(#%d", self.Tag)
	switch self.Kind {
	case DataKindBool:
		fmt.Fprintf(&b, " %t", self.Bool())
	case DataKindBytes:
		fmt.Fprintf(&b, " %x", self.Bytes())
	case DataKindFVLN:
		s := fmt.Sprintf("%.f", self.Float64())
		b.WriteByte(' ')
		b.WriteString(s)
	case DataKindSTLV:
		cs := self.Children()
		b.WriteString(" [")
		for i := range cs {
			row := &cs[i]
			if i != 0 {
				b.WriteString(" ")
			}
			b.WriteString(row.GoString())
		}
		b.WriteString("]")
	case DataKindString:
		fmt.Fprintf(&b, " %s", self.String())
	case DataKindTime:
		b.WriteByte(' ')
		b.WriteString(self.Time().Format(time.RFC3339))
	case DataKindUint:
		fmt.Fprintf(&b, " %x", self.Uint32())
	case DataKindVLN:
		fmt.Fprintf(&b, " %d", self.Uint64())
	}
	b.WriteString(")")
	return b.String()
}

func (self *TLV) String() string {
	if !self.Varlen {
		return self.FixedString()
	}
	x := toString(self.value)
	if s, ok := x.(string); ok {
		return s
	}
	panic(fmt.Sprintf("TLV.String() #%d value=%#v", self.Tag, self.value))
}

func (self *TLV) FixedString() string {
	crude := toString(self.value)
	if s, ok := crude.(string); ok {
		return strings.TrimRightFunc(s, isSpace)
	}
	panic(fmt.Sprintf("TLV.FixedString() #%d crude=%#v", self.Tag, crude))
}

func (self *TLV) Bool() bool {
	return self.value.(bool)
}

func (self *TLV) Float64() float64 {
	return self.value.(float64)
}

func (self *TLV) Time() time.Time {
	return self.value.(time.Time)
}

func (self *TLV) Uint32() uint32 {
	return self.value.(uint32)
}

// VLN / byte[] bug
func (self *TLV) Uint64() uint64 {
	if u64, ok := toUint64(self.value); ok {
		return u64
	}
	return self.value.(uint64)
}

type FindByTager interface {
	FindByTag(Tag) *TLV
}

func (self *TLV) FindByTag(tag Tag) *TLV {
	if self.Tag == tag {
		return self
	}
	if self.Kind == DataKindSTLV {
		for _, child := range self.Children() {
			if t := child.FindByTag(tag); t != nil {
				return t
			}
		}
	}
	return nil
}

func isSpace(r rune) bool { return r == ' ' }

func toDt(v interface{}) interface{} {
	if dt, ok := v.(time.Time); ok {
		return dt
	} else if dt, ok := v.(*time.Time); ok {
		if dt == nil {
			return time.Time{}
		}
		return *dt
	} else if s, ok := v.(string); ok {
		if dt, err := time.Parse(time.RFC3339, s); err == nil {
			return dt
		} else {
			return fmt.Errorf("toDt v=%s err=%v", s, err)
		}
	} else if n, ok := toUint64(v); ok {
		return time.Unix(int64(n), 0)
	}
	return fmt.Errorf("toDt v=%q", v)
}

func toString(v interface{}) interface{} {
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	if n, ok := toUint64(v); ok {
		return fmt.Sprint(n)
	}
	return fmt.Errorf("toString v=%q", v)
}

func toUint64(v interface{}) (uint64, bool) {
	switch n := v.(type) {
	case int32:
		return uint64(n), true
	case uint32:
		return uint64(n), true
	case int:
		return uint64(n), true
	case uint:
		return uint64(n), true
	case int64:
		return uint64(n), true
	case uint64:
		return uint64(n), true
	default:
		return 0, false
	}
}

func toVLN(v interface{}, length uint16) interface{} {
	var u uint64
	// TODO case string: strconv.ParseInt
	if u64, ok := toUint64(v); ok {
		u = u64
	} else {
		return fmt.Errorf("toVLN v=%q", v)
	}
	if length <= 6 {
		return uint32(u)
	}
	return u
}
