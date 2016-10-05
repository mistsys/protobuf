// Go support for Protocol Buffers - Google's data interchange format
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
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const debug bool = false

// XXXHack enables a backwards compatability hack to match the canonical golang.go/protobuf error behavior for fields whose names start with XXX_
// This isn't needed unless you are dealing with old protobuf v2 generated types like some unit tests do
var XXXHack = false

// Constants that identify the encoding of a value on the wire.
const (
	WireVarint     = WireType(0)
	WireFixed64    = WireType(1)
	WireBytes      = WireType(2)
	WireStartGroup = WireType(3)
	WireEndGroup   = WireType(4)
	WireFixed32    = WireType(5)
)

type WireType byte

// mapping from WireType to string
var wireTypeNames = []string{WireVarint: "varint", WireFixed64: "fixed64", WireBytes: "bytes", WireStartGroup: "start-group", WireEndGroup: "end-group", WireFixed32: "fixed32"}

func (wt WireType) String() string {
	if int(wt) < len(wireTypeNames) {
		return wireTypeNames[wt]
	}
	return fmt.Sprintf("WireType(%d)", byte(wt))
}

// Encoders are defined in encode.go
// An encoder outputs the full representation of a field, including its
// tag and encoder type.
type encoder func(p *Buffer, prop *Properties, base structPointer)

// A valueEncoder encodes a single integer in a particular encoding.
type valueEncoder func(o *Buffer, x uint64)

// StructProperties represents properties for all the fields of a struct.
type StructProperties struct {
	Prop  []Properties // properties for each field, indexed by reflection's field number. Fields which are not encoded in protobuf have incomplete Properties
	order []int        // list of struct field numbers in tag order, indexed 0 to N-1 by the number of fields to encode in protobuf. value indexes into .Prop[]
}

// Implement the sorting interface so we can sort the fields in tag order, as recommended by the spec.
// See encode.go, (*Buffer).enc_struct.
func (sp *StructProperties) Len() int { return len(sp.order) }
func (sp *StructProperties) Less(i, j int) bool {
	return sp.Prop[sp.order[i]].Tag < sp.Prop[sp.order[j]].Tag
}
func (sp *StructProperties) Swap(i, j int) { sp.order[i], sp.order[j] = sp.order[j], sp.order[i] }

