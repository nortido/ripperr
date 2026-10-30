package tlscapture

import "slices"

type Mode uint8

const (
	ModeBuiltin Mode = iota
	ModeCustom
	ModeFlare
)

type Setting struct {
	ID  uint16
	Val uint32
}

type Priority struct {
	StreamID  uint32
	StreamDep uint32
	Exclusive bool
	Weight    uint8
}

type PriorityParam struct {
	StreamDep uint32
	Exclusive bool
	Weight    uint8
}

type Fingerprint struct {
	ClientHello       []byte
	Settings          []Setting
	ConnectionFlow    uint32
	Priorities        []Priority
	HeaderPriority    *PriorityParam
	StreamID          uint32
	PseudoHeaderOrder []string
	HeaderOrder       []string
	Headers           map[string]string
}

type Identity struct {
	UserAgent   string
	Profile     string
	Mode        Mode
	Fingerprint *Fingerprint
}

func (f *Fingerprint) Matches(o *Fingerprint) bool {
	// Приоритеты HEADERS и PRIORITY не сравниваем: fhttp подставляет свои, и профиль зря ушел бы во flare-only
	if f == nil || o == nil {
		return false
	}
	a, err := JA4(f.ClientHello)
	if err != nil {
		return false
	}
	b, err := JA4(o.ClientHello)
	if err != nil || a != b {
		return false
	}
	return slices.Equal(f.Settings, o.Settings) &&
		f.ConnectionFlow == o.ConnectionFlow &&
		slices.Equal(f.PseudoHeaderOrder, o.PseudoHeaderOrder)
}
