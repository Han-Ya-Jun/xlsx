package xlsx

import (
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/araddon/dateparse"

	"github.com/bingoohuang/gor"
	"github.com/sirupsen/logrus"
	"github.com/unidoc/unioffice/spreadsheet"
)

// T just for tag for convenience to declare some tags for the whole structure.
type T interface{ t() }

// Xlsx is the structure for xlsx processing.
type Xlsx struct {
	workbook     *spreadsheet.Workbook
	currentSheet spreadsheet.Sheet
	option       *Option
	rowsWritten  int
}

func (x *Xlsx) hasInput() bool { return x.option.TemplateFile != "" || x.option.InputFile != "" }

// New creates a new instance of Xlsx.
func New(optionFns ...OptionFn) *Xlsx {
	xlsx := &Xlsx{
		option: createOption(optionFns),
	}

	var err error

	if t := xlsx.option.TemplateFile; t != "" {
		if xlsx.workbook, err = spreadsheet.Open(t); err != nil {
			logrus.Warnf("failed to open template file %s: %v", t, err)
		}
	}

	if t := xlsx.option.InputFile; t != "" {
		if xlsx.workbook, err = spreadsheet.Open(t); err != nil {
			logrus.Warnf("failed to open input file %s: %v", t, err)
		}
	}

	if xlsx.workbook == nil {
		xlsx.workbook = spreadsheet.New()
	}

	return xlsx
}

func createOption(optionFns []OptionFn) *Option {
	option := &Option{}

	for _, fn := range optionFns {
		fn(option)
	}

	return option
}

// Option defines the option for the xlsx processing.
type Option struct {
	TemplateFile string
	InputFile    string
}

// OptionFn defines the func to change the option.
type OptionFn func(*Option)

// WithTemplate defines the template excel file for writing template.
func WithTemplate(f string) OptionFn { return func(o *Option) { o.TemplateFile = f } }

// WithInputFile defines the input excel file for reading.
func WithInputFile(f string) OptionFn { return func(o *Option) { o.InputFile = f } }

// Write Writes beans to the underlying xlsx.
func (x *Xlsx) Write(beans interface{}) {
	beanReflectValue := reflect.ValueOf(beans)
	beanType := beanReflectValue.Type()
	isSlice := beanReflectValue.Kind() == reflect.Slice

	if isSlice {
		beanType = beanReflectValue.Type().Elem()
	}

	if beanType.Kind() == reflect.Ptr {
		beanType = beanType.Elem()
	}

	ttag := findTTag(beanType)
	x.currentSheet = x.createSheet(ttag, false)

	fields := collectExportableFields(beanType)
	titles, customizedTitle := collectTitles(fields)
	location := x.locateTitles(fields, titles, false)
	customizedTitle = customizedTitle && !location.isValid()

	if writeTitle := customizedTitle || ttag.Get("title") != ""; writeTitle {
		x.writeTitles(fields, titles)
	}

	if location.isValid() {
		x.rowsWritten = 0

		if isSlice {
			for i := 0; i < beanReflectValue.Len(); i++ {
				x.writeTemplateRow(location, beanReflectValue.Index(i))
			}
		} else {
			x.writeTemplateRow(location, beanReflectValue)
		}

		x.removeTempleRows(location)

		return
	}

	if isSlice {
		for i := 0; i < beanReflectValue.Len(); i++ {
			x.writeRow(fields, beanReflectValue.Index(i))
		}
	} else {
		x.writeRow(fields, beanReflectValue)
	}
}

func (x *Xlsx) Read(slicePtr interface{}) error {
	v := reflect.ValueOf(slicePtr)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Slice {
		return errors.New("the input argument should be a pointer of slice")
	}

	beanType := v.Elem().Type().Elem()

	ttag := findTTag(beanType)
	x.currentSheet = x.createSheet(ttag, true)

	fields := collectExportableFields(beanType)
	titles, _ := collectTitles(fields)
	location := x.locateTitles(fields, titles, true)

	if location.isValid() {
		slice, err := x.readRows(beanType, location)
		if err != nil {
			return err
		}

		v.Elem().Set(slice)
	}

	return nil
}

func (x *Xlsx) readRows(beanType reflect.Type, l templateLocation) (reflect.Value, error) {
	slice := reflect.MakeSlice(reflect.SliceOf(beanType), 0, len(l.templateRows))

	for _, row := range l.templateRows {
		rowBean, err := x.createRowBean(beanType, l, row)
		if err != nil {
			return reflect.Value{}, err
		}

		slice = reflect.Append(slice, rowBean)
	}

	return slice, nil
}

