// Copyright 2017 The Cockroach Authors.
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

package opt

// This file is home to TestOpt, which is similar to the logic tests, except it
// is used for optimizer-specific testcases.
//
// Each testfile contains testcases of the form
//   <command>[,<command>...] [arg | arg=val | arg=(val1, val2, ...)]...
//   <SQL statement or expression>
//   ----
//   <expected results>
//
// The supported commands are:
//
//  - semtree-normalize
//
//    Builds an expression tree from a scalar SQL expression and runs the
//    TypedExpr normalization code. It must be followed by build-scalar.
//
//  - build-scalar
//
//    Builds an expression tree from a scalar SQL expression and outputs a
//    representation of the tree. The expression can refer to external variables
//    using @1, @2, etc. in which case the types of the variables must be passed
//    via a "columns" argument.
//
//  - normalize
//
//    Normalizes the expression. If present, must follow build-scalar.
//
//  - semtree-expr
//
//    Converts the scalar expression to a TypedExpr and prints it.
//    If present, must follow build-scalar or semtree-normalize.
//
//  - index-constraints
//
//    Creates index constraints on the assumption that the index is formed by
//    the index var columns (as specified by "columns").
//    If present, build-scalar must have been an earlier command.
//
// The supported arguments are:
//
//  - vars=(<type>, ...)
//
//    Sets the types for the index vars in the expression.
//
//  - index=(@<index> [ascending|asc|descending|desc] [not null], ...)
//
//    Information for the index (used by index-constraints). Each column of the
//    index refers to an index var.
//
//  - inverted-index=@<index>
//
//    Information about an inverted index (used by index-constraints). The one column of
//    the inverted index refers to an index var. Only one of "index" and
//    "inverted-index" should be used.

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/types"
	"github.com/cockroachdb/cockroach/pkg/testutils/datadriven"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/util/encoding"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"

	"github.com/cockroachdb/cockroach/pkg/sql/opt/testutils"
	_ "github.com/cockroachdb/cockroach/pkg/sql/sem/builtins"
)

var (
	testDataGlob = flag.String("d", "testdata/[^.]*", "test data glob")
)

