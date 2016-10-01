// Go support for Protocol Buffers - Google's data interchange format
//
// Copyright 2014 The Go Authors.  All rights reserved.
// https://github.com/golang/protobuf
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are
// met:
//
//     * Redistributions of source code must retain the above copyright
// notice, this list of conditions and the following disclaimer.
//     * Redistributions in binary form must reproduce the above
// copyright notice, this list of conditions and the following disclaimer
// in the documentation and/or other materials provided with the
// distribution.
//     * Neither the name of Google Inc. nor the names of its
// contributors may be used to endorse or promote products derived from
// this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
// OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
// SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
// LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
// DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
// THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package protobuf3_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/mistsys/protobuf3/proto"
	pb "github.com/mistsys/protobuf3/proto/proto3_proto"
	"github.com/mistsys/protobuf3/protobuf3"
)

func TestProto3ZeroValues(t *testing.T) {
	tests := []struct {
		desc string
		m    proto.Message
	}{
		{"zero message", &pb.Message{}},
		{"empty bytes field", &pb.Message{Data: []byte{}}},
	}
	for _, test := range tests {
		b, err := protobuf3.Marshal(test.m)
		if err != nil {
			t.Errorf("%s: protobuf3.Marshal: %v", test.desc, err)
			continue
		}
		if len(b) > 0 {
			t.Errorf("%s: Encoding is non-empty: %q", test.desc, b)
		}
	}
}

func TestRoundTripProto3(t *testing.T) {
	m := &pb.Message{
		Name:         "David",          // (2 | 1<<3): 0x0a 0x05 "David"
		Hilarity:     pb.Message_PUNS,  // (0 | 2<<3): 0x10 0x01
		HeightInCm:   178,              // (0 | 3<<3): 0x18 0xb2 0x01
		Data:         []byte("roboto"), // (2 | 4<<3): 0x20 0x06 "roboto"
		ResultCount:  47,               // (0 | 7<<3): 0x38 0x2f
		TrueScotsman: true,             // (0 | 8<<3): 0x40 0x01
		Score:        8.1,              // (5 | 9<<3): 0x4d <8.1>

		Key: []uint64{1, 0xdeadbeef},
		Nested: &pb.Nested{
			Bunny: "Monty",
		},
	}
	t.Logf(" m: %v", m)

	b, err := protobuf3.Marshal(m)
	if err != nil {
		t.Fatalf("protobuf3.Marshal: %v", err)
	}
	t.Logf(" b: % x", b)

	// also log the correct answer
	c, err := proto.Marshal(m)
	t.Logf(" c: % x", c)

	m2 := new(pb.Message)
	if err := proto.Unmarshal(b, m2); err != nil {
		t.Fatalf("proto.Unmarshal: %v", err)
	}
	t.Logf("m2: %v", m2)

	if !proto.Equal(m, m2) {
		t.Errorf("proto.Equal returned false:\n m: %v\nm2: %v", m, m2)
	}
}

//----------------------------------------------------------------------------------------------

// test message with fixed-sized encoded fields
type FixedMsg struct {
	i32 int32   `protobuf:"fixed32,1"`
	u32 uint32  `protobuf:"fixed32,2"`
	i64 int64   `protobuf:"fixed64,3"`
	u64 uint64  `protobuf:"fixed64,4"`
	f32 float32 `protobuf:"fixed32,8"`
	f64 float64 `protobuf:"fixed64,9"`

	pi32 *int32   `protobuf:"fixed32,11"`
	pu32 *uint32  `protobuf:"fixed32,12"`
	pi64 *int64   `protobuf:"fixed64,13"`
	pu64 *uint64  `protobuf:"fixed64,14"`
	pf32 *float32 `protobuf:"fixed32,18"`
	pf64 *float64 `protobuf:"fixed64,19"`

	si32 []int32   `protobuf:"fixed32,21,packed"`
	su32 []uint32  `protobuf:"fixed32,22,packed"`
	si64 []int64   `protobuf:"fixed64,23,packed"`
	su64 []uint64  `protobuf:"fixed64,24,packed"`
	sf32 []float32 `protobuf:"fixed32,28,packed"`
	sf64 []float64 `protobuf:"fixed64,29,packed"`
}

