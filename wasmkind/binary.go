package wasmkind

import (
	"errors"
	"fmt"
	"strings"
)

// imported is one import of a module: from where, its name, and what it is.
type imported struct {
	module, name, what string
}

func (i imported) String() string { return i.module + "." + i.name + " (" + i.what + ")" }

// importsOf reads the imports of a binary module from its import section (section 2), which the
// runtime does not list whole: it lists the functions and the memories, not the tables and the
// globals. A module of kinds imports nothing (kaname's DESIGN 36.11).
func importsOf(bin []byte) ([]imported, error) {
	if len(bin) < 8 || string(bin[:4]) != "\x00asm" {
		return nil, errors.New("it is not a binary module of WebAssembly")
	}
	r := &reader{b: bin, i: 8}
	for r.i < len(r.b) {
		id := r.byte()
		size := r.u32()
		end := r.i + int(size)
		if r.err != nil || end > len(r.b) || end < r.i {
			return nil, errors.New("its sections end before their sizes say")
		}
		if id != 2 {
			r.i = end
			continue
		}
		n := r.u32()
		var out []imported
		for k := uint32(0); k < n && r.err == nil; k++ {
			im := imported{module: r.name(), name: r.name()}
			switch kind := r.byte(); kind {
			case 0:
				im.what = "a function"
				r.u32()
			case 1:
				im.what = "a table"
				r.byte()
				r.limits()
			case 2:
				im.what = "a memory"
				r.limits()
			case 3:
				im.what = "a global"
				r.byte()
				r.byte()
			default:
				im.what = fmt.Sprintf("an import of kind %d", kind)
				r.i = end
			}
			out = append(out, im)
		}
		if r.err != nil {
			return nil, errors.New("its import section is not one WebAssembly reads")
		}
		return out, nil
	}
	return nil, nil
}

// importsSaid says the imports of a module, three at most.
func importsSaid(ims []imported) string {
	var words []string
	for i, im := range ims {
		if i == 3 {
			words = append(words, fmt.Sprintf("and %d more", len(ims)-3))
			break
		}
		words = append(words, im.String())
	}
	return strings.Join(words, ", ")
}

// reader reads the numbers and the names of a binary module.
type reader struct {
	b   []byte
	i   int
	err error
}

var errShort = errors.New("the module ends early")

func (r *reader) byte() byte {
	if r.err != nil || r.i >= len(r.b) {
		r.err = errShort
		return 0
	}
	c := r.b[r.i]
	r.i++
	return c
}

// u32 reads an unsigned number of LEB128, of at most 32 bits.
func (r *reader) u32() uint32 {
	var v uint32
	for shift := 0; shift < 35; shift += 7 {
		c := r.byte()
		if r.err != nil {
			return 0
		}
		v |= uint32(c&0x7f) << shift
		if c&0x80 == 0 {
			return v
		}
	}
	r.err = errors.New("a number of the module is longer than 32 bits")
	return 0
}

func (r *reader) name() string {
	n := int(r.u32())
	if r.err != nil || n > len(r.b)-r.i {
		r.err = errShort
		return ""
	}
	s := string(r.b[r.i : r.i+n])
	r.i += n
	return s
}

func (r *reader) limits() {
	flag := r.byte()
	r.u32()
	if flag&1 != 0 {
		r.u32()
	}
}
