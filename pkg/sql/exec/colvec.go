// Copyright 2018 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

package exec

import (
	"fmt"

	"github.com/cockroachdb/apd"
	"github.com/cockroachdb/cockroach/pkg/sql/exec/types"
)

// column is an interface that represents a raw array of a Go native type.
type column interface{}

// ColVec is an interface that represents a column vector that's accessible by
// Go native types.
type ColVec interface {
	Nulls

	// TODO(jordan): is a bitmap or slice of bools better?
	// Bool returns a bool list.
	Bool() []bool
	// Int8 returns an int8 slice.
	Int8() []int8
	// Int16 returns an int16 slice.
	Int16() []int16
	// Int32 returns an int32 slice.
	Int32() []int32
	// Int64 returns an int64 slice.
	Int64() []int64
	// Float32 returns a float32 slice.
	Float32() []float32
	// Float64 returns a float64 slice.
	Float64() []float64
	// Bytes returns a []byte slice.
	Bytes() [][]byte
	// TODO(jordan): should this be [][]byte?
	// Decimal returns an apd.Decimal slice.
	Decimal() []apd.Decimal

	// Col returns the raw, typeless backing storage for this ColVec.
	Col() interface{}

	// TemplateType returns an []interface{} and is used for operator templates.
	// Do not call this from normal code - it'll always panic.
	_TemplateType() []interface{}

	// Append appends fromLength elements of the given ColVec to toLength
	// elements of this ColVec, assuming that both ColVecs are of type colType.
	Append(vec ColVec, colType types.T, toLength uint64, fromLength uint16)

	// AppendWithSel appends into itself another column vector from a ColBatch with
	// maximum size of ColBatchSize, filtered by the given selection vector.
	AppendWithSel(vec ColVec, sel []uint16, batchSize uint16, colType types.T, toLength uint64)

	// Copy copies src[srcStartIdx:srcEndIdx] into this ColVec.
	Copy(src ColVec, srcStartIdx, srcEndIdx uint64, typ types.T)

	// CopyWithSelInt64 copies vec, filtered by sel, into this ColVec. It replaces
	// the contents of this ColVec.
	CopyWithSelInt64(vec ColVec, sel []uint64, nSel uint16, colType types.T)
	// CopyWithSelInt16 copies vec, filtered by sel, into this ColVec. It replaces
	// the contents of this ColVec.
	CopyWithSelInt16(vec ColVec, sel []uint16, nSel uint16, colType types.T)

	// PrettyValueAt returns a "pretty"value for the idx'th value in this ColVec.
	// It uses the reflect package and is not suitable for calling in hot paths.
	PrettyValueAt(idx uint16, colType types.T) string
}

// Nulls represents a list of potentially nullable values.
type Nulls interface {
	// HasNulls returns true if the column has any null values.
	HasNulls() bool

	// At returns true if the ith value of the column is null.
	NullAt(i uint16) bool

	// SetNull sets the ith value of the column to null.
	SetNull(i uint16)

	// UnsetNulls sets the column to have 0 null values.
	UnsetNulls()
}

var _ ColVec = &memColumn{}

var emptyNulls [ColBatchSize / 8 / 8]int64

// memColumn is a simple pass-through implementation of ColVec that just casts
// a generic interface{} to the proper type when requested.
type memColumn struct {
	col column

	nulls [ColBatchSize / 8 / 8]int64
}

// newMemColumn returns a new memColumn, initialized with a length.
func newMemColumn(t types.T, n int) *memColumn {
	switch t {
	case types.Bool:
		return &memColumn{col: make([]bool, n)}
	case types.Bytes:
		return &memColumn{col: make([][]byte, n)}
	case types.Int8:
		return &memColumn{col: make([]int8, n)}
	case types.Int16:
		return &memColumn{col: make([]int16, n)}
	case types.Int32:
		return &memColumn{col: make([]int32, n)}
	case types.Int64:
		return &memColumn{col: make([]int64, n)}
	case types.Float32:
		return &memColumn{col: make([]float32, n)}
	case types.Float64:
		return &memColumn{col: make([]float64, n)}
	case types.Decimal:
		return &memColumn{col: make([]apd.Decimal, n)}
	default:
		panic(fmt.Sprintf("unhandled type %s", t))
	}
}

func (m *memColumn) HasNulls() bool {
	sum := int64(0)
	for i := range m.nulls {
		sum += m.nulls[i]
	}
	return sum != 0
}

func (m *memColumn) NullAt(i uint16) bool {
	intIdx := i >> 6
	return ((m.nulls[intIdx] >> (i % 64)) & 1) == 1
}

func (m *memColumn) SetNull(i uint16) {
	intIdx := i >> 6
	m.nulls[intIdx] |= 1 << (i % 64)
}

func (m *memColumn) UnsetNulls() {
	copy(m.nulls[:], emptyNulls[:])
}

func (m *memColumn) Bool() []bool {
	return m.col.([]bool)
}

func (m *memColumn) Int8() []int8 {
	return m.col.([]int8)
}

func (m *memColumn) Int16() []int16 {
	return m.col.([]int16)
}

func (m *memColumn) Int32() []int32 {
	return m.col.([]int32)
}

func (m *memColumn) Int64() []int64 {
	return m.col.([]int64)
}

func (m *memColumn) Float32() []float32 {
	return m.col.([]float32)
}

func (m *memColumn) Float64() []float64 {
	return m.col.([]float64)
}

func (m *memColumn) Bytes() [][]byte {
	return m.col.([][]byte)
}

func (m *memColumn) Decimal() []apd.Decimal {
	return m.col.([]apd.Decimal)
}

func (m *memColumn) Col() interface{} {
	return m.col
}

func (m *memColumn) _TemplateType() []interface{} {
	panic("don't call this from non template code")
}
