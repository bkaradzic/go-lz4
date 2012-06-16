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

import (
	"bufio"
	"io"
)

const (
	mlBits     = 4
	mlMask     = (1 << mlBits) - 1
	runBits    = 8 - mlBits
	runMask    = (1 << runBits) - 1
	bufferSize = 128 << 10
	flushSize  = 1 << 16
)

type decoder struct {
	r   io.ByteReader
	w   *bufio.Writer
	buf []byte
	pos uint32
	ref uint32
}

func (d *decoder) getLen() (uint32, error) {

	length := uint32(0)
	ln, err := d.r.ReadByte()
	if err != nil {
		return 0, err
	}
	for ln == 255 {
		length += 255
		ln, err = d.r.ReadByte()
		if err != nil {
			return 0, err
		}
	}
	length += uint32(ln)

	return length, nil
}

func (d *decoder) readUint16() (uint16, error) {
	b1, err := d.r.ReadByte()
	if err != nil {
		return 0, err
	}
	b2, err := d.r.ReadByte()
	if err != nil {
		return 0, err
	}
	u16 := (uint16(b2) << 8) | uint16(b1)
	return u16, nil
}

func (d *decoder) cp(length, decr uint32) {
	d.flush(length)

	for ii := uint32(0); ii < length; ii++ {
		d.buf[d.pos+ii] = d.buf[d.ref+ii]
	}
	d.pos += length
	d.ref += length - decr
}

func (d *decoder) consume(length uint32) error {

	d.flush(length)

	for ii := uint32(0); ii < length; ii++ {
		by, err := d.r.ReadByte()
		if err != nil {
			return d.finish(err)
		}
		d.buf[d.pos] = by
		d.pos++
	}

	return nil
}

func (d *decoder) flush(length uint32) {

	if d.pos+length > bufferSize {
		s := d.ref - flushSize
		d.w.Write(d.buf[0:s])
		n := d.pos - d.ref
		copy(d.buf[0:flushSize+n], d.buf[s:d.pos])
		d.pos = flushSize + n
		d.ref = flushSize
	}
}

func (d *decoder) finish(err error) error {
	if err == io.EOF {
		d.w.Write(d.buf[0:d.pos])
		return d.w.Flush()
	}

	return err
}

func decode1(pw *io.PipeWriter, r io.ByteReader) error {

	w := bufio.NewWriter(pw)
	d := decoder{r, w, make([]byte, bufferSize), 0, 0}

	decr := []uint32{0, 3, 2, 3}

	for {
		code, err := d.r.ReadByte()
		if err != nil {
			return d.finish(err)
		}

		length := uint32(code >> mlBits)
		if length == runMask {
			ln, err := d.getLen()
			if err != nil {
				return d.finish(err)
			}
			length += ln
		}

		err = d.consume(length)
		if err != nil {
			return d.finish(err)
		}

		back, err := d.readUint16()
		if err != nil {
			return d.finish(err)
		}
		d.ref = d.pos - uint32(back)

		length = uint32(code & mlMask)
		if length == mlMask {
			ln, err := d.getLen()
			if err != nil {
				return d.finish(err)
			}
			length += ln
		}

		literal := d.pos - d.ref
		if literal < 4 {
			d.cp(4, decr[literal])
		} else {
			length += 4
		}

		d.cp(length, 0)
	}
	panic("unreachable")
}

func decode(r io.Reader, pw *io.PipeWriter) {
	br, ok := r.(io.ByteReader)
	if !ok {
		br = bufio.NewReader(r)
	}
	pw.CloseWithError(decode1(pw, br))
}

func NewReader(r io.Reader) io.ReadCloser {
	pr, pw := io.Pipe()
	go decode(r, pw)
	return pr
}