// returns the properties into protobuf v3 format, suitable for feeding back into the protobuf compiler.
func (sp *StructProperties) asProtobuf(t reflect.Type, tname string) string {
	lines := []string{fmt.Sprintf("message %s {", tname)}
	for i := range sp.Prop {
		pp := &sp.Prop[i]
		if pp.Wire != "-" {
			lines = append(lines, fmt.Sprintf("  %s %s = %d;", pp.asProtobuf, pp.Name, pp.Tag))
		}
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n")
}

// returns the type expressed in protobuf v3 format, suitable for feeding back into the protobuf compiler.
func AsProtobuf(t reflect.Type) string {
	return GetProperties(t).asProtobuf(t, t.Name())
}

// returns the type expressed in protobuf v3 format, including all dependent types and imports
func AsProtobufFull(t reflect.Type) string {
	todo := make(map[reflect.Type]struct{})
	done := make(map[reflect.Type]struct{})

	headers := []string{`syntax = "proto3";`}

	// kick things off with the top level struct
	todo[t] = struct{}{}
	time_type := reflect.TypeOf(time.Time{})

	// and lather/rinse/repeat until we're done with all the types
	var body []string
	for len(todo) != 0 {
		for t := range todo {
			// move t from todo to done
			delete(todo, t)
			done[t] = struct{}{}

			// capture its definition
			body = append(body, AsProtobuf(t))

			// add to todo any new non-anonymous types used by its fields
			p := GetProperties(t)
			for i := range p.Prop {
				pp := &p.Prop[i]
				tt := pp.Subtype()
				if tt != nil && tt.Kind() == reflect.Struct && tt.Name() != "" {
					if _, ok := done[tt]; !ok {
						// it's a new type for us
						switch {
						case pp.isMarshaler:
							// we can't define a custom type
							body = append(body, fmt.Sprintf("// TODO insert definition of custom marshaling type %s here", tt.Name()))
							done[tt] = struct{}{} // and don't bother with the
						case tt == time_type:
							// the timestamp type gets defined by an import
							headers = append(headers, `import "timestamp.proto"`)
							done[tt] = struct{}{}
						default:
							todo[tt] = struct{}{}
						}
					}
				}
			}

			// and we must break since todo has possibly been altered
			break
		}
	}

	return strings.Join(append(headers, body...), "\n")
}

// Properties represents the protocol-specific behavior of a single struct field.
type Properties struct {
	Name       string // name of the field, for error messages
	Wire       string
	asProtobuf string // protobuf v3 type for this field (or something equivalent, since we can't figure it out perfectly from the Go field type and tags)
	Tag        uint32
	WireType   WireType

	enc         encoder
	valEnc      valueEncoder // set for bool and numeric types only
	field       field
	tagcode     []byte // encoding of EncodeVarint((Tag<<3)|WireType)
	tagbuf      [8]byte
	stype       reflect.Type      // set for struct types only
	sprop       *StructProperties // set for struct types only
	isMarshaler bool

	mtype    reflect.Type // set for map types only
	mkeyprop *Properties  // set for map types only
	mvalprop *Properties  // set for map types only

	length int // set for array types only
}

// String formats the properties in the protobuf struct field tag style.
func (p *Properties) String() string {
	if p.stype != nil {
		return fmt.Sprintf("%s %s (%s)", p.Wire, p.Name, p.stype.Name())
	}
	if p.mtype != nil {
		return fmt.Sprintf("%s %s (%s)", p.Wire, p.Name, p.mtype.Name())
	}
	return fmt.Sprintf("%s %s", p.Wire, p.Name)
}

// returns the inner type, or nil
func (p *Properties) Subtype() reflect.Type {
	return p.stype
}

// IntEncoder enumerates the different ways of encoding integers in Protobuf v3
type IntEncoder int

const (
	UnknownEncoder IntEncoder = iota // make the zero-value be different from any valid value so I can tell it is not set
	VarintEncoder
	Fixed32Encoder
	Fixed64Encoder
	Zigzag32Encoder
	Zigzag64Encoder
)

// Parse populates p by parsing a string in the protobuf struct field tag style.
func (p *Properties) Parse(s string) (IntEncoder, bool, error) {
	p.Wire = s

	// "bytes,49,rep,..."
	fields := strings.Split(s, ",")

	if len(fields) < 2 {
		if len(fields) > 0 && fields[0] == "-" {
			// `protobuf="-"` is used to mark fields which should be skipped by the protobuf encoder (this is same mark as is used by the std encoding/json package)
			return 0, true, nil
		}
		return 0, true, fmt.Errorf("protobuf3: tag of %q has too few fields: %q", p.Name, s)
	}

	var enc IntEncoder
	switch fields[0] {
	case "varint":
		p.valEnc = (*Buffer).EncodeVarint
		p.WireType = WireVarint
		enc = VarintEncoder
	case "fixed32":
		p.valEnc = (*Buffer).EncodeFixed32
		p.WireType = WireFixed32
		enc = Fixed32Encoder
	case "fixed64":
		p.valEnc = (*Buffer).EncodeFixed64
		p.WireType = WireFixed64
		enc = Fixed64Encoder
	case "zigzag32":
		p.valEnc = (*Buffer).EncodeZigzag32
		p.WireType = WireVarint
		enc = Zigzag32Encoder
	case "zigzag64":
		p.valEnc = (*Buffer).EncodeZigzag64
		p.WireType = WireVarint
		enc = Zigzag64Encoder
	case "bytes":
		// no numeric converter for non-numeric types
		p.WireType = WireBytes
	default:
		return 0, false, fmt.Errorf("protobuf3: tag of %q has unknown wire type: %q", p.Name, s)
	}

	tag, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false, fmt.Errorf("protobuf3: tag id of %q invalid: %s: %s", p.Name, s, err.Error())
	}
	if tag <= 0 { // catch any negative or 0 values
		return 0, false, fmt.Errorf("protobuf3: tag id of %q out of range: %s", p.Name, s)
	}
	p.Tag = uint32(tag)

	// and we don't care about any other fields
	// (if you don't mark slices/arrays/maps with ",rep" that's your own problem; this encoder always repeats those types)

	return enc, false, nil
}

