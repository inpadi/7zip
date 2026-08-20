// Copyright 2014-2022 Ulrich Kunitz. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lzma

import (
	"fmt"
	"unicode"
)

// operation represents either a literal or a match. A zero match length marks
// a literal; valid matches always have a positive length.
type operation struct {
	match
	b byte
}

func literalOperation(b byte) operation { return operation{b: b} }

func matchOperation(m match) operation { return operation{match: m} }

// Len returns the number of uncompressed bytes represented by the operation.
func (o operation) Len() int {
	if o.n == 0 {
		return 1
	}
	return o.n
}

// rep represents a repetition at the given distance and the given length
type match struct {
	// supports all possible distance values, including the eos marker
	distance int64
	// length
	n int
}

// Len returns the number of bytes matched.
func (m match) Len() int {
	return m.n
}

// String returns a string representation for the repetition.
func (m match) String() string {
	return fmt.Sprintf("M{%d,%d}", m.distance, m.n)
}

// lit represents a single byte literal.
type lit struct {
	b byte
}

// Len returns 1 for the single byte literal.
func (l lit) Len() int {
	return 1
}

// String returns a string representation for the literal.
func (l lit) String() string {
	var c byte
	if unicode.IsPrint(rune(l.b)) {
		c = l.b
	} else {
		c = '.'
	}
	return fmt.Sprintf("L{%c/%02x}", c, l.b)
}
