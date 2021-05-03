package bun

import (
	"fmt"
	"reflect"
	"strconv"

	"github.com/uptrace/bun/dialect/feature"
	"github.com/uptrace/bun/schema"
	"github.com/uptrace/bun/sqlfmt"
)

type ValuesQuery struct {
	baseQuery
	customValueQuery

	withOrder bool
}

func NewValuesQuery(db *DB, model interface{}) *ValuesQuery {
	q := &ValuesQuery{
		baseQuery: baseQuery{
			db:  db,
			dbi: db.DB,
		},
	}
	q.setTableModel(model)
	return q
}

func (q *ValuesQuery) DB(db DBI) *ValuesQuery {
	q.dbi = db
	return q
}

func (q *ValuesQuery) WithOrder() *ValuesQuery {
	q.withOrder = true
	return q
}

func (q *ValuesQuery) AppendArg(fmter sqlfmt.QueryFormatter, b []byte, name string) ([]byte, bool) {
	switch name {
	case "Columns":
		bb, err := q.AppendColumns(fmter, b)
		if err != nil {
			q.setErr(err)
			return b, true
		}
		return bb, true
	}
	return b, false
}

// AppendColumns appends the table columns. It is used by CTE.
func (q *ValuesQuery) AppendColumns(fmter sqlfmt.QueryFormatter, b []byte) (_ []byte, err error) {
	if q.err != nil {
		return nil, q.err
	}
	if q.model == nil {
		return nil, errModelNil
	}

	if q.tableModel != nil {
		fields, err := q.getFields()
		if err != nil {
			return nil, err
		}

		b = appendColumns(b, "", fields)

		if q.withOrder {
			b = append(b, ", _order"...)
		}

		return b, nil
	}

	switch model := q.model.(type) {
	case *mapSliceModel:
		return model.appendColumns(fmter, b)
	}

	return nil, fmt.Errorf("bun: Values does not support %T", q.model)
}

func (q *ValuesQuery) AppendQuery(fmter sqlfmt.QueryFormatter, b []byte) (_ []byte, err error) {
	if q.err != nil {
		return nil, q.err
	}
	if q.model == nil {
		return nil, errModelNil
	}

	fmter = formatterWithModel(fmter, q)

	if q.tableModel != nil {
		fields, err := q.getFields()
		if err != nil {
			return nil, err
		}
		return q.appendQuery(fmter, b, fields)
	}

	switch model := q.model.(type) {
	case *mapSliceModel:
		return model.appendValues(fmter, b)
	}

	return nil, fmt.Errorf("bun: Values does not support %T", q.model)
}

func (q *ValuesQuery) appendQuery(
	fmter sqlfmt.QueryFormatter,
	b []byte,
	fields []*schema.Field,
) (_ []byte, err error) {
	b = append(b, "VALUES "...)
	if q.db.features.Has(feature.ValuesRow) {
		b = append(b, "ROW("...)
	} else {
		b = append(b, '(')
	}

	switch model := q.tableModel.(type) {
	case *structTableModel:
		b, err = q.appendValues(fmter, b, fields, model.strct)
		if err != nil {
			return nil, err
		}

		if q.withOrder {
			b = append(b, ", "...)
			b = strconv.AppendInt(b, 0, 10)
		}
	case *sliceTableModel:
		slice := model.slice
		sliceLen := slice.Len()
		for i := 0; i < sliceLen; i++ {
			if i > 0 {
				b = append(b, "), ("...)
			}

			b, err = q.appendValues(fmter, b, fields, slice.Index(i))
			if err != nil {
				return nil, err
			}

			if q.withOrder {
				b = append(b, ", "...)
				b = strconv.AppendInt(b, int64(i), 10)
			}
		}
	default:
		return nil, fmt.Errorf("bun: Values does not support %T", q.model)
	}

	b = append(b, ')')

	return b, nil
}

func (q *ValuesQuery) appendValues(
	fmter sqlfmt.QueryFormatter, b []byte, fields []*schema.Field, strct reflect.Value,
) (_ []byte, err error) {
	isTemplate := sqlfmt.IsNopFormatter(fmter)
	for i, f := range fields {
		if i > 0 {
			b = append(b, ", "...)
		}

		app, ok := q.modelValues[f.Name]
		if ok {
			b, err = app.AppendQuery(fmter, b)
			if err != nil {
				return nil, err
			}
			continue
		}

		if isTemplate {
			b = append(b, '?')
		} else {
			b = f.AppendValue(fmter, b, indirect(strct))
		}

		b = append(b, "::"...)
		b = append(b, f.UserSQLType...)
	}
	return b, nil
}