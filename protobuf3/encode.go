// Go support for Protocol Buffers - Google's data interchange format
//
// Copyright 2016 Mist Systems. All rights reserved.
//
// This code is derived from earlier code which was itself:
//
// Copyright 2010 The Go Authors.  All rights reserved.
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

package protobuf3

/*
 * Routines for encoding data into the wire format for protocol buffers.
 */

import (
	"errors"
	"fmt"
	"reflect"
	"time"
	"unsafe"
)

var (
	// errRepeatedHasNil is the error returned if Marshal is called with
	// a struct with a repeated field containing a nil element.
	errRepeatedHasNil = errors.New("protobuf3: repeated field has nil element")

	// ErrNil is the error returned if Marshal is called with nil.
	ErrNil = errors.New("protobuf3: [Un]Marshal called with nil")

	ErrNotPointerToStruct = errors.New("protobuf3: Unmarshal called with argument which is not a pointer to a struct")
)

// The fundamental encoders that put bytes on the wire.
// Those that take integer types all accept uint64 and are
// therefore of type valueEncoder.

// EncodeVarint writes a varint-encoded integer to the Buffer.
// This is the format for the
// int32, int64, uint32, uint64, bool, and enum
// protocol buffer types.
func (p *WriteBuffer) EncodeVarint(x uint64) {
	x32 := uint32(x)
	if x>>32 == 0 {
		// use 32-bit math. this is measureably faster on 32-bit targets
		// probably because the >>7 on a uint64 is messy
		if x32 < 1<<7 { // very common case of small positive ints
			p.buf = append(p.buf, uint8(x32))
			return
		}
		if x < 1<<14 {
			p.buf = append(p.buf, uint8(x32)|0x80, uint8(x32>>7))
			return
		}
		// we know x takes at least 3 bytes to encode, so we can lay down
		// the first two immediately
		p.buf = append(p.buf, uint8(x32)|0x80, uint8(x32>>7)|0x80)
		x32 >>= 14
		for x32 >= 1<<7 {
			p.buf = append(p.buf, uint8(x32)|0x80)
			x32 >>= 7
		}
		p.buf = append(p.buf, uint8(x32))
	} else {
		// we know x takes at least 5 bytes to encode (since it is >= 1<<32)
		// so we can lay down the first 4 bytes immediately
		p.buf = append(p.buf, uint8(x32)|0x80, uint8(x32>>7)|0x80, uint8(x32>>14)|0x80, uint8(x32>>21)|0x80)
		x >>= 28
		for x >= 1<<7 {
			p.buf = append(p.buf, uint8(x)|0x80)
			x >>= 7
		}
		p.buf = append(p.buf, uint8(x))
	}
}

// SizeVarint returns the varint encoding size of an integer.
func SizeVarint(x uint64) (n int) {
	if x>>32 == 0 {
		// use 32-bit math. this is measureably faster on 32-bit targets
		// probably because the >>7 on a uint64 is messy
		x32 := uint32(x)
		for {
			n++
			x32 >>= 7
			if x32 == 0 {
				break
			}
		}
	} else {
		for {
			n++
			x >>= 7
			if x == 0 {
				break
			}
		}
	}
	return n
}

// EncodeFixed64 writes a 64-bit integer to the Buffer.
// This is the format for the
// fixed64, sfixed64, and double protocol buffer types.
func (p *WriteBuffer) EncodeFixed64(x uint64) {
	p.buf = append(p.buf,
		uint8(x),
		uint8(x>>8),
		uint8(x>>16),
		uint8(x>>24),
		uint8(x>>32),
		uint8(x>>40),
		uint8(x>>48),
		uint8(x>>56))
}

// EncodeFixed32 writes a 32-bit integer to the Buffer.
// This is the format for the
// fixed32, sfixed32, and float protocol buffer types.
func (p *WriteBuffer) EncodeFixed32(x uint64) {
	p.buf = append(p.buf,
		uint8(x),
		uint8(x>>8),
		uint8(x>>16),
		uint8(x>>24))
}

// EncodeZigzag64 writes a zigzag-encoded 64-bit integer
// to the Buffer.
// This is the format used for the sint64 protocol buffer type.
func (p *WriteBuffer) EncodeZigzag64(x uint64) {
	// use signed number to get arithmetic right shift.
	p.EncodeVarint(uint64((x << 1) ^ uint64((int64(x) >> 63))))
}

// EncodeZigzag32 writes a zigzag-encoded 32-bit integer
// to the Buffer.
// This is the format used for the sint32 protocol buffer type.
func (p *WriteBuffer) EncodeZigzag32(x uint64) {
	// use signed number to get arithmetic right shift.
	p.EncodeVarint(uint64((uint32(x) << 1) ^ uint32((int32(x) >> 31))))
}

// EncodeBytes writes a bytes tag and count-delimited byte slice to the Buffer.
// This is equivalent to encoding a 'b []byte `protobuf:"bytes,tag"` field.
func (p *WriteBuffer) EncodeBytes(tag uint32, b []byte) {
	p.EncodeVarint(uint64(tag)<<3 + uint64(WireBytes))
	p.EncodeRawBytes(b)
}

// EncodeRawBytes writes a count-delimited byte buffer to the Buffer.
// This is the format used for the bytes protocol buffer
// type and for embedded messages.
func (p *WriteBuffer) EncodeRawBytes(b []byte) {
	p.EncodeVarint(uint64(len(b)))
	p.buf = append(p.buf, b...)
}