func (*FixedMsg) ProtoMessage()    {}
func (m *FixedMsg) String() string { return fmt.Sprintf("%+v", *m) }
func (m *FixedMsg) Reset()         { *m = FixedMsg{} }

// fixed size array fields (split out because regular proto.Marshal can't deal with them)
type FixedArrayMsg struct {
	ai32 [1]int32   `protobuf:"fixed32,21,packed"`
	au32 [2]uint32  `protobuf:"fixed32,22,packed"`
	ai64 [3]int64   `protobuf:"fixed64,23,packed"`
	au64 [4]uint64  `protobuf:"fixed64,24,packed"`
	af32 [5]float32 `protobuf:"fixed32,28,packed"`
	af64 [6]float64 `protobuf:"fixed64,29,packed"`
}

func (*FixedArrayMsg) ProtoMessage() {}

// test message with varint encoded fields
type VarMsg struct {
	i32 int32  `protobuf:"varint,1"`
	u32 uint32 `protobuf:"varint,2"`
	i64 int64  `protobuf:"varint,3"`
	u64 uint64 `protobuf:"varint,4"`
	b   bool   `protobuf:"varint,5"`

	pi32 *int32  `protobuf:"varint,11"`
	pu32 *uint32 `protobuf:"varint,12"`
	pi64 *int64  `protobuf:"varint,13"`
	pu64 *uint64 `protobuf:"varint,14"`
	pb   *bool   `protobuf:"varint,15"`

	si32 []int32  `protobuf:"varint,21,packed"`
	su32 []uint32 `protobuf:"varint,22,packed"`
	si64 []int64  `protobuf:"varint,23,packed"`
	su64 []uint64 `protobuf:"varint,24,packed"`
	sb   []bool   `protobuf:"varint,25,packed"`
}

func (*VarMsg) ProtoMessage()    {}
func (m *VarMsg) String() string { return fmt.Sprintf("%+v", *m) }
func (m *VarMsg) Reset()         { *m = VarMsg{} }

type VarArrayMsg struct {
	ai32 [1]int32  `protobuf:"varint,21,packed"`
	au32 [2]uint32 `protobuf:"varint,22,packed"`
	ai64 [3]int64  `protobuf:"varint,23,packed"`
	au64 [4]uint64 `protobuf:"varint,24,packed"`
	ab   [5]bool   `protobuf:"varint,25,packed"`
}

func (*VarArrayMsg) ProtoMessage() {}

// test message with zigzag encodings
type ZigZagMsg struct {
	i32 int32 `protobuf:"zigzag32,1"`
	i64 int64 `protobuf:"zigzag64,2"`

	pi32 *int32 `protobuf:"zigzag32,11"`
	pi64 *int64 `protobuf:"zigzag64,12"`

	si32 []int32 `protobuf:"zigzag32,21,packed"`
	si64 []int64 `protobuf:"zigzag64,22,packed"`
}

func (*ZigZagMsg) ProtoMessage()    {}
func (m *ZigZagMsg) String() string { return fmt.Sprintf("%+v", *m) }
func (m *ZigZagMsg) Reset()         { *m = ZigZagMsg{} }

type ZigZagArrayMsg struct {
	ai32 [1]int32 `protobuf:"zigzag32,21,packed"`
	ai64 [2]int64 `protobuf:"zigzag64,22,packed"`
}

func (*ZigZagArrayMsg) ProtoMessage() {}

// test message with bytes encoded fields
type BytesMsg struct {
	s  string   `protobuf:"bytes,1"`
	ps *string  `protobuf:"bytes,2"`
	ss []string `protobuf:"bytes,3,packed"`

	sb []byte `protobuf:"bytes,11,packed"`
}

func (*BytesMsg) ProtoMessage()    {}
func (m *BytesMsg) String() string { return fmt.Sprintf("%+v", *m) }
func (m *BytesMsg) Reset()         { *m = BytesMsg{} }

type BytesArrayMsg struct {
	sb [3]byte `protobuf:"bytes,11,packed"`
}