// Initialize the fields for encoding and decoding.
func (p *Properties) setEnc(typ reflect.Type, f *reflect.StructField, int_encoder IntEncoder) {
	p.enc = nil
	wire := p.WireType

	// since so many cases need it, decode int_encoder into a  string now
	var int32_encoder_txt, uint32_encoder_txt,
		int64_encoder_txt, uint64_encoder_txt string
	switch int_encoder {
	case VarintEncoder:
		uint32_encoder_txt = "uint32"
		int32_encoder_txt = uint32_encoder_txt[1:] // strip the 'u' off
		uint64_encoder_txt = "uint64"
		int64_encoder_txt = uint64_encoder_txt[1:] // strip the 'u' off
	case Fixed32Encoder:
		int32_encoder_txt = "sfixed32"
		uint32_encoder_txt = int32_encoder_txt[1:] // strip the 's' off
	case Fixed64Encoder:
		int64_encoder_txt = "sfixed64"
		uint64_encoder_txt = int64_encoder_txt[1:] // strip the 's' off
	case Zigzag32Encoder:
		int32_encoder_txt = "sint32"
	case Zigzag64Encoder:
		int64_encoder_txt = "sint64"
	}

	switch t1 := typ; t1.Kind() {
	default:
		fmt.Fprintf(os.Stderr, "protobuf3: no coders for %s\n", t1.Name())

	// proto3 scalar types

	case reflect.Bool:
		p.enc = (*Buffer).enc_bool
		p.asProtobuf = "bool"
	case reflect.Int:
		p.enc = (*Buffer).enc_int
		p.asProtobuf = int32_encoder_txt
	case reflect.Uint:
		p.enc = (*Buffer).enc_uint
		p.asProtobuf = uint32_encoder_txt
	case reflect.Int8:
		p.enc = (*Buffer).enc_int8
		p.asProtobuf = int32_encoder_txt
	case reflect.Uint8:
		p.enc = (*Buffer).enc_uint8
		p.asProtobuf = uint32_encoder_txt
	case reflect.Int16:
		p.enc = (*Buffer).enc_int16
		p.asProtobuf = int32_encoder_txt
	case reflect.Uint16:
		p.enc = (*Buffer).enc_uint16
		p.asProtobuf = uint32_encoder_txt
	case reflect.Int32:
		p.enc = (*Buffer).enc_int32
		p.asProtobuf = int32_encoder_txt
	case reflect.Uint32:
		p.enc = (*Buffer).enc_uint32
		p.asProtobuf = uint32_encoder_txt
	case reflect.Int64:
		p.enc = (*Buffer).enc_int64
		p.asProtobuf = int64_encoder_txt
	case reflect.Uint64:
		p.enc = (*Buffer).enc_int64
		p.asProtobuf = uint64_encoder_txt
	case reflect.Float32:
		p.enc = (*Buffer).enc_uint32 // can just treat them as bits
		p.asProtobuf = "float"
	case reflect.Float64:
		p.enc = (*Buffer).enc_int64 // can just treat them as bits
		p.asProtobuf = "double"
	case reflect.String:
		p.enc = (*Buffer).enc_string
		p.asProtobuf = "string"

	case reflect.Struct:
		p.stype = t1
		p.sprop = getPropertiesLocked(t1)
		p.isMarshaler = isMarshaler(reflect.PtrTo(t1))
		if p.isMarshaler {
			p.enc = (*Buffer).enc_marshaler
		} else {
			p.enc = (*Buffer).enc_struct_message
		}
		p.asProtobuf = p.stypeAsProtobuf()

	case reflect.Ptr:
		switch t2 := t1.Elem(); t2.Kind() {
		default:
			fmt.Fprintf(os.Stderr, "protobuf3: no encoder function for %s -> %s\n", t1.Name(), t2.Name())
			break
		case reflect.Bool:
			p.enc = (*Buffer).enc_ptr_bool
			p.asProtobuf = "bool"
		case reflect.Int32:
			p.enc = (*Buffer).enc_ptr_int32
			p.asProtobuf = int32_encoder_txt
		case reflect.Uint32:
			p.enc = (*Buffer).enc_ptr_uint32
			p.asProtobuf = uint32_encoder_txt
		case reflect.Int64:
			p.enc = (*Buffer).enc_ptr_int64
			p.asProtobuf = int64_encoder_txt
		case reflect.Uint64:
			p.enc = (*Buffer).enc_ptr_int64
			p.asProtobuf = uint64_encoder_txt
		case reflect.Float32:
			p.enc = (*Buffer).enc_ptr_uint32 // can just treat them as bits
			p.asProtobuf = "float"
		case reflect.Float64:
			p.enc = (*Buffer).enc_ptr_int64 // can just treat them as bits
			p.asProtobuf = "double"
		case reflect.String:
			p.enc = (*Buffer).enc_ptr_string
			p.asProtobuf = "string"
		case reflect.Struct:
			p.stype = t2
			p.sprop = getPropertiesLocked(t2)
			p.isMarshaler = isMarshaler(t1)
			if p.isMarshaler {
				p.enc = (*Buffer).enc_ptr_marshaler
			} else {
				p.enc = (*Buffer).enc_ptr_struct_message
			}
			p.asProtobuf = p.stypeAsProtobuf()
		}

	case reflect.Slice:
		// can the slice marshal itself?
		if isMarshaler(reflect.PtrTo(typ)) {
			p.isMarshaler = true
			p.stype = typ
			p.enc = (*Buffer).enc_marshaler
			p.asProtobuf = "repeated " + p.stypeAsProtobuf()
			break
		}

		switch t2 := t1.Elem(); t2.Kind() {
		default:
			fmt.Fprintf(os.Stderr, "protobuf3: no slice oenc for %s = []%s\n", t1.Name(), t2.Name())
			break
		case reflect.Bool:
			p.enc = (*Buffer).enc_slice_packed_bool
			wire = WireBytes // packed=true is implied in protobuf v3
			p.asProtobuf = "repeated bool"
		case reflect.Int:
			p.enc = (*Buffer).enc_slice_packed_int
			wire = WireBytes // packed=true...
			p.asProtobuf = "repeated " + int32_encoder_txt
		case reflect.Uint:
			p.enc = (*Buffer).enc_slice_packed_uint
			wire = WireBytes // packed=true...
			p.asProtobuf = "repeated " + uint32_encoder_txt
		case reflect.Int8:
			p.enc = (*Buffer).enc_slice_packed_int8
			wire = WireBytes // packed=true...
			p.asProtobuf = "repeated " + int32_encoder_txt
		case reflect.Uint8:
			p.enc = (*Buffer).enc_slice_byte
			wire = WireBytes // packed=true... even for integers
			p.asProtobuf = "bytes"
		case reflect.Int16:
			p.enc = (*Buffer).enc_slice_packed_int16
			wire = WireBytes // packed=true...
			p.asProtobuf = "repeated " + int32_encoder_txt
		case reflect.Uint16:
			p.enc = (*Buffer).enc_slice_packed_uint16
			wire = WireBytes // packed=true...
			p.asProtobuf = "repeated " + uint32_encoder_txt
		case reflect.Int32:
			p.enc = (*Buffer).enc_slice_packed_int32
			wire = WireBytes // packed=true...
			p.asProtobuf = "repeated " + int32_encoder_txt
		case reflect.Uint32:
			p.enc = (*Buffer).enc_slice_packed_uint32
			wire = WireBytes // packed=true...
			p.asProtobuf = "repeated " + uint32_encoder_txt
		case reflect.Int64, reflect.Uint64:
			p.enc = (*Buffer).enc_slice_packed_int64
			wire = WireBytes // packed=true...
			p.asProtobuf = "repeated " + int64_encoder_txt
		case reflect.Float32:
			// can just treat them as bits
			p.enc = (*Buffer).enc_slice_packed_uint32
			wire = WireBytes // packed=true...
			p.asProtobuf = "repeated float"
		case reflect.Float64:
			// can just treat them as bits
			p.enc = (*Buffer).enc_slice_packed_int64
			wire = WireBytes // packed=true...
			p.asProtobuf = "repeated double"
		case reflect.String:
			p.enc = (*Buffer).enc_slice_string
			p.asProtobuf = "repeated string"
		case reflect.Struct:
			p.stype = t2
			p.sprop = getPropertiesLocked(t2)
			p.isMarshaler = isMarshaler(reflect.PtrTo(t2))
			p.enc = (*Buffer).enc_slice_struct_message
			p.asProtobuf = "repeated " + p.stypeAsProtobuf()
		case reflect.Ptr:
			switch t3 := t2.Elem(); t3.Kind() {
			default:
				fmt.Fprintf(os.Stderr, "protobuf3: no ptr oenc for %s -> %s -> %s\n", t1.Name(), t2.Name(), t3.Name())
				break
			case reflect.Struct:
				p.stype = t3
				p.sprop = getPropertiesLocked(t3)
				p.isMarshaler = isMarshaler(t2)
				p.enc = (*Buffer).enc_slice_ptr_struct_message
				p.asProtobuf = "repeated " + p.stypeAsProtobuf()
			}
		case reflect.Slice:
			switch t2.Elem().Kind() {
			default:
				fmt.Fprintf(os.Stderr, "protobuf3: no slice elem oenc for %s -> %s -> %s\n", t1.Name(), t2.Name(), t2.Elem().Name())
				break
			case reflect.Uint8:
				p.enc = (*Buffer).enc_slice_slice_byte
				p.asProtobuf = "repeated bytes"
			}
		}

	case reflect.Array:
		p.length = t1.Len()
		if p.length == 0 {
			// save checking the array length at encode-time by doing it now
			// a zero-length array will always encode as nothing
			p.enc = (*Buffer).enc_nothing
		} else {
			switch t2 := t1.Elem(); t2.Kind() {
			default:
				fmt.Fprintf(os.Stderr, "protobuf3: no array oenc for %s = %s\n", t1.Name(), t2.Name())
				break
			case reflect.Bool:
				p.enc = (*Buffer).enc_array_packed_bool
				wire = WireBytes // packed=true is implied in protobuf v3
				p.asProtobuf = "repeated bool"
			case reflect.Int32:
				p.enc = (*Buffer).enc_array_packed_int32
				wire = WireBytes // packed=true...
				p.asProtobuf = "repeated " + int32_encoder_txt
			case reflect.Uint32:
				p.enc = (*Buffer).enc_array_packed_uint32
				wire = WireBytes // packed=true...
				p.asProtobuf = "repeated " + uint32_encoder_txt
			case reflect.Int64:
				p.enc = (*Buffer).enc_array_packed_int64
				wire = WireBytes // packed=true...
				p.asProtobuf = "repeated " + int64_encoder_txt
			case reflect.Uint64:
				p.enc = (*Buffer).enc_array_packed_int64
				wire = WireBytes // packed=true...
				p.asProtobuf = "repeated " + uint64_encoder_txt
			case reflect.Uint8:
				p.enc = (*Buffer).enc_array_byte
				p.asProtobuf = "bytes"
			case reflect.Float32:
				// can just treat them as bits
				p.enc = (*Buffer).enc_array_packed_uint32
				wire = WireBytes // packed=true...
				p.asProtobuf = "repeated float"
			case reflect.Float64:
				// can just treat them as bits
				p.enc = (*Buffer).enc_array_packed_int64
				wire = WireBytes // packed=true...
				p.asProtobuf = "repeated double"
			case reflect.String:
				p.enc = (*Buffer).enc_array_string
				p.asProtobuf = "repeated string"
			case reflect.Struct:
				p.stype = t2
				p.sprop = getPropertiesLocked(t2)
				p.isMarshaler = isMarshaler(reflect.PtrTo(t2))
				p.enc = (*Buffer).enc_array_struct_message
				p.asProtobuf = "repeated " + p.stypeAsProtobuf()
			case reflect.Ptr:
				switch t3 := t2.Elem(); t3.Kind() {
				default:
					fmt.Fprintf(os.Stderr, "protobuf3: no ptr oenc for %s -> %s -> %s\n", t1.Name(), t2.Name(), t3.Name())
					break
				case reflect.Struct:
					p.stype = t3
					p.sprop = getPropertiesLocked(t3)
					p.isMarshaler = isMarshaler(t2)
					p.enc = (*Buffer).enc_array_ptr_struct_message
					p.asProtobuf = "repeated " + p.stypeAsProtobuf()
				}
			}
		}

	case reflect.Map:
		p.enc = (*Buffer).enc_new_map

		p.mtype = t1
		p.mkeyprop = &Properties{}
		p.mkeyprop.init(reflect.PtrTo(p.mtype.Key()), "Key", f.Tag.Get("protobuf_key"), nil)
		p.mvalprop = &Properties{}

		vtype := p.mtype.Elem()
		if vtype.Kind() != reflect.Ptr && vtype.Kind() != reflect.Slice {
			// The value type is not a message (*T) or bytes ([]byte),
			// so we need encoders for the pointer to this type.
			vtype = reflect.PtrTo(vtype)
		}
		p.mvalprop.init(vtype, "Value", f.Tag.Get("protobuf_val"), nil)
		p.asProtobuf = fmt.Sprintf("map<%s,%s>", p.mtype.Key().Name(), vtype.Name()) // TODO finish this
	}

	// precalculate tag code
	x := p.Tag<<3 | uint32(wire)
	i := 0
	for i = 0; x > 127; i++ {
		p.tagbuf[i] = 0x80 | uint8(x&0x7F)
		x >>= 7
	}
	p.tagbuf[i] = uint8(x)
	p.tagcode = p.tagbuf[0 : i+1]
}