// EncodeStringBytes writes an encoded string to the Buffer.
// This is the format used for the proto2 string type.
func (p *WriteBuffer) EncodeStringBytes(s string) {
	p.EncodeVarint(uint64(len(s)))
	p.buf = append(p.buf, s...)
}

// Marshaler is the interface implemented by types that can marshal and unmarshal themselves.
// (note this is a single interface because dealing with types which only implement half the
// operations creates too many edge cases, and so far I haven't had any cases where I didn't
// have both a custom marshal and a custom unmarshal function)
type Marshaler interface {
	MarshalProtobuf3() ([]byte, error)
	unmarshaler
}

// Appender is the interface implemented by types that can marshal themselves directly into
// a protobuf3.Buffer. This can be more efficient than Marshaler.MarshalProtobuf3() because
// it may not require a temporary []byte.
type Appender interface {
	// AppendProtobuf3 should encode the Appender in protobuf, appending it to the argument
	// byte slice, and returning the result. the argument might be nil, or it might have no
	// capacity, but the hope is it will be a useful temporary buffer. AppendProtobuf3 does
	// not append the Appender's tag, wirebyte, nor the length in the case of WireBytes.
	// AppendProtobuf3 should append what MarshalProtobuf3 would have returned, but hopefully
	// do so more efficiently than that.
	AppendProtobuf3([]byte) ([]byte, error)
	unmarshaler
}

// Unmarshaler is the interface implemented by types that can unmarshal themselves.
// It's not that useful on its own so it's not exported.
type unmarshaler interface {
	// UnmarshalProtobuf3 decodes the protobuf, reversing what MarshalProtobuf3 or AppendProtobuf3 produced.
	// The argument does not include the length of a WireBytes.
	UnmarshalProtobuf3([]byte) error
}

// Marshal takes the protocol buffer
// and encodes it into the wire format, returning the data.
func Marshal(pb Message) ([]byte, error) {
	buf := newBuffer(nil)
	err := buf.Marshal(pb)
	bytes := buf.release()
	if err != nil {
		return nil, err
	}
	return bytes, nil
}

// Marshal takes the protocol buffer
// and encodes it into the wire format, writing the result to the
// Buffer.
func (o *Buffer) Marshal(pb Message) error {
	// Can it marshal itself?
	// (note: we don't ask Appenders to marshal themselves b/c there's a problem handling WireBytes and removing the prepended length)
	if m, ok := pb.(Marshaler); ok {
		data, err := m.MarshalProtobuf3()
		if err != nil {
			o.noteError(err)
		}
		// the entire message marshaled itself without us
		o.buf = append(o.buf, data...)
		return o.err
	}

	// unpack the interface and sanity check
	if pb == nil {
		return ErrNil // don't pass in nil interfaces. we need types
	}
	v := reflect.ValueOf(pb)
	t := v.Type()
	if t.Kind() != reflect.Ptr || t.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("protobuf3: can't Marshal(%s): not a *struct type", t)
	}
	base := unsafe.Pointer(v.Pointer())
	if base == nil {
		return ErrNil // don't pass in nil pointers. we need values
	}

	prop, err := GetProperties(t.Elem())
	if err != nil {
		return err
	}

	o.enc_struct(prop, base)
	return o.err
}

// Individual type encoders.

// Encode a *bool.
func (o *Buffer) enc_ptr_bool(p *Properties, base unsafe.Pointer) {
	v := *(**bool)(unsafe.Pointer(uintptr(base) + p.offset))
	if v == nil {
		return
	}
	x := 0
	if *v {
		x = 1
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(x))
}

// Encode a bool.
func (o *Buffer) enc_bool(p *Properties, base unsafe.Pointer) {
	v := *(*bool)(unsafe.Pointer(uintptr(base) + p.offset))
	if !v {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, 1)
}

// Encode an *int
func (o *Buffer) enc_ptr_int(p *Properties, base unsafe.Pointer) {
	v := *(**int)(unsafe.Pointer(uintptr(base) + p.offset))
	if v == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(*v))
}

// Encode an int
func (o *Buffer) enc_int(p *Properties, base unsafe.Pointer) {
	x := *(*int)(unsafe.Pointer(uintptr(base) + p.offset))
	if x == 0 {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(x))
}

// Encode an *uint
func (o *Buffer) enc_ptr_uint(p *Properties, base unsafe.Pointer) {
	v := *(**uint)(unsafe.Pointer(uintptr(base) + p.offset))
	if v == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(*v))
}

// Encode a uint
func (o *Buffer) enc_uint(p *Properties, base unsafe.Pointer) {
	x := *(*uint)(unsafe.Pointer(uintptr(base) + p.offset))
	if x == 0 {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(x))
}

// Encode an *int8.
func (o *Buffer) enc_ptr_int8(p *Properties, base unsafe.Pointer) {
	v := *(**int8)(unsafe.Pointer(uintptr(base) + p.offset))
	if v == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(*v))
}

// Encode an int8
func (o *Buffer) enc_int8(p *Properties, base unsafe.Pointer) {
	x := *(*int8)(unsafe.Pointer(uintptr(base) + p.offset))
	if x == 0 {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(x))
}

// Encode a *uint8.
// Exactly the same as int8, except for no sign extension.
func (o *Buffer) enc_ptr_uint8(p *Properties, base unsafe.Pointer) {
	v := *(**uint8)(unsafe.Pointer(uintptr(base) + p.offset))
	if v == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(*v))
}

// Encode a uint8
func (o *Buffer) enc_uint8(p *Properties, base unsafe.Pointer) {
	x := *(*uint8)(unsafe.Pointer(uintptr(base) + p.offset))
	if x == 0 {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(x))
}