func TestOpt(t *testing.T) {
	defer leaktest.AfterTest(t)()

	paths, err := filepath.Glob(*testDataGlob)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no testfiles found matching: %s", *testDataGlob)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			ctx := context.Background()
			s, sqlDB, _ := serverutils.StartServer(t, base.TestServerArgs{})
			defer s.Stopper().Stop(ctx)

			datadriven.RunTest(t, path, func(d *datadriven.TestData) string {
				var e *Expr
				var varTypes []types.T
				var iVarHelper tree.IndexedVarHelper
				var colInfos []IndexColumnInfo
				var invertedIndex bool
				var typedExpr tree.TypedExpr
				st := cluster.MakeTestingClusterSettings()
				evalCtx := tree.MakeTestingEvalContext(st)

				for _, arg := range d.CmdArgs {
					key := arg
					val := ""
					if pos := strings.Index(key, "="); pos >= 0 {
						key = arg[:pos]
						val = arg[pos+1:]
					}
					if len(val) > 2 && val[0] == '(' && val[len(val)-1] == ')' {
						val = val[1 : len(val)-1]
					}
					vals := strings.Split(val, ",")
					switch key {
					case "vars":
						varTypes, err = testutils.ParseTypes(vals)
						if err != nil {
							d.Fatalf(t, "%v", err)
						}

						iVarHelper = tree.MakeTypesOnlyIndexedVarHelper(varTypes)

					case "index", "inverted-index":
						if varTypes == nil {
							d.Fatalf(t, "vars must precede index")
						}
						var err error
						colInfos, err = parseIndexColumns(varTypes, vals)
						if err != nil {
							d.Fatalf(t, "%v", err)
						}
						if key == "inverted-index" {
							if len(colInfos) > 1 {
								d.Fatalf(t, "inverted index must be on a single column")
							}
							invertedIndex = true
						}
					default:
						d.Fatalf(t, "unknown argument: %s", key)
					}
				}
				getTypedExpr := func() tree.TypedExpr {
					if typedExpr == nil {
						var err error
						typedExpr, err = testutils.ParseScalarExpr(d.Input, iVarHelper.Container())
						if err != nil {
							d.Fatalf(t, "%v", err)
						}
					}
					return typedExpr
				}
				buildScalarFn := func() {
					var err error
					e, err = buildScalar(getTypedExpr(), &evalCtx)
					if err != nil {
						t.Fatal(err)
					}
				}

				for _, cmd := range strings.Split(d.Cmd, ",") {
					switch cmd {
					case "semtree-normalize":
						// Apply the TypedExpr normalization and rebuild the expression.
						typedExpr, err = evalCtx.NormalizeExpr(getTypedExpr())
						if err != nil {
							d.Fatalf(t, "%v", err)
						}

					case "exec-raw":
						_, err := sqlDB.Exec(d.Input)
						if err != nil {
							d.Fatalf(t, "%v", err)
						}
						return ""

					case "build-scalar":
						buildScalarFn()

					case "normalize":
						normalizeExpr(e)

					case "semtree-expr":
						c := typedExprConvCtx{ivh: &iVarHelper}
						expr := scalarToTypedExpr(&c, e)
						return fmt.Sprintf("%s%s\n", e.String(), expr)

					case "index-constraints":
						if e == nil {
							d.Fatalf(t, "no expression for index-constraints")
						}
						var ic IndexConstraints

						ic.Init(e, colInfos, invertedIndex, &evalCtx)
						spans, ok := ic.Spans()

						var buf bytes.Buffer
						if !ok {
							spans = LogicalSpans{MakeFullSpan()}
						}
						for _, sp := range spans {
							fmt.Fprintf(&buf, "%s\n", sp)
						}
						remainingFilter := ic.RemainingFilter(&iVarHelper)
						if remainingFilter != nil {
							fmt.Fprintf(&buf, "Remaining filter: %s\n", remainingFilter)
						}
						return buf.String()
					default:
						d.Fatalf(t, "unsupported command: %s", cmd)
						return ""
					}
				}
				return e.String()
			})
		})
	}
}

// parseIndexColumns parses descriptions of index columns; each
// string corresponds to an index column and is of the form:
//   <type> [ascending|descending]
func parseIndexColumns(indexVarTypes []types.T, colStrs []string) ([]IndexColumnInfo, error) {
	res := make([]IndexColumnInfo, len(colStrs))
	for i := range colStrs {
		fields := strings.Fields(colStrs[i])
		if fields[0][0] != '@' {
			return nil, fmt.Errorf("index column must start with @<index>")
		}
		idx, err := strconv.Atoi(fields[0][1:])
		if err != nil {
			return nil, err
		}
		if idx < 1 || idx > len(indexVarTypes) {
			return nil, fmt.Errorf("invalid index var @%d", idx)
		}
		res[i].VarIdx = idx - 1
		res[i].Typ = indexVarTypes[res[i].VarIdx]
		res[i].Direction = encoding.Ascending
		res[i].Nullable = true
		fields = fields[1:]
		for len(fields) > 0 {
			switch strings.ToLower(fields[0]) {
			case "ascending", "asc":
				// ascending is the default.
				fields = fields[1:]
			case "descending", "desc":
				res[i].Direction = encoding.Descending
				fields = fields[1:]

			case "not":
				if len(fields) < 2 || strings.ToLower(fields[1]) != "null" {
					return nil, fmt.Errorf("unknown column attribute %s", fields)
				}
				res[i].Nullable = false
				fields = fields[2:]
			default:
				return nil, fmt.Errorf("unknown column attribute %s", fields)
			}
		}
	}
	return res, nil
}