// using p.Name, p.stype and p.sprop, figure out the right name for the type of field p.
// if the name of the type is known, use that. Otherwise build a nested type and use it.
func (p *Properties) stypeAsProtobuf() string {
	n := p.stype.Name()
	if n != "" {
		return n
	}
	// the struct has no typename. It is an anonymous type in Go. The equivalent in Protobuf is
	// a a nested type. We use the name of the field as the name of the type, since the name of
	// the field ought to be unique within the enclosing struct type.

	lines := []string{p.sprop.asProtobuf(p.stype, p.Name)}
	lines = append(lines, p.Name)
	str := strings.Join(lines, "\n")
	// indent str two spaces to the right. we have to do this as a search step rather than as part of Join()
	// because the strings lines are already multi-line strings. (The other solutions are to indent as a
	// reformatting step at the end, or to store Properties.asProtobuf as []string and never loose the LFs.
	// The latter makes asProtobuf expensive for all the simple types. Reformatting needs to work on all fields.
	// So the "nasty" approach here is, AFAICS, for the best.
	return strings.Replace(str, "\n", "\n  ", -1)
}

var (
	marshalerType = reflect.TypeOf((*Marshaler)(nil)).Elem()
)

// isMarshaler reports whether type t implements Marshaler.
func isMarshaler(t reflect.Type) bool {
	return t.Implements(marshalerType)
}