// Encode an *int16.
func (o *Buffer) enc_ptr_int16(p *Properties, base unsafe.Pointer) {
	v := *(**int16)(unsafe.Pointer(uintptr(base) + p.offset))
	if v == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(*v))
}

// Encode an int16
func (o *Buffer) enc_int16(p *Properties, base unsafe.Pointer) {
	x := *(*int16)(unsafe.Pointer(uintptr(base) + p.offset))
	if x == 0 {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(x))
}

// Encode a *uint16.
// Exactly the same as int16, except for no sign extension.
func (o *Buffer) enc_ptr_uint16(p *Properties, base unsafe.Pointer) {
	v := *(**uint16)(unsafe.Pointer(uintptr(base) + p.offset))
	if v == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(*v))
}

// Encode a uint16
func (o *Buffer) enc_uint16(p *Properties, base unsafe.Pointer) {
	x := *(*uint16)(unsafe.Pointer(uintptr(base) + p.offset))
	if x == 0 {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(x))
}

// Encode an *int32.
func (o *Buffer) enc_ptr_int32(p *Properties, base unsafe.Pointer) {
	v := *(**int32)(unsafe.Pointer(uintptr(base) + p.offset))
	if v == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(*v))
}

// Encode an int32.
func (o *Buffer) enc_int32(p *Properties, base unsafe.Pointer) {
	x := *(*int32)(unsafe.Pointer(uintptr(base) + p.offset))
	if x == 0 {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(x))
}

// Encode a *uint32.
// Exactly the same as int32, except for no sign extension.
func (o *Buffer) enc_ptr_uint32(p *Properties, base unsafe.Pointer) {
	v := *(**uint32)(unsafe.Pointer(uintptr(base) + p.offset))
	if v == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(*v))
}

// Encode a uint32.
func (o *Buffer) enc_uint32(p *Properties, base unsafe.Pointer) {
	x := *(*uint32)(unsafe.Pointer(uintptr(base) + p.offset))
	if x == 0 {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, uint64(x))
}

// Encode an *int64.
func (o *Buffer) enc_ptr_int64(p *Properties, base unsafe.Pointer) {
	v := *(**uint64)(unsafe.Pointer(uintptr(base) + p.offset))
	if v == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, *v)
}