func (x *Xlsx) createRowBean(beanType reflect.Type, l templateLocation, row spreadsheet.Row) (reflect.Value, error) {
	rowBean := reflect.New(beanType).Elem()

	for _, cell := range l.templateCells {
		c := row.Cell(cell.cellColumn)
		if c.IsEmpty() {
			continue
		}

		sf := cell.structField
		f := rowBean.FieldByIndex(sf.Index)
		s := c.GetString()

		if sf.Type == timeType {
			t, err := parseTime(sf, s)
			if err != nil {
				return reflect.Value{}, err
			}

			f.Set(reflect.ValueOf(t))

			continue
		}

		v, err := gor.CastAny(s, sf.Type)

		if err != nil && sf.Tag.Get("omiterr") != "true" {
			return reflect.Value{}, err
		}

		f.Set(v)
	}

	return rowBean, nil
}

func parseTime(sf reflect.StructField, s string) (time.Time, error) {
	if f := sf.Tag.Get("format"); f != "" {
		return time.ParseInLocation(ConvertLayout(f), s, time.Local)
	}

	return dateparse.ParseLocal(s)
}

func (x *Xlsx) createSheet(ttag reflect.StructTag, readonly bool) spreadsheet.Sheet {
	sheetName := ttag.Get("sheet")
	wbSheet := spreadsheet.Sheet{}

	if x.hasInput() {
		for _, sheet := range x.workbook.Sheets() {
			if strings.Contains(sheet.Name(), sheetName) {
				return sheet
			}
		}

		if len(x.workbook.Sheets()) > 0 {
			wbSheet = x.workbook.Sheets()[0]
		}
	}

	if readonly {
		return wbSheet
	}

	if !wbSheet.IsValid() {
		wbSheet = x.workbook.AddSheet()
	}

	if sheetName != "" && !strings.Contains(wbSheet.Name(), sheetName) {
		wbSheet.SetName(sheetName)
	}

	return wbSheet
}

func collectTitles(fields []reflect.StructField) ([]string, bool) {
	titles := make([]string, 0)
	customizedTitle := false

	for _, f := range fields {
		if t := f.Tag.Get("title"); t != "" {
			customizedTitle = true

			titles = append(titles, t)
		} else {
			titles = append(titles, f.Name)
		}
	}

	return titles, customizedTitle
}

func collectExportableFields(t reflect.Type) []reflect.StructField {
	fields := make([]reflect.StructField, 0, t.NumField())

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)

		if f.PkgPath != "" || f.Type == tType {
			continue
		}

		fields = append(fields, f)
	}

	return fields
}

func findTTag(t reflect.Type) reflect.StructTag {
	for i := 0; i < t.NumField(); i++ {
		if f := t.Field(i); f.Type == tType {
			return f.Tag
		}
	}

	return ""
}

// Save persists to the excel file.
func (x *Xlsx) Save(file string) error {
	return x.workbook.SaveToFile(file)
}

func (x *Xlsx) writeRow(fields []reflect.StructField, value reflect.Value) {
	row := x.currentSheet.AddRow()

	for _, field := range fields {
		setCellValue(row.AddCell(), field, value)
	}
}

func setCellValue(cell spreadsheet.Cell, field reflect.StructField, value reflect.Value) {
	v := value.FieldByIndex(field.Index).Interface()

	switch fv := v.(type) {
	case time.Time:
		if format := field.Tag.Get("format"); format != "" {
			cell.SetString(fv.Format(ConvertLayout(format)))
		} else {
			cell.SetTime(fv)
		}
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		cell.SetNumber(convertToFloat64(v))
	case string:
		cell.SetString(fv)
	}
}

func (x *Xlsx) writeTitles(fields []reflect.StructField, titles []string) {
	row := x.currentSheet.AddRow()

	for i := range fields {
		cell := row.AddCell()
		cell.SetString(titles[i])
	}
}

type templateLocation struct {
	titledRowIndex int
	rowsEndIndex   int
	templateCells  []templateCell
	templateRows   []spreadsheet.Row
}

type templateCell struct {
	cellColumn  string
	structField reflect.StructField
}

func (t *templateLocation) isValid() bool {
	return len(t.templateCells) > 0
}

func (x *Xlsx) locateTitles(fields []reflect.StructField, titles []string, forread bool) templateLocation {
	if !x.hasInput() {
		return templateLocation{}
	}

	rows := x.currentSheet.Rows()
	titledRowIndex, templateCells := x.findTemplateTitledRow(fields, titles, rows)
	templateRows := x.findTemplateRows(titledRowIndex, templateCells, rows, forread)

	return templateLocation{
		titledRowIndex: titledRowIndex,
		templateRows:   templateRows,
		templateCells:  templateCells,
		rowsEndIndex:   len(rows),
	}
}

