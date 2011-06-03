/*
 * Copyright 2011 Branimir Karadzic. All rights reserved.
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

import (
	"bufio"
	"io"
	"os"
)

const (
	minMatch       = 4
	hashLog        = 17
	hashTableSize  = 1 << hashLog
	hashMask       = hashTableSize - 1
	hashShift      = (minMatch * 8) - hashLog
	incompressible uint8 = 128
	prefetchSize   = 1024
	flushPos       = bufferSize - prefetchSize
	uninitHash     = 0x88888888
)

type encoder struct {
	r         io.ByteReader
	w         *bufio.Writer
	hashTable []uint32
	buf       []byte
	pos       uint32
	anchor    uint32
	cached    uint32
}

type errWriteCloser struct {
	err os.Error
}

func (e *errWriteCloser) Write([]byte) (int, os.Error) {
	return 0, e.err
}

func (e *errWriteCloser) Close() os.Error {
	return e.err
}

func (e *encoder) cache(ln uint32) os.Error {

	if e.pos+ln > e.cached {
		for ii := uint32(0); ii < ln; ii++ {
			b0, err := e.r.ReadByte()
			if err != nil && e.pos == e.cached {
				return os.EOF
			}
			e.buf[e.cached] = b0
			e.cached++
		}
	}

	return nil
}

func (e *encoder) readUint32() (uint32, os.Error) {
	err := e.cache(4)
	if err != nil {
		return 0, err
	}

	b0 := e.buf[e.pos+0]
	b1 := e.buf[e.pos+1]
	b2 := e.buf[e.pos+2]
	b3 := e.buf[e.pos+3]

	return uint32(b3)<<24 | uint32(b2)<<16 | uint32(b1)<<8 | uint32(b0), nil
}

func (e *encoder) readRef(ref uint32) uint32 {
	b0 := e.buf[ref+0]
	b1 := e.buf[ref+1]
	b2 := e.buf[ref+2]
	b3 := e.buf[ref+3]
	seq := uint32(b3)<<24 | uint32(b2)<<16 | uint32(b1)<<8 | uint32(b0)
	return seq
}

func (e *encoder) writeLiterals(length, mlLen, anchor uint32) {

	ln := length

	var code byte
	if ln > runMask-1 {
		code = runMask
	} else {
		code = byte(ln)
	}

	if mlLen > mlMask-1 {
		e.w.WriteByte((code << mlBits) + byte(mlMask))
	} else {
		e.w.WriteByte((code << mlBits) + byte(mlLen))
	}

	if code == runMask {
		ln -= runMask
		for ; ln > 254; ln -= 255 {
			e.w.WriteByte(255)
		}
		e.w.WriteByte(byte(ln))
	}

	for ii := uint32(0); ii < length; ii++ {
		e.w.WriteByte(e.buf[anchor+ii])
	}
}

func (e *encoder) writeUint16(value uint16) {
	e.w.WriteByte(uint8(value))
	e.w.WriteByte(uint8(value >> 8))
}

func (e *encoder) flush() {
	if e.cached > flushPos {
		length := e.cached - e.anchor
		copy(e.buf[0:length], e.buf[e.anchor:e.anchor+length])

		for ii := uint32(0); ii < hashTableSize; ii++ {
			if e.hashTable[ii] != uninitHash {
				if e.hashTable[ii] < e.anchor {
					e.hashTable[ii] = uninitHash
				} else {
					e.hashTable[ii] -= e.anchor
				}
			}
		}

		e.pos -= e.anchor
		e.cached -= e.anchor
		e.anchor = 0
	}
}

func (e *encoder) finish(err os.Error) os.Error {

	if err == os.EOF {
		e.writeLiterals(e.cached-e.pos, 0, e.anchor)
		return e.w.Flush()
	}

	return err
}

func encode1(pw *io.PipeWriter, r io.ByteReader) os.Error {

	w := bufio.NewWriter(pw)
	e := encoder{r, w, make([]uint32, hashTableSize), make([]byte, bufferSize), 0, 0, 0}

	var (
		step  uint32 = 1
		limit uint32 = 128
	)

	for ii := uint32(0); ii < hashTableSize; ii++ {
		e.hashTable[ii] = uninitHash
	}

	for {
		e.flush()
		sequence, err := e.readUint32()
		if err != nil {
			return e.finish(err)
		}
		hash := (sequence * 2654435761) >> hashShift
		ref := e.hashTable[hash]
		e.hashTable[hash] = e.pos

		if ((e.pos-ref)>>16) != 0 || e.readRef(ref) != sequence {
			if e.pos-e.anchor > limit {
				limit <<= 1
				step += 1 + (step >> 2)
			}
			e.pos += step
			continue
		}

		if step > 1 {
			e.hashTable[hash] = ref
			e.pos -= step - 1
			step = 1
			continue
		}
		limit = 128

		ln := e.pos - e.anchor
		back := e.pos - ref

		anchor := e.anchor

		e.pos += minMatch
		ref += minMatch
		e.anchor = e.pos

		for {
			err = e.cache(1)
			if err != nil || e.buf[e.pos] != e.buf[ref] {
				break
			}

			e.pos++
			ref++
		}

		mlLen := e.pos - e.anchor

		e.writeLiterals(ln, mlLen, anchor)
		e.writeUint16(uint16(back))

		if mlLen > mlMask-1 {
			mlLen -= mlMask
			for mlLen > 254 {
				mlLen -= 255
				e.w.WriteByte(255)
			}
			e.w.WriteByte(byte(mlLen))
		}

		e.anchor = e.pos

		if err != nil {
			e.finish(err)
		}
	}
	panic("unreachable")
}

func encode(r io.Reader, pw *io.PipeWriter) {
	br, ok := r.(io.ByteReader)
	if !ok {
		br = bufio.NewReader(r)
	}
	pw.CloseWithError(encode1(pw, br))
}

func NewWriter(r io.Reader) io.ReadCloser {
	pr, pw := io.Pipe()
	go encode(r, pw)
	return pr
}