// Encode an int64.
func (o *Buffer) enc_int64(p *Properties, base unsafe.Pointer) {
	x := *(*uint64)(unsafe.Pointer(uintptr(base) + p.offset))
	if x == 0 {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	p.valEnc(o, x)
}

// Encode a *string.
func (o *Buffer) enc_ptr_string(p *Properties, base unsafe.Pointer) {
	v := *(**string)(unsafe.Pointer(uintptr(base) + p.offset))
	if v == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	o.EncodeStringBytes(*v)
}

// Encode a string.
func (o *Buffer) enc_string(p *Properties, base unsafe.Pointer) {
	x := *(*string)(unsafe.Pointer(uintptr(base) + p.offset))
	if x == "" {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	o.EncodeStringBytes(x)
}

// Encode an message struct field which implements the Marshaler interface
func (o *Buffer) enc_marshaler(p *Properties, base unsafe.Pointer) {
	ptr := (unsafe.Pointer(uintptr(base) + p.offset))
	// note *ptr is embedded in base, so pointer cannot be nil

	m := reflect.NewAt(p.stype, ptr).Interface().(Marshaler)
	data, err := m.MarshalProtobuf3()
	if err != nil {
		o.noteError(err)
		return
	}
	if data == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	if p.WireType == WireBytes {
		// bytes wiretype need to be prefixed by the length of the bytes array
		o.EncodeVarint(uint64(len(data)))
	} // the other wiretypes (varint, fixed, zigzag) encode the length implicitly
	o.buf = append(o.buf, data...)
}

// encode_appender appends a Appender, handling the zero value case properly.
// the returned error is noted in the buffer
func (o *Buffer) encode_appender(p *Properties, ptr unsafe.Pointer, must_encode bool) error {
	// append the tagcode. we'll remove it if, in the end, ptr marshals to nothing
	n1 := len(o.buf)
	o.buf = append(o.buf, p.tagcode...)
	if p.WireType == WireBytes {
		// add a single byte for the length. we might need more, and we'll adjust later if we do,
		// but it's the small objects which need the most performance improvement so we optimize for them
		o.buf = append(o.buf, 0)
	}
	n2 := len(o.buf)

	a := reflect.NewAt(p.stype, ptr).Interface().(Appender)
	b, err := a.AppendProtobuf3(o.buf)
	if err != nil {
		o.noteError(err)
		return err
	}
	// quick sanity check because I've already messed up and returned nil instead of the input
	if len(b) < len(o.buf) {
		err = fmt.Errorf("protobuf3: buggy %s.(%s).AppendProtobuf3 implementation returned []byte len %d", p.stype.PkgPath(), p.stype.Name(), len(b))
		o.noteError(err)
		return err
	}
	o.buf = b

	if !must_encode && len(o.buf) == n2 {
		// AppendProtobuf3 didn't append anything; it must be the zero value; remove the tagcode (and if we added it, the length placeholder byte)
		o.buf = o.buf[:n1]
		return nil
	}

	if p.WireType == WireBytes {
		// fixup the length
		n := uint64(len(o.buf) - n2)
		if n < 128 {
			// it fits in the placeholder byte we reserved (including length 0)
			o.buf[n2-1] = byte(n)
		} else {
			// move the appended data forward to make room for the byte length
			s := SizeVarint(n)
			o.buf = append(o.buf[:n2-1+s], o.buf[n2:]...)
			// temporarily rewind to where the length goes
			n3 := len(o.buf)
			o.buf = o.buf[:n2-1]
			o.EncodeVarint(n)
			// then restore to the whole buffer
			o.buf = o.buf[:n3]
		}
	}

	return nil
}

// Encode an message struct field which implements the Appender interface
func (o *Buffer) enc_appender(p *Properties, base unsafe.Pointer) {
	ptr := (unsafe.Pointer(uintptr(base) + p.offset))
	// note *ptr is embedded in base, so pointer cannot be nil

	o.encode_appender(p, ptr, false)
}

// Encode an message struct field of a message struct.
func (o *Buffer) enc_struct_message(p *Properties, base unsafe.Pointer) {
	structp := unsafe.Pointer(uintptr(base) + p.offset)
	// note struct is embedded in base, so pointer cannot be nil

	iTag := len(o.buf)
	o.buf = append(o.buf, p.tagcode...)
	iLen := len(o.buf)
	o.enc_len_struct(p.sprop, structp)

	// if the contents encoded to nothing (length = 0) then we can skip this field entirely
	if len(o.buf) == iLen+1 && o.buf[iLen] == 0 {
		o.buf = o.buf[:iTag]
	}
}

// Encode a *Marshaler.
func (o *Buffer) enc_ptr_marshaler(p *Properties, base unsafe.Pointer) {
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(uintptr(base) + p.offset))
	if ptr == nil {
		return
	}

	m := reflect.NewAt(p.stype, ptr).Interface().(Marshaler)
	data, err := m.MarshalProtobuf3()
	if err != nil {
		o.noteError(err)
		return
	}
	if data == nil {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	if p.WireType == WireBytes {
		o.EncodeVarint(uint64(len(data)))
	}
	o.buf = append(o.buf, data...)
}

// Encode an *Appender.
func (o *Buffer) enc_ptr_appender(p *Properties, base unsafe.Pointer) {
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(uintptr(base) + p.offset))
	if ptr == nil {
		return
	}

	o.encode_appender(p, ptr, false)
}

// Encode a *message struct.
func (o *Buffer) enc_ptr_struct_message(p *Properties, base unsafe.Pointer) {
	structp := *(*unsafe.Pointer)(unsafe.Pointer(uintptr(base) + p.offset))
	if structp == nil {
		// a nil pointer encodes as nothing
		return
	}

	// note: since this is pointer to a message we don't elide empty values, since they represent a pointer to a zero-value, not a nil pointer
	o.buf = append(o.buf, p.tagcode...)
	o.enc_len_struct(p.sprop, structp)
}

// Encode a slice of bools ([]bool) in packed format.
func (o *Buffer) enc_slice_packed_bool(p *Properties, base unsafe.Pointer) {
	s := *(*[]bool)(unsafe.Pointer(uintptr(base) + p.offset))
	l := len(s)
	if l == 0 {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(l)) // each bool takes exactly one byte
	for _, x := range s {
		v := uint64(0)
		if x {
			v = 1
		}
		p.valEnc(o, v)
	}
}

// Encode an array of bools ([N]bool) in packed format.
func (o *Buffer) enc_array_packed_bool(p *Properties, base unsafe.Pointer) {
	n := p.length
	s := ((*[maxLen]bool)(unsafe.Pointer(uintptr(base) + p.offset)))[0:n:n]
	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(n)) // each bool takes exactly one byte
	for _, x := range s {
		v := uint64(0)
		if x {
			v = 1
		}
		p.valEnc(o, v)
	}
}

// Encode a slice of bytes ([]byte).
func (o *Buffer) enc_slice_byte(p *Properties, base unsafe.Pointer) {
	s := *(*[]byte)(unsafe.Pointer(uintptr(base) + p.offset))
	if len(s) == 0 {
		return
	}
	o.buf = append(o.buf, p.tagcode...)
	o.EncodeRawBytes(s)
}

// Encode an array of bytes ([n]byte).
func (o *Buffer) enc_array_byte(p *Properties, base unsafe.Pointer) {
	n := p.length
	s := ((*[maxLen]byte)(unsafe.Pointer(uintptr(base) + p.offset)))[0:n:n]
	o.buf = append(o.buf, p.tagcode...)
	o.EncodeRawBytes(s)
}