func (*BytesArrayMsg) ProtoMessage() {}

func TestFixedMsg(t *testing.T) {
	i32 := int32(-10)
	u32 := uint32(11)
	i64 := int64(-12)
	u64 := uint64(13)
	f32 := float32(-14.14)
	f64 := float64(15.15)

	m := FixedMsg{
		i32: -1,
		u32: 2,
		i64: -3,
		u64: 4,
		f32: -5.5,
		f64: 6.6,

		pi32: &i32,
		pu32: &u32,
		pi64: &i64,
		pu64: &u64,
		pf32: &f32,
		pf64: &f64,

		si32: []int32{-1},
		su32: []uint32{1, 2},
		si64: []int64{-1, 3, -3},
		su64: []uint64{1, 2, 3, 4},
		sf32: []float32{-1.1, 2.2, -3.3, 4.4},
		sf64: []float64{-1.1, 2.2, -3.3, 4.4},
	}

	check(&m, &m, t)
}

func TestVarMsg(t *testing.T) {
	i32 := int32(-10)
	u32 := uint32(11)
	i64 := int64(-12)
	u64 := uint64(13)

	m := VarMsg{
		i32: -1,
		u32: 2,
		i64: -3,
		u64: 4,

		pi32: &i32,
		pu32: &u32,
		pi64: &i64,
		pu64: &u64,

		si32: []int32{-1},
		su32: []uint32{1, 2},
		si64: []int64{-1, 3, -3},
		su64: []uint64{1, 2, 3, 4},
	}

	check(&m, &m, t)
}

func TestBytesMsg(t *testing.T) {
	s := "str"

	m := BytesMsg{
		s:  "test1",
		ps: &s,
		ss: []string{"test3", "test4"},
		sb: []byte{3, 2, 1, 0},
	}

	check(&m, &m, t)
}

func TestFixedArrayMsg(t *testing.T) {
	a := FixedArrayMsg{
		ai32: [1]int32{1},
		au32: [2]uint32{2, 3},
		ai64: [3]int64{4, 5, 6},
		au64: [4]uint64{8, 9, 10, 11},
		af32: [5]float32{16, 17, 18, 19, 20},
		af64: [6]float64{32, 33, 34, 35, 36, 37},
	}

	m := FixedMsg{
		si32: []int32{1},
		su32: []uint32{2, 3},
		si64: []int64{4, 5, 6},
		su64: []uint64{8, 9, 10, 11},
		sf32: []float32{16, 17, 18, 19, 20},
		sf64: []float64{32, 33, 34, 35, 36, 37},
	}

	check(&a, &m, t)
}

func TestVarArrayMsg(t *testing.T) {
	a := VarArrayMsg{
		ai32: [1]int32{1},
		au32: [2]uint32{2, 3},
		ai64: [3]int64{4, 5, 6},
		au64: [4]uint64{8, 9, 10, 11},
		ab:   [5]bool{true, false, true, false, true},
	}

	m := VarMsg{
		si32: []int32{1},
		su32: []uint32{2, 3},
		si64: []int64{4, 5, 6},
		su64: []uint64{8, 9, 10, 11},
		sb:   []bool{true, false, true, false, true},
	}

	check(&a, &m, t)
}

func TestZigZagArrayMsg(t *testing.T) {
	a := ZigZagArrayMsg{
		ai32: [1]int32{-123456789},
		ai64: [2]int64{9876543210123, 4567890987654321},
	}

	m := ZigZagMsg{
		si32: []int32{-123456789},
		si64: []int64{9876543210123, 4567890987654321},
	}

	check(&a, &m, t)
}

func TestByteArrayMsg(t *testing.T) {
	a := BytesArrayMsg{
		sb: [3]byte{0, 1, 2},
	}

	m := BytesMsg{
		sb: []byte{0, 1, 2},
	}

	check(&a, &m, t)
}

func TestZeroMsgs(t *testing.T) {
	f := FixedMsg{}
	check(&f, &f, t)

	v := VarMsg{}
	check(&v, &v, t)

	z := ZigZagMsg{}
	check(&z, &z, t)

	b := BytesMsg{}
	check(&b, &b, t)
}

