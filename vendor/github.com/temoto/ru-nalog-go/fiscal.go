package ru_nalog

import (
	"fmt"
	"strings"
)

// kinds of TLV.value
//go:generate stringer -type=DataKind -trimprefix=DataKind
type DataKind uint

const (
	DataKindInvalid DataKind = iota
	DataKindSTLV
	DataKindBool
	DataKindUint
	DataKindVLN
	DataKindFVLN
	DataKindTime
	DataKindString
	DataKindBytes
)

type DocType uint16

const (
	FDRegistration         DocType = 1
	FDRegChange            DocType = 11
	FDCycleOpen            DocType = 2
	FDStateReport          DocType = 21
	FDCheck                DocType = 3
	FDCorrectionCheck      DocType = 31
	FDBSO                  DocType = 4
	FDCorrectionBSO        DocType = 41
	FDCycleClose           DocType = 5
	FDStorageClose         DocType = 6
	FDOperatorConfirmation DocType = 7
)

type Doc struct {
	Number uint32 `fdn:"1040"`
	Type   DocType
	Props  TLV
}

func NewDoc(number uint32, dtype DocType) *Doc {
	d := &Doc{
		Number: number,
		Type:   dtype,
		Props:  TLV{},
	}
	d.Props.Kind = DataKindSTLV
	d.Props.value = make([]TLV, 0, 8)
	return d
}

func (d *Doc) FindByTag(tag Tag) *TLV {
	for _, child := range d.Props.Children() {
		if t := child.FindByTag(tag); t != nil {
			return t
		}
	}
	return nil
}

func (d *Doc) AppendNew(tag Tag, value interface{}) *TLV {
	if d == nil {
		return nil
	}
	return d.Props.AppendNew(tag, value)
}

func (d *Doc) String() string {
	b := strings.Builder{}
	fmt.Fprintf(&b, "Doc(#%d Type=%d Props=[", d.Number, d.Type)
	cs := d.Props.Children()
	for i := range cs {
		prop := &cs[i]
		if i != 0 {
			b.WriteString(" ")
		}
		b.WriteString(prop.GoString())
	}
	b.WriteString("])")
	return b.String()
}