// Encode a slice of int ([]int) in packed format.
func (o *Buffer) enc_slice_packed_int(p *Properties, base unsafe.Pointer) {
	s := *(*[]int)(unsafe.Pointer(uintptr(base) + p.offset))
	l := len(s)
	if l == 0 {
		return
	}
	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode a slice of uint ([]uint) in packed format.
func (o *Buffer) enc_slice_packed_uint(p *Properties, base unsafe.Pointer) {
	s := *(*[]uint)(unsafe.Pointer(uintptr(base) + p.offset))
	l := len(s)
	if l == 0 {
		return
	}
	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode a slice of int8s ([]int8) in packed format.
func (o *Buffer) enc_slice_packed_int8(p *Properties, base unsafe.Pointer) {
	s := *(*[]int8)(unsafe.Pointer(uintptr(base) + p.offset))
	l := len(s)
	if l == 0 {
		return
	}
	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode a slice of int16s ([]int16) in packed format.
func (o *Buffer) enc_slice_packed_int16(p *Properties, base unsafe.Pointer) {
	s := *(*[]int16)(unsafe.Pointer(uintptr(base) + p.offset))
	l := len(s)
	if l == 0 {
		return
	}
	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode an array of int8s ([length]int8) in packed format.
func (o *Buffer) enc_array_packed_int8(p *Properties, base unsafe.Pointer) {
	n := p.length
	s := ((*[maxLen]int8)(unsafe.Pointer(uintptr(base) + p.offset)))[0:n:n]

	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode an array of int16s ([length]int16) in packed format.
func (o *Buffer) enc_array_packed_int16(p *Properties, base unsafe.Pointer) {
	n := p.length
	s := ((*[maxLen / 2]int16)(unsafe.Pointer(uintptr(base) + p.offset)))[0:n:n]

	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode a slice of uint16s ([]uint16) in packed format.
func (o *Buffer) enc_slice_packed_uint16(p *Properties, base unsafe.Pointer) {
	s := *(*[]uint16)(unsafe.Pointer(uintptr(base) + p.offset))
	l := len(s)
	if l == 0 {
		return
	}
	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode an array of uint16s ([length]uint16) in packed format.
func (o *Buffer) enc_array_packed_uint16(p *Properties, base unsafe.Pointer) {
	n := p.length
	s := ((*[maxLen / 2]uint16)(unsafe.Pointer(uintptr(base) + p.offset)))[0:n:n]

	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode a slice of int32s ([]int32) in packed format.
func (o *Buffer) enc_slice_packed_int32(p *Properties, base unsafe.Pointer) {
	s := *(*[]int32)(unsafe.Pointer(uintptr(base) + p.offset))
	l := len(s)
	if l == 0 {
		return
	}
	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode an array of int32s ([length]int32) in packed format.
func (o *Buffer) enc_array_packed_int32(p *Properties, base unsafe.Pointer) {
	n := p.length
	s := ((*[maxLen / 4]int32)(unsafe.Pointer(uintptr(base) + p.offset)))[0:n:n]

	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode a slice of uint32s ([]uint32) in packed format.
// Exactly the same as int32, except for no sign extension.
func (o *Buffer) enc_slice_packed_uint32(p *Properties, base unsafe.Pointer) {
	s := *(*[]uint32)(unsafe.Pointer(uintptr(base) + p.offset))
	l := len(s)
	if l == 0 {
		return
	}
	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode an array of uint32s ([length]uint32) in packed format.
func (o *Buffer) enc_array_packed_uint32(p *Properties, base unsafe.Pointer) {
	n := p.length
	s := ((*[maxLen / 4]uint32)(unsafe.Pointer(uintptr(base) + p.offset)))[0:n:n]

	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, uint64(x))
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode a slice of int64s or uint64s ([](u)int64) in packed format.
func (o *Buffer) enc_slice_packed_int64(p *Properties, base unsafe.Pointer) {
	s := *(*[]uint64)(unsafe.Pointer(uintptr(base) + p.offset))
	l := len(s)
	if l == 0 {
		return
	}
	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, x)
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode an array of int64s ([n]int64) in packed format.
func (o *Buffer) enc_array_packed_int64(p *Properties, base unsafe.Pointer) {
	n := p.length
	s := ((*[maxLen / 8]uint64)(unsafe.Pointer(uintptr(base) + p.offset)))[0:n:n]

	buf := newBuffer(nil)
	for _, x := range s {
		p.valEnc(buf, x)
	}

	o.buf = append(o.buf, p.tagcode...)
	o.EncodeVarint(uint64(len(buf.buf)))
	o.buf = append(o.buf, buf.buf...)
	buf.release()
}

// Encode a slice of slice of bytes ([][]byte).
func (o *Buffer) enc_slice_slice_byte(p *Properties, base unsafe.Pointer) {
	ss := *(*[][]byte)(unsafe.Pointer(uintptr(base) + p.offset))
	for _, s := range ss {
		o.buf = append(o.buf, p.tagcode...)
		o.EncodeRawBytes(s)
	}
}

// Encode a slice of strings ([]string).
func (o *Buffer) enc_slice_string(p *Properties, base unsafe.Pointer) {
	ss := *(*[]string)(unsafe.Pointer(uintptr(base) + p.offset))
	for _, x := range ss {
		o.buf = append(o.buf, p.tagcode...)
		o.EncodeStringBytes(x)
	}
}

// Encode an array of strings ([n]string).
func (o *Buffer) enc_array_string(p *Properties, base unsafe.Pointer) {
	n := p.length
	s := ((*[maxLen / 8 / 2]string)(unsafe.Pointer(uintptr(base) + p.offset)))[0:n:n]

	for _, x := range s {
		o.buf = append(o.buf, p.tagcode...)
		o.EncodeStringBytes(x)
	}
}

// Encode a slice of *message structs ([]*struct).
func (o *Buffer) enc_slice_ptr_struct_message(p *Properties, base unsafe.Pointer) {
	s := *(*[]unsafe.Pointer)(unsafe.Pointer(uintptr(base) + p.offset))

	// Can the object marshal itself?
	if p.isAppender {
		for _, structp := range s {
			if structp == nil {
				o.noteError(errRepeatedHasNil)
				return
			}

			if o.encode_appender(p, structp, true) != nil {
				// err is already noted
				return
			}
		}
		return
	}
	if p.isMarshaler {
		for _, structp := range s {
			if structp == nil {
				o.noteError(errRepeatedHasNil)
				return
			}

			m := reflect.NewAt(p.stype, unsafe.Pointer(structp)).Interface().(Marshaler)
			data, err := m.MarshalProtobuf3()
			if err != nil {
				o.noteError(err)
				return
			}
			// note in a slice we always encode the data, even if it is nil, in order to preserve indexing of the slice
			o.buf = append(o.buf, p.tagcode...)
			if p.WireType == WireBytes {
				o.EncodeVarint(uint64(len(data)))
			}
			o.buf = append(o.buf, data...)
		}
		return
	}

	for _, structp := range s {
		if structp == nil {
			o.noteError(errRepeatedHasNil)
			return
		}

		// note: since this is an element of a slice we don't elide empty values, since they still serve to occupy a position in the slice
		o.buf = append(o.buf, p.tagcode...)
		o.enc_len_struct(p.sprop, structp)
	}
}

// Encode an array of *message structs ([n]*struct).
func (o *Buffer) enc_array_ptr_struct_message(p *Properties, base unsafe.Pointer) {
	n := p.length
	s := ((*[maxLen / 8]unsafe.Pointer)(unsafe.Pointer(uintptr(base) + p.offset)))[0:n:n]

	// Can the object marshal itself?
	if p.isAppender {
		for _, structp := range s {
			if structp == nil {
				o.noteError(errRepeatedHasNil)
				return
			}

			if o.encode_appender(p, structp, true) != nil {
				// err is already noted
				return
			}
		}
		return
	}
	if p.isMarshaler {
		for _, structp := range s {
			if structp == nil {
				o.noteError(errRepeatedHasNil)
				return
			}

			m := reflect.NewAt(p.stype, unsafe.Pointer(structp)).Interface().(Marshaler)
			data, err := m.MarshalProtobuf3()
			if err != nil {
				o.noteError(err)
				return
			}
			// note in an array we always encode the data, even if it is nil, in order to preserve indexing of the array
			o.buf = append(o.buf, p.tagcode...)
			if p.WireType == WireBytes {
				o.EncodeVarint(uint64(len(data)))
			}
			o.buf = append(o.buf, data...)
		}
		return
	}

	for _, structp := range s {
		if structp == nil {
			o.noteError(errRepeatedHasNil)
			return
		}

		// note: since this is an element of a slice we don't elide empty values, since they still serve to occupy a position in the slice
		o.buf = append(o.buf, p.tagcode...)
		o.enc_len_struct(p.sprop, structp)
	}
}

// Encode a slice of message structs ([]struct).
func (o *Buffer) enc_slice_struct_message(p *Properties, base unsafe.Pointer) {
	s := *(*[]byte)(unsafe.Pointer(uintptr(base) + p.offset)) // note this could just as well be (*[]int) or anything
	n := ulen(s)                                              // note this is the # of elements, not the # of bytes, because of the way a Slice is built in the runtime (go1.7) as { start *T, len, cap int }
	if n == 0 {
		// no elements to encode. we have to treat this as a special case because &s[0] would cause a panic since it would be returning a pointer to something past the end of the underlying array
		return
	}
	enc_struct_messages(o, p, unsafe.Pointer(&s[0]), n)
}

// Encode a slice of Marshalers ([]T, where T implements Marshaler)
func (o *Buffer) enc_slice_marshaler(p *Properties, base unsafe.Pointer) {
	s := *(*[]byte)(unsafe.Pointer(uintptr(base) + p.offset)) // note this could just as well be (*[]int) or anything
	n := len(s)                                               // note this is the # of elements, not the # of bytes, because of the way a Slice is built in the runtime (go1.7) as { start *T, len, cap int }
	if n == 0 {
		// no elements to encode. we have to treat this as a special case because &s[0] would cause a panic since it would be returning a pointer to something past the end of the underlying array
		return
	}

	base = unsafe.Pointer(&s[0])
	sz := p.stype.Size()  // size of one struct
	nb := uintptr(n) * sz // # of bytes used by the array of structs

	// the slice's elements marshal themselves
	for i := uintptr(0); i < nb; i += sz {
		structp := unsafe.Pointer(uintptr(base) + i)

		m := reflect.NewAt(p.stype, structp).Interface().(Marshaler)
		data, err := m.MarshalProtobuf3()
		if err != nil {
			o.noteError(err)
			return
		}
		// note in a slice we always encode the data, even if it is nil, in order to preserve indexing of the slice
		o.buf = append(o.buf, p.tagcode...)
		if p.WireType == WireBytes {
			o.EncodeVarint(uint64(len(data)))
		}
		o.buf = append(o.buf, data...)
	}
}

// Encode a slice of Appenders ([]T, where T implements Appender)
func (o *Buffer) enc_slice_appender(p *Properties, base unsafe.Pointer) {
	s := *(*[]byte)(unsafe.Pointer(uintptr(base) + p.offset)) // note this could just as well be (*[]int) or anything
	n := len(s)                                               // note this is the # of elements, not the # of bytes, because of the way a Slice is built in the runtime (go1.7) as { start *T, len, cap int }
	if n == 0 {
		// no elements to encode. we have to treat this as a special case because &s[0] would cause a panic since it would be returning a pointer to something past the end of the underlying array
		return
	}

	base = unsafe.Pointer(&s[0])
	sz := p.stype.Size()  // size of one struct
	nb := uintptr(n) * sz // # of bytes used by the array of structs

	// the slice's elements marshal themselves
	for i := uintptr(0); i < nb; i += sz {
		structp := unsafe.Pointer(uintptr(base) + i)

		// note in a slice we always encode the data, even if it is nil, in order to preserve indexing of the slice
		o.encode_appender(p, structp, true)
	}
}

// Encode an array of Marshalers ([N]T, where T implements Marshaler)
func (o *Buffer) enc_array_marshaler(p *Properties, base unsafe.Pointer) {
	enc_struct_messages(o, p, unsafe.Pointer(uintptr(base)+p.offset), p.length)
}

// Encode an array of Appenders ([N]T, where T implements Appender)
func (o *Buffer) enc_array_appender(p *Properties, base unsafe.Pointer) {
	enc_struct_messages(o, p, unsafe.Pointer(uintptr(base)+p.offset), p.length)
}

// utility function to encode a series of 'n' struct messages in a line in memory (from a slice or from an array)
func enc_struct_messages(o *Buffer, p *Properties, base unsafe.Pointer, n uint) {
	sz := p.stype.Size()  // size of one struct
	nb := uintptr(n) * sz // # of bytes used by the array of structs

	// Can the object marshal itself?
	if p.isAppender {
		for i := uintptr(0); i < nb; i += sz {
			structp := unsafe.Pointer(uintptr(base) + i)

			if o.encode_appender(p, structp, true) != nil {
				// err is already noted
				return
			}
		}
		return
	}
	if p.isMarshaler {
		for i := uintptr(0); i < nb; i += sz {
			structp := unsafe.Pointer(uintptr(base) + i)

			m := reflect.NewAt(p.stype, structp).Interface().(Marshaler)
			data, err := m.MarshalProtobuf3()
			if err != nil {
				o.noteError(err)
				return
			}
			// note in a slice we always encode the data, even if it is nil, in order to preserve indexing of the slice
			o.buf = append(o.buf, p.tagcode...)
			if p.WireType == WireBytes {
				o.EncodeVarint(uint64(len(data)))
			}
			o.buf = append(o.buf, data...)
		}
		return
	}

	for i := uintptr(0); i < nb; i += sz {
		structp := unsafe.Pointer(uintptr(base) + i)

		// note: since this is an element of a slice we don't elide empty values, since they still serve to occupy a position in the slice
		o.buf = append(o.buf, p.tagcode...)
		o.enc_len_struct(p.sprop, structp)
	}
}

// Encode an array of message structs ([n]struct).
func (o *Buffer) enc_array_struct_message(p *Properties, base unsafe.Pointer) {
	enc_struct_messages(o, p, unsafe.Pointer(uintptr(base)+p.offset), p.length)
}

// Encode a map field.
func (o *Buffer) enc_new_map(p *Properties, base unsafe.Pointer) {
	/*
		A map defined as
			map<key_type, value_type> map_field = N;
		is encoded in the same way as
			message MapFieldEntry {
				key_type key = 1;
				value_type value = 2;
			}
			repeated MapFieldEntry map_field = N;
	*/

	v := reflect.NewAt(p.mtype, unsafe.Pointer(uintptr(base)+p.offset)).Elem() // map[K]V
	if v.Len() == 0 {
		return
	}

	keycopy, valcopy, keybase, valbase := mapEncodeScratch(p.mtype)

	enc := func() {
		p.mkeyprop.enc(o, p.mkeyprop, keybase)
		p.mvalprop.enc(o, p.mvalprop, valbase)
	}

	// Don't sort map keys. It is not required by the spec, and C++ doesn't do it.
	for _, key := range v.MapKeys() {
		val := v.MapIndex(key)

		keycopy.Set(key)
		valcopy.Set(val)

		o.buf = append(o.buf, p.tagcode...)
		o.enc_len_thing(enc)
	}
}

// mapEncodeScratch returns a new reflect.Value matching the map's value type,
// and a unsafe.Pointer suitable for passing to an encoder or sizer.
func mapEncodeScratch(mapType reflect.Type) (keycopy, valcopy reflect.Value, keybase, valbase unsafe.Pointer) {
	// Prepare addressable doubly-indirect placeholders for the key and value types.
	// This is needed because the element-type encoders expect **T, but the map iteration produces T.

	keyptr := reflect.New(mapType.Key())           // *K
	keycopy = keyptr.Elem()                        // addressable K
	keybase = unsafe.Pointer(keycopy.UnsafeAddr()) // *K

	valptr := reflect.New(mapType.Elem())          // *V
	valcopy = valptr.Elem()                        // addressable V
	valbase = unsafe.Pointer(valcopy.UnsafeAddr()) // *V

	return
}

// Encode a struct.
func (o *Buffer) enc_struct(prop *StructProperties, base unsafe.Pointer) {
	// Encode fields in tag order so that decoders may use optimizations
	// that depend on the ordering.
	// https://developers.google.com/protocol-buffers/docs/encoding#order
	for i := range prop.props {
		p := &prop.props[i]
		p.enc(o, p, base)
	}
}

var zeroes [20]byte // longer than any conceivable SizeVarint

// Encode a struct, preceded by its encoded length (as a varint).
func (o *Buffer) enc_len_struct(prop *StructProperties, base unsafe.Pointer) {
	o.enc_len_thing(func() { o.enc_struct(prop, base) })
}

// Encode something, preceded by its encoded length (as a varint).
func (o *Buffer) enc_len_thing(enc func()) {
	iLen := len(o.buf)
	o.buf = append(o.buf, 0, 0, 0, 0) // reserve four bytes for length
	iMsg := len(o.buf)
	enc()
	lMsg := len(o.buf) - iMsg
	lLen := SizeVarint(uint64(lMsg))
	switch x := lLen - (iMsg - iLen); {
	case x > 0: // actual length is x bytes larger than the space we reserved
		// Move msg x bytes right.
		o.buf = append(o.buf, zeroes[:x]...)
		copy(o.buf[iMsg+x:], o.buf[iMsg:iMsg+lMsg])
	case x < 0: // actual length is x bytes smaller than the space we reserved
		// Move msg x bytes left.
		copy(o.buf[iMsg+x:], o.buf[iMsg:iMsg+lMsg])
		o.buf = o.buf[:len(o.buf)+x] // x is negative
	}
	// Encode the length in the reserved space.
	o.buf = o.buf[:iLen]
	o.EncodeVarint(uint64(lMsg))
	o.buf = o.buf[:len(o.buf)+lMsg]
}

// dummy no-op encoder used for encoding 0-length array types
func (o *Buffer) enc_nothing(p *Properties, base unsafe.Pointer) {
}

// custom encoder for time.Time, encoding it into the protobuf3 standard Timestamp
func (o *WriteBuffer) enc_time_Time(p *Properties, base unsafe.Pointer) {
	ts := *(*time.Time)(unsafe.Pointer(uintptr(base) + p.offset))
	o.EncodeTimestamp(ts)
}

// EncodeTimestamp marshals a time.Time as a google.protobuf.Timestamp, which is a pair of varints (secs,nanos) tagged 1 and 2
func (o *WriteBuffer) EncodeTimestamp(ts time.Time) {
	// protobuf Timestamp uses its own encoding, different from time.Time
	// we have to convert.
	// don't blame me, the algo comes from ptypes/timestamp.go
	secs := ts.Unix()
	nanos := int32(ts.Sub(time.Unix(secs, 0))) // abuses the implementation detail that time.Duration is in nanoseconds

	o.buf = append(o.buf, 1<<3|byte(WireVarint))
	o.EncodeVarint(uint64(secs))
	o.buf = append(o.buf, 2<<3|byte(WireVarint))
	o.EncodeVarint(uint64(nanos))
}

// EncodeNSecTimestamp marshals a int64 nanosecond unix timestamp as a google.protobuf.Timestamp, which is a pair of varints (secs,nanos) tagged 1 and 2
// (this is more performant than converting nanosconds to time.Time and calling EncodeTimestamp(), but the result is identical)
func (o *WriteBuffer) EncodeNSecTimestamp(ts int64) {
	secs := ts / 1000_000_000
	nanos := int32(ts - secs*1000_000_000)

	o.buf = append(o.buf, 1<<3|byte(WireVarint))
	o.EncodeVarint(uint64(secs))
	o.buf = append(o.buf, 2<<3|byte(WireVarint))
	o.EncodeVarint(uint64(nanos))
}

// custom encoder for time.Duration, encoding it into the protobuf3 standard Duration
func (o *Buffer) enc_time_Duration(p *Properties, base unsafe.Pointer) {
	d := *(*time.Duration)(unsafe.Pointer(uintptr(base) + p.offset))
	if d != 0 {
		o.enc_Duration(p, d)
	} // else we don't have to encode the zero value
}

// helper function to encode a time.Duration value
func (o *Buffer) enc_Duration(p *Properties, d time.Duration) {
	// protobuf Duration uses its own encoding, different from time.Duration
	// we have to convert. protobuf Duration uses signed seconds and nanoseconds,
	// where seconds and nanoseconds must have the same sign or be == 0.
	//   message Duration
	//     int64 seconds = 1;
	//     int32 nanos = 2;
	//   }
	var nanos int64 = d.Nanoseconds()
	secs := nanos / 1000_000_000 // note secs ends up with the same sign as nanos, or is 0
	nanos -= secs * 1000_000_000 // note this preserves the sign of nanos (or sets it to 0)

	// furthermore go time.Duration is not a struct, but protobuf Duration is a message,
	// so we have to prepend the tag and length (we expect time.Duration to be sent as bytes,
	// as a protobuf message always is)
	o.buf = append(o.buf, p.tagcode...)
	// the byte length cannot take more than 1 byte to encode as a varint because
	// the greatest length of a protobuf Duration is two negative varint encoded uint64s,
	// and their ID bytes, or 22 bytes.
	o.buf = append(o.buf, 0) // placeholder for the length
	body_start := len(o.buf)
	if secs != 0 {
		o.buf = append(o.buf, 1<<3|byte(WireVarint))
		o.EncodeVarint(uint64(secs)) // NOTE WELL the duration.proto uses protobuf type 'int64' for seconds, not 'sint64'. So Varint is correct
	}
	if nanos != 0 {
		o.buf = append(o.buf, 2<<3|byte(WireVarint))
		o.EncodeVarint(uint64(nanos))
	}
	// go back and fill in the byte length
	o.buf[body_start-1] = uint8(len(o.buf) - body_start)
}

// custom encoder for *time.Duration, ... protobuf Duration message
func (o *Buffer) enc_ptr_time_Duration(p *Properties, base unsafe.Pointer) {
	d := *(**time.Duration)(unsafe.Pointer(uintptr(base) + p.offset))
	if d != nil && *d != 0 {
		o.enc_Duration(p, *d)
	} // else we don't have to encode a zero value
}

// custom encoder for []time.Duration, ... repeated protobuf Duration messages
func (o *Buffer) enc_slice_time_Duration(p *Properties, base unsafe.Pointer) {
	s := *(*[]time.Duration)(unsafe.Pointer(uintptr(base) + p.offset))
	for _, d := range s {
		o.enc_Duration(p, d)
	}
}

// custom encoder for [N]time.Duration, ... repeated protobuf Duration messages
func (o *Buffer) enc_array_time_Duration(p *Properties, base unsafe.Pointer) {
	n := p.length
	s := ((*[maxLen / 8]time.Duration)(unsafe.Pointer(uintptr(base) + p.offset)))[0:n:n]
	for _, d := range s {
		o.enc_Duration(p, d)
	}
}
