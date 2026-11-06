package dissect

import (
	"strconv"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestExcelCompassIntWritesNumericCells(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	c := newExcelCompass(f, "Sheet1")

	values := []int{0, 42, -7, 1 << 40}
	for _, n := range values {
		c.Int(n).Down(1)
	}

	for i, want := range values {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		got, err := f.GetCellValue("Sheet1", cell)
		if err != nil {
			t.Fatalf("read %s: %v", cell, err)
		}
		if got != strconv.Itoa(want) {
			t.Errorf("%s = %q, want %d", cell, got, want)
		}
		typ, err := f.GetCellType("Sheet1", cell)
		if err != nil {
			t.Fatalf("type %s: %v", cell, err)
		}
		if typ == excelize.CellTypeSharedString || typ == excelize.CellTypeInlineString {
			t.Errorf("%s stored as text (type %v), want a number", cell, typ)
		}
	}
}