// Init populates the properties from a protocol buffer struct tag.
func (p *Properties) init(typ reflect.Type, name, tag string, f *reflect.StructField) (bool, error) {
	// "bytes,49,opt,def=hello!"

	// fields without a protobuf tag are an error
	if tag == "" {
		// backwards compatability HACK. canonical golang.org/protobuf ignores errors on fields with names that start with XXX_
		// we must do the same to pass their unit tests
		if XXXHack && strings.HasPrefix(name, "XXX_") {
			return true, nil
		}
		err := fmt.Errorf("protobuf3: %s (%s) lacks a protobuf tag. Mark it with `protobuf:\"-\"` to suppress this error", name, typ.Name())
		fmt.Fprintln(os.Stderr, err) // print the error too
		return true, err
	}

	p.Name = name
	if f != nil {
		p.field = field(f.Offset)
	}

	intencoder, skip, err := p.Parse(tag)
	if skip || err != nil {
		return skip, err
	}

	p.setEnc(typ, f, intencoder)

	return false, nil
}

var (
	propertiesMu  sync.RWMutex
	propertiesMap = make(map[reflect.Type]*StructProperties)
)

func init() {
	// synthesize a StructProperties for time.Time which will encode it
	// to the same as the standard protobuf3 Timestamp type.
	propertiesMap[reflect.TypeOf(time.Time{})] = &StructProperties{
		Prop: []Properties{
			Properties{
				Name: "time.Time",
				enc:  (*Buffer).enc_time_Time,
			},
		},
		order: []int{0},
	}
}

