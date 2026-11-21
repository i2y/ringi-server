package server

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// The letters of the IDs: Crockford's base 32, which reads aloud without mix-ups.
const idLetters = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewID makes a ticket's ID: 26 characters, the time in milliseconds (10) then 80 random bits (16),
// so that IDs sort in the order they were made, to the millisecond (two made in one millisecond
// sort as their random bits do).
func NewID() string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixMilli())<<16)
	rand.Read(b[6:])
	hi := binary.BigEndian.Uint64(b[:8])
	lo := binary.BigEndian.Uint64(b[8:])
	var out [26]byte
	for i := 25; i >= 0; i-- {
		out[i] = idLetters[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}
