/*
 * Copyright 2011-2012 Branimir Karadzic. All rights reserved.
 *
 * Redistribution and use in source and binary forms, with or without modification,
 * are permitted provided that the following conditions are met:
 *
 *    1. Redistributions of source code must retain the above copyright notice, this
 *       list of conditions and the following disclaimer.
 *
 *    2. Redistributions in binary form must reproduce the above copyright notice,
 *       this list of conditions and the following disclaimer in the documentation
 *       and/or other materials provided with the distribution.
 *
 * THIS SOFTWARE IS PROVIDED BY COPYRIGHT HOLDER ``AS IS'' AND ANY EXPRESS OR
 * IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT
 * SHALL COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT,
 * INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
 * LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
 * PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY,
 * WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE
 * OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF
 * THE POSSIBILITY OF SUCH DAMAGE.
 */

package lz4

import "errors"

const (
	minMatch              = 4
	hashLog               = 17
	hashTableSize         = 1 << hashLog
	hashShift             = (minMatch * 8) - hashLog
	incompressible uint32 = 128
	uninitHash            = 0x88888888
	MaxInputSize          = 0x7E000000
)

var (
	ErrTooLarge = errors.New("input too large")
)

type encoder struct {
	src       []byte
	dst       []byte
	hashTable []uint32
	pos       uint32
	anchor    uint32
	dpos      uint32
}

// CompressBound returns the maximum length of a lz4 block, given it's uncompressed length
func CompressBound(isize int) int {
	if isize > MaxInputSize {
		return 0
	}
	return isize + ((isize) / 255) + 16
}

func (e *encoder) readUint32(pos int) uint32 {

	if pos >= len(e.src) {
		return 0
	}

	return uint32(e.src[pos+3])<<24 | uint32(e.src[pos+2])<<16 | uint32(e.src[pos+1])<<8 | uint32(e.src[pos+0])
}

func (e *encoder) writeLiterals(length, mlLen, pos uint32) {

	ln := length

	var code byte
	if ln > runMask-1 {
		code = runMask
	} else {
		code = byte(ln)
	}

	if mlLen > mlMask-1 {
		e.dst[e.dpos] = (code << mlBits) + byte(mlMask)
	} else {
		e.dst[e.dpos] = (code << mlBits) + byte(mlLen)
	}
	e.dpos++

	if code == runMask {
		ln -= runMask
		for ; ln > 254; ln -= 255 {
			e.dst[e.dpos] = 255
			e.dpos++
		}

		e.dst[e.dpos] = byte(ln)
		e.dpos++
	}

	copy(e.dst[e.dpos:], e.src[pos:pos+length])
	e.dpos += length
}

func Encode(dst, src []byte) ([]byte, error) {

	if len(src) >= MaxInputSize {
		return nil, ErrTooLarge
	}

	if n := CompressBound(len(src)); len(dst) < n {
		dst = make([]byte, n)
	}

	e := encoder{src: src, dst: dst, hashTable: make([]uint32, hashTableSize)}

	var (
		step  uint32 = 1
		limit uint32 = incompressible
	)

	for {
		if int(e.pos)+4 >= len(e.src) {
			e.writeLiterals(uint32(len(e.src))-e.anchor, 0, e.anchor)
			return e.dst[:e.dpos], nil
		}
		sequence := e.readUint32(int(e.pos))
		hash := (sequence * 2654435761) >> hashShift
		ref := e.hashTable[hash] + uninitHash
		e.hashTable[hash] = e.pos - uninitHash

		if ((e.pos-ref)>>16) != 0 || e.readUint32(int(ref)) != sequence {
			if e.pos-e.anchor > limit {
				limit <<= 1
				step += 1 + (step >> 2)
			}
			e.pos += step
			continue
		}

		if step > 1 {
			e.hashTable[hash] = ref - uninitHash
			e.pos -= step - 1
			step = 1
			continue
		}
		limit = incompressible

		ln := e.pos - e.anchor
		back := e.pos - ref

		anchor := e.anchor

		e.pos += minMatch
		ref += minMatch
		e.anchor = e.pos

		for int(e.pos) < len(e.src) && e.src[e.pos] == e.src[ref] {
			e.pos++
			ref++
		}

		mlLen := e.pos - e.anchor

		e.writeLiterals(ln, mlLen, anchor)
		e.dst[e.dpos] = uint8(back)
		e.dst[e.dpos+1] = uint8(back >> 8)
		e.dpos += 2

		if mlLen > mlMask-1 {
			mlLen -= mlMask
			for mlLen > 254 {
				mlLen -= 255

				e.dst[e.dpos] = 255
				e.dpos++
			}

			e.dst[e.dpos] = byte(mlLen)
			e.dpos++
		}

		e.anchor = e.pos
	}
}