// GetProperties returns the list of properties for the type represented by t.
// t must represent a generated struct type of a protocol message.
func GetProperties(t reflect.Type) *StructProperties {
	k := t.Kind()
	// accept a pointer-to-struct as well (but just one level)
	if k == reflect.Ptr {
		t = t.Elem()
		k = t.Kind()
	}
	if k != reflect.Struct {
		panic("protobuf3: type must have kind struct")
	}

	// Most calls to GetProperties in a long-running program will be
	// retrieving details for types we have seen before.
	propertiesMu.RLock()
	sprop, ok := propertiesMap[t]
	propertiesMu.RUnlock()
	if ok {
		return sprop
	}

	propertiesMu.Lock()
	sprop = getPropertiesLocked(t)
	propertiesMu.Unlock()
	return sprop
}

// getPropertiesLocked requires that propertiesMu is held.
func getPropertiesLocked(t reflect.Type) *StructProperties {
	if prop, ok := propertiesMap[t]; ok {
		return prop
	}

	prop := new(StructProperties)
	// in case of recursive protos, fill this in now.
	propertiesMap[t] = prop

	// build properties
	nf := t.NumField()
	prop.Prop = make([]Properties, nf)
	prop.order = make([]int, nf)

	// sanity check for duplicate tags, since some of us are hand editing the tags
	seen := make(map[uint32]struct{})

	j := 0
	for i := 0; i < nf; i++ {
		f := t.Field(i)
		p := &prop.Prop[i]
		name := f.Name

		skip, err := p.init(f.Type, name, f.Tag.Get("protobuf"), &f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "protobuf3: Error preparing field %q of type %q: %v\n", name, t.Name(), err)
			continue
		}
		if skip {
			// silently skip this field. It's not part of the protobuf encoding of this struct
			continue
		}
		if _, ok := seen[p.Tag]; ok {
			sname := t.Name()
			if sname == "" {
				sname = "<anonymous struct>"
			}
			panic(fmt.Sprintf("protobuf3: duplicate tag %d on %s.%s", p.Tag, sname, name))
		}
		seen[p.Tag] = struct{}{}

		prop.order[j] = i
		j++

		if debug {
			print(i, " ", f.Name, " ", t.String(), " ")
			if p.Tag > 0 {
				print(p.String())
			}
			print("\n")
		}

		if p.enc == nil {
			fmt.Fprintln(os.Stderr, "protobuf3: no encoder for", f.Name, f.Type.String(), "[GetProperties]")
		}
	}

	// slice off any unused indexes
	prop.order = prop.order[:j]

	// Re-order prop.order.
	sort.Sort(prop)

	return prop
}
