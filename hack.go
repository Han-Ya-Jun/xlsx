package xlsx

import (
	"flag"
	"fmt"
	"strconv"

	"github.com/unidoc/unioffice/schema/soo/sml"
	"github.com/unidoc/unioffice/spreadsheet"
)

// GetCellString returns the string in a cell if it's an inline or string table
// string. Otherwise it returns an empty string.
func GetCellString(w *spreadsheet.Workbook, c spreadsheet.Cell) string {
	x := c.X()

	switch x.TAttr {
	case sml.ST_CellTypeInlineStr:
		if x.Is != nil && x.Is.T != nil {
			return *x.Is.T
		}

		if x.V != nil {
			return *x.V
		}
	case sml.ST_CellTypeS:
		if x.V == nil {
			return ""
		}

		id, err := strconv.Atoi(*x.V)
		if err != nil {
			return ""
		}

		s, err := GetSharedString(w.SharedStrings.X(), id)
		if err != nil {
			return ""
		}

		return s
	}

	if x.V == nil {
		return ""
	}

	return *x.V
}

// GetSharedString retrieves a string from the shared strings table by index.
// nolint:goerr113
func GetSharedString(x *sml.Sst, id int) (string, error) {
	if id < 0 {
		return "", fmt.Errorf("invalid string index %d, must be > 0", id)
	}

	if id > len(x.Si) {
		return "", fmt.Errorf("invalid string index %d, table only has %d values", id, len(x.Si))
	}

	si := x.Si[id]

	if si.T != nil {
		return *si.T, nil
	}

	s := ""
	for _, r := range si.R {
		s += r.T
	}

	return s, nil
}

type flagNoopValue struct{}

func (*flagNoopValue) String() string   { return "noop" }
func (*flagNoopValue) Set(string) error { return nil }

func hackTestV() {
	if flag.Lookup("test.v") == nil {
		flag.CommandLine.Var(&flagNoopValue{}, "test.v", "test.v")
	}
}