func (x *Xlsx) findTemplateTitledRow(fields []reflect.StructField,
	titles []string, rows []spreadsheet.Row) (int, []templateCell) {
	titledRowIndex := -1
	templateCells := make([]templateCell, 0, len(fields))

	for rowIndex, row := range rows {
		for _, cell := range row.Cells() {
			cellString := cell.GetString()

			for i, title := range titles {
				if !strings.Contains(cellString, title) {
					continue
				}

				col, err := cell.Column()
				if err != nil {
					logrus.Warnf("failed to get column error: %v", err)
					continue
				}

				templateCells = append(templateCells, templateCell{
					cellColumn:  col,
					structField: fields[i],
				})

				break
			}
		}

		if len(templateCells) > 0 {
			titledRowIndex = rowIndex
			break
		}
	}

	return titledRowIndex, templateCells
}

func (x *Xlsx) findTemplateRows(titledRowIndex int,
	templateCells []templateCell, rows []spreadsheet.Row, forread bool) []spreadsheet.Row {
	templateRows := make([]spreadsheet.Row, 0)

	if titledRowIndex < 0 {
		return templateRows
	}

	col := templateCells[0].cellColumn

	for i := titledRowIndex + 1; i < len(rows); i++ {
		if forread || strings.Contains(rows[i].Cell(col).GetString(), "template") {
			templateRows = append(templateRows, rows[i])
		} else if len(templateRows) == 0 {
			return append(templateRows, rows[i])
		}
	}

	return templateRows
}

func (x *Xlsx) writeTemplateRow(l templateLocation, v reflect.Value) {
	// 2 是为了计算row num(1-N), 从标题行(T+1)的下一行（T+2)开始写
	num := uint32(l.titledRowIndex + 2 + x.rowsWritten)
	x.rowsWritten++
	row := x.currentSheet.Row(num)

	for _, tc := range l.templateCells {
		setCellValue(row.Cell(tc.cellColumn), tc.structField, v)
	}

	x.copyRowStyle(l, row)
}

func (x *Xlsx) copyRowStyle(l templateLocation, row spreadsheet.Row) {
	if len(l.templateRows) == 0 || x.rowsWritten < len(l.templateRows) {
		return
	}

	templateRow := l.templateRows[(x.rowsWritten-1)%len(l.templateRows)]

	for _, tc := range l.templateCells { // copying cell style
		if cx := templateRow.Cell(tc.cellColumn).X(); cx.SAttr != nil {
			if style := x.workbook.StyleSheet.GetCellStyle(*cx.SAttr); !style.IsEmpty() {
				row.Cell(tc.cellColumn).SetStyle(style)
			}
		}
	}
}

func (x *Xlsx) removeTempleRows(l templateLocation) {
	if len(l.templateRows) == 0 {
		return // no template rows in the template file.
	}

	sd := x.currentSheet.X().CT_Worksheet.SheetData
	rows := sd.Row

	if endIndex := l.titledRowIndex + 1 + x.rowsWritten; endIndex < len(rows) {
		sd.Row = rows[:endIndex]
	}
}

// nolint gochecknoglobals
var (
	tType    = reflect.TypeOf((*T)(nil)).Elem()
	timeType = reflect.TypeOf((*time.Time)(nil)).Elem()
)

// ConvertLayout converts the time format in java to golang.
func ConvertLayout(layout string) string {
	lo := layout
	lo = strings.Replace(lo, "yyyy", "2006", -1)
	lo = strings.Replace(lo, "yy", "06", -1)
	lo = strings.Replace(lo, "MM", "01", -1)
	lo = strings.Replace(lo, "dd", "02", -1)
	lo = strings.Replace(lo, "HH", "15", -1)
	lo = strings.Replace(lo, "mm", "04", -1)
	lo = strings.Replace(lo, "ss", "05", -1)
	lo = strings.Replace(lo, "SSS", "000", -1)

	return lo
}

func convertToFloat64(v interface{}) float64 {
	switch fv := v.(type) {
	case int:
		return float64(fv)
	case int8:
		return float64(fv)
	case int16:
		return float64(fv)
	case int32:
		return float64(fv)
	case int64:
		return float64(fv)
	case uint:
		return float64(fv)
	case uint8:
		return float64(fv)
	case uint16:
		return float64(fv)
	case uint32:
		return float64(fv)
	case uint64:
		return float64(fv)
	case float32:
		return float64(fv)
	case float64:
		return fv
	}

	return 0
}
