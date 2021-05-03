package bun

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/uptrace/bun/dialect/feature"
	"github.com/uptrace/bun/sqlfmt"
)

type mapSliceModel struct {
	mapModel
	slicePtr *[]map[string]interface{}

	keys []string
}

var _ model = (*mapSliceModel)(nil)

func newMapSliceModel(db *DB, ptr *[]map[string]interface{}) *mapSliceModel {
	return &mapSliceModel{
		mapModel: mapModel{
			db: db,
		},
		slicePtr: ptr,
	}
}

func (m *mapSliceModel) ScanRows(ctx context.Context, rows *sql.Rows) (int, error) {
	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}

	m.columns = columns
	dest := makeDest(m, len(columns))

	slice := *m.slicePtr
	if len(slice) > 0 {
		slice = slice[:0]
	}

	var n int

	for rows.Next() {
		m.m = make(map[string]interface{}, len(columns))

		m.scanIndex = 0
		if err := rows.Scan(dest...); err != nil {
			return 0, err
		}

		slice = append(slice, m.m)
		n++
	}

	*m.slicePtr = slice
	return n, nil
}

func (m *mapSliceModel) appendColumns(fmter sqlfmt.QueryFormatter, b []byte) (_ []byte, err error) {
	if err := m.initKeys(); err != nil {
		return nil, err
	}

	for i, k := range m.keys {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = sqlfmt.AppendIdent(fmter, b, k)
	}

	return b, nil
}

func (m *mapSliceModel) appendValues(fmter sqlfmt.QueryFormatter, b []byte) (_ []byte, err error) {
	if err := m.initKeys(); err != nil {
		return nil, err
	}
	slice := *m.slicePtr

	b = append(b, "VALUES "...)
	if m.db.features.Has(feature.ValuesRow) {
		b = append(b, "ROW("...)
	} else {
		b = append(b, '(')
	}

	if sqlfmt.IsNopFormatter(fmter) {
		for i := range m.keys {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = append(b, '?')
		}
		return b, nil
	}

	for i, el := range slice {
		if i > 0 {
			b = append(b, "), "...)
			if m.db.features.Has(feature.ValuesRow) {
				b = append(b, "ROW("...)
			} else {
				b = append(b, '(')
			}
		}

		for j, key := range m.keys {
			if j > 0 {
				b = append(b, ", "...)
			}
			b = sqlfmt.Append(fmter, b, el[key])
		}
	}

	b = append(b, ')')

	return b, nil
}

func (m *mapSliceModel) initKeys() error {
	if m.keys != nil {
		return nil
	}

	slice := *m.slicePtr
	if len(slice) == 0 {
		return errors.New("bun: map slice is empty")
	}

	first := slice[0]
	keys := make([]string, 0, len(first))

	for k := range first {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	m.keys = keys

	return nil
}