// check that protobuf3.Marshal(mb) == proto.Marshal(mc)
func check(mb protobuf3.Message, mc proto.Message, t *testing.T) {
	b, err := protobuf3.Marshal(mb)
	if err != nil {
		t.Error(err)
		return
	}

	c, err := proto.Marshal(mc)
	if err != nil {
		t.Error(err)
		return
	}

	t.Logf("b = % x", b)
	t.Logf("c = % x", c)

	if !bytes.Equal(b, c) {
		t.Errorf("Marshal(%T) different", mb)
	}
}

type NestedPtrStructMsg struct {
	first  *InnerMsg   `protobuf:"bytes,1"`
	second *InnerMsg   `protobuf:"bytes,2"`
	many   []*InnerMsg `protobuf:"bytes,3"`
	more   []*InnerMsg `protobuf:"bytes,4"`
}

func (*NestedPtrStructMsg) ProtoMessage()    {}
func (m *NestedPtrStructMsg) String() string { return fmt.Sprintf("%+v", *m) }
func (m *NestedPtrStructMsg) Reset()         { *m = NestedPtrStructMsg{} }

type InnerMsg struct {
	i int32 `protobuf:"varint,2"`
}

func TestNestedPtrStructMsg(t *testing.T) {
	m := NestedPtrStructMsg{
		first:  &InnerMsg{0x11},
		second: &InnerMsg{0x22},
		many:   []*InnerMsg{&InnerMsg{0x33}, &InnerMsg{0x44}},
		more:   []*InnerMsg{},
	}

	check(&m, &m, t)
}

type NestedStructMsg struct {
	first  InnerMsg    `protobuf:"bytes,1"`
	second InnerMsg    `protobuf:"bytes,2"`
	many   []InnerMsg  `protobuf:"bytes,3"`
	more   [3]InnerMsg `protobuf:"bytes,4"`
}

func (*NestedStructMsg) ProtoMessage() {}

func TestNestedStructMsg(t *testing.T) {
	a := NestedStructMsg{
		first:  InnerMsg{0x11},
		second: InnerMsg{0x22},
		many:   []InnerMsg{InnerMsg{0x33}},
		more:   [3]InnerMsg{InnerMsg{0x44}, InnerMsg{0x55}, InnerMsg{0x66}},
	}

	m := NestedPtrStructMsg{
		first:  &InnerMsg{0x11},
		second: &InnerMsg{0x22},
		many:   []*InnerMsg{&InnerMsg{0x33}},
		more:   []*InnerMsg{&InnerMsg{0x44}, &InnerMsg{0x55}, &InnerMsg{0x66}},
	}

	check(&a, &m, t)
}

func (*InnerMsg) ProtoMessage()    {}
func (m *InnerMsg) String() string { return fmt.Sprintf("%+v", *m) }
func (m *InnerMsg) Reset()         { *m = InnerMsg{} }

type RecursiveTypeMsg struct {
	// type-recursive pointer
	self *RecursiveTypeMsg `protobuf:"bytes,1"`
	b    bool              `protobuf:"varint,22334455"`
}

func (*RecursiveTypeMsg) ProtoMessage()    {}
func (m *RecursiveTypeMsg) String() string { return fmt.Sprintf("%+v", *m) }
func (m *RecursiveTypeMsg) Reset()         { *m = RecursiveTypeMsg{} }

func TestRecursiveTypeMsg(t *testing.T) {
	m := RecursiveTypeMsg{
		self: &RecursiveTypeMsg{
			b: true,
		},
	}

	check(&m, &m, t)
}

type MapMsg struct {
	m map[string]int32 `protobuf:"bytes,1" protobuf_key:"bytes,1" protobuf_val:"varint,2"`
}

func (*MapMsg) ProtoMessage()    {}
func (m *MapMsg) String() string { return fmt.Sprintf("%+v", *m) }
func (m *MapMsg) Reset()         { *m = MapMsg{} }

func TestMapMsg(t *testing.T) {
	m := MapMsg{
		m: map[string]int32{"123": 123, "abc": 456},
	}

	check(&m, &m, t)
}
