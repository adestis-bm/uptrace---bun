package bun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/uptrace/bun/internal"
	"github.com/uptrace/bun/schema"
	"github.com/uptrace/bun/sqlfmt"
)

type union struct {
	expr  string
	query *SelectQuery
}

type SelectQuery struct {
	whereBaseQuery

	distinctOn []sqlfmt.QueryWithArgs
	joins      []joinQuery
	group      []sqlfmt.QueryWithArgs
	having     []sqlfmt.QueryWithArgs
	order      []sqlfmt.QueryWithArgs
	limit      int32
	offset     int32
	selFor     sqlfmt.QueryWithArgs

	union []union
}

func NewSelectQuery(db *DB) *SelectQuery {
	return &SelectQuery{
		whereBaseQuery: whereBaseQuery{
			baseQuery: baseQuery{
				db:  db,
				dbi: db.DB,
			},
		},
	}
}

func (q *SelectQuery) Tx(db DBI) *SelectQuery {
	q.dbi = db
	return q
}

func (q *SelectQuery) Model(model interface{}) *SelectQuery {
	q.setTableModel(model)
	return q
}

// Apply calls the fn passing the SelectQuery as an argument.
func (q *SelectQuery) Apply(fn func(*SelectQuery) *SelectQuery) *SelectQuery {
	return fn(q)
}

func (q *SelectQuery) With(name string, query sqlfmt.QueryAppender) *SelectQuery {
	q.addWith(name, query)
	return q
}

func (q *SelectQuery) Distinct() *SelectQuery {
	q.distinctOn = make([]sqlfmt.QueryWithArgs, 0)
	return q
}

func (q *SelectQuery) DistinctOn(query string, args ...interface{}) *SelectQuery {
	q.distinctOn = append(q.distinctOn, sqlfmt.SafeQuery(query, args))
	return q
}

func (q *SelectQuery) Table(tables ...string) *SelectQuery {
	for _, table := range tables {
		q.addTable(sqlfmt.UnsafeIdent(table))
	}
	return q
}

func (q *SelectQuery) TableExpr(query string, args ...interface{}) *SelectQuery {
	q.addTable(sqlfmt.SafeQuery(query, args))
	return q
}

func (q *SelectQuery) ModelTableExpr(query string, args ...interface{}) *SelectQuery {
	q.modelTable = sqlfmt.SafeQuery(query, args)
	return q
}

func (q *SelectQuery) Column(columns ...string) *SelectQuery {
	for _, column := range columns {
		q.addColumn(sqlfmt.UnsafeIdent(column))
	}
	return q
}

func (q *SelectQuery) ColumnExpr(query string, args ...interface{}) *SelectQuery {
	q.addColumn(sqlfmt.SafeQuery(query, args))
	return q
}

func (q *SelectQuery) ExcludeColumn(columns ...string) *SelectQuery {
	q.excludeColumn(columns)
	return q
}

//------------------------------------------------------------------------------

func (q *SelectQuery) Where(query string, args ...interface{}) *SelectQuery {
	q.addWhere(sqlfmt.SafeQueryWithSep(query, args, " AND "))
	return q
}

func (q *SelectQuery) WhereOr(query string, args ...interface{}) *SelectQuery {
	q.addWhere(sqlfmt.SafeQueryWithSep(query, args, " OR "))
	return q
}

func (q *SelectQuery) WhereGroup(sep string, fn func(*WhereQuery)) *SelectQuery {
	q.addWhereGroup(sep, fn)
	return q
}

// WherePK adds conditions based on the model primary keys.
// Usually it is the same as:
//
//    Where("id = ?id")
func (q *SelectQuery) WherePK() *SelectQuery {
	q.flags = q.flags.Set(wherePKFlag)
	return q
}

func (q *SelectQuery) WhereDeleted() *SelectQuery {
	q.whereDeleted()
	return q
}

func (q *SelectQuery) WhereAllWithDeleted() *SelectQuery {
	q.whereAllWithDeleted()
	return q
}

//------------------------------------------------------------------------------

func (q *SelectQuery) Group(columns ...string) *SelectQuery {
	for _, column := range columns {
		q.group = append(q.group, sqlfmt.UnsafeIdent(column))
	}
	return q
}

func (q *SelectQuery) GroupExpr(group string, args ...interface{}) *SelectQuery {
	q.group = append(q.group, sqlfmt.SafeQuery(group, args))
	return q
}

func (q *SelectQuery) Having(having string, args ...interface{}) *SelectQuery {
	q.having = append(q.having, sqlfmt.SafeQuery(having, args))
	return q
}

func (q *SelectQuery) Order(columns ...string) *SelectQuery {
	for _, column := range columns {
		q.order = append(q.order, sqlfmt.UnsafeIdent(column))
	}
	return q
}

func (q *SelectQuery) OrderExpr(query string, args ...interface{}) *SelectQuery {
	q.order = append(q.order, sqlfmt.SafeQuery(query, args))
	return q
}

func (q *SelectQuery) Limit(n int) *SelectQuery {
	q.limit = int32(n)
	return q
}

func (q *SelectQuery) Offset(n int) *SelectQuery {
	q.offset = int32(n)
	return q
}

func (q *SelectQuery) For(s string, args ...interface{}) *SelectQuery {
	q.selFor = sqlfmt.SafeQuery(s, args)
	return q
}

//------------------------------------------------------------------------------

func (q *SelectQuery) Union(other *SelectQuery) *SelectQuery {
	return q.addUnion(" UNION ", other)
}

func (q *SelectQuery) UnionAll(other *SelectQuery) *SelectQuery {
	return q.addUnion(" UNION ALL ", other)
}

func (q *SelectQuery) Intersect(other *SelectQuery) *SelectQuery {
	return q.addUnion(" INTERSECT ", other)
}

func (q *SelectQuery) IntersectAll(other *SelectQuery) *SelectQuery {
	return q.addUnion(" INTERSECT ALL ", other)
}

func (q *SelectQuery) Except(other *SelectQuery) *SelectQuery {
	return q.addUnion(" EXCEPT ", other)
}

func (q *SelectQuery) ExceptAll(other *SelectQuery) *SelectQuery {
	return q.addUnion(" EXCEPT ALL ", other)
}

func (q *SelectQuery) addUnion(expr string, other *SelectQuery) *SelectQuery {
	q.union = append(q.union, union{
		expr:  expr,
		query: other,
	})
	return q
}

//------------------------------------------------------------------------------

func (q *SelectQuery) Join(join string, args ...interface{}) *SelectQuery {
	q.joins = append(q.joins, joinQuery{
		join: sqlfmt.SafeQuery(join, args),
	})
	return q
}

func (q *SelectQuery) JoinOn(cond string, args ...interface{}) *SelectQuery {
	return q.joinOn(cond, args, " AND ")
}

func (q *SelectQuery) JoinOnOr(cond string, args ...interface{}) *SelectQuery {
	return q.joinOn(cond, args, " OR ")
}

func (q *SelectQuery) joinOn(cond string, args []interface{}, sep string) *SelectQuery {
	if len(q.joins) == 0 {
		q.err = errors.New("bun: query has no joins")
		return q
	}
	j := &q.joins[len(q.joins)-1]
	j.on = append(j.on, sqlfmt.SafeQueryWithSep(cond, args, sep))
	return q
}

//------------------------------------------------------------------------------

// Relation adds a relation to the query. Relation name can be:
//   - RelationName to select all columns,
//   - RelationName.column_name,
//   - RelationName._ to join relation without selecting relation columns.
func (q *SelectQuery) Relation(name string, apply ...func(*SelectQuery) *SelectQuery) *SelectQuery {
	var fn func(*SelectQuery) *SelectQuery

	if len(apply) == 1 {
		fn = apply[0]
	} else if len(apply) > 1 {
		panic("only one apply function is supported")
	}

	join := q.tableModel.Join(name, fn)
	if join == nil {
		q.err = fmt.Errorf("%s does not have relation=%q", q.table, name)
		return q
	}

	if fn == nil {
		return q
	}

	switch join.Relation.Type {
	case schema.HasOneRelation, schema.BelongsToRelation:
		return q
	default:
		return q
	}
}

func (q *SelectQuery) forEachHasOneJoin(fn func(*join) error) error {
	if q.tableModel == nil {
		return nil
	}
	return q._forEachHasOneJoin(fn, q.tableModel.GetJoins())
}

func (q *SelectQuery) _forEachHasOneJoin(fn func(*join) error, joins []join) error {
	for i := range joins {
		j := &joins[i]
		switch j.Relation.Type {
		case schema.HasOneRelation, schema.BelongsToRelation:
			if err := fn(j); err != nil {
				return err
			}
			if err := q._forEachHasOneJoin(fn, j.JoinModel.GetJoins()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (q *SelectQuery) selectJoins(ctx context.Context, joins []join) error {
	var err error
	for i := range joins {
		j := &joins[i]
		switch j.Relation.Type {
		case schema.HasOneRelation, schema.BelongsToRelation:
			err = q.selectJoins(ctx, j.JoinModel.GetJoins())
		default:
			err = j.Select(ctx, q.db.NewSelect())
		}
		if err != nil {
			return err
		}
	}
	return nil
}

//------------------------------------------------------------------------------

func (q *SelectQuery) AppendQuery(fmter sqlfmt.QueryFormatter, b []byte) (_ []byte, err error) {
	return q.appendQuery(formatterWithModel(fmter, q), b, false)
}

func (q *SelectQuery) appendQuery(
	fmter sqlfmt.QueryFormatter, b []byte, count bool,
) (_ []byte, err error) {
	if q.err != nil {
		return nil, q.err
	}

	cteCount := count && (len(q.group) > 0 || q.distinctOn != nil)
	if cteCount {
		b = append(b, "WITH _count_wrapper AS ("...)
	}

	if len(q.union) > 0 {
		b = append(b, '(')
	}

	b, err = q.appendWith(fmter, b)
	if err != nil {
		return nil, err
	}

	b = append(b, "SELECT "...)

	if len(q.distinctOn) > 0 {
		b = append(b, "DISTINCT ON ("...)
		for i, app := range q.distinctOn {
			if i > 0 {
				b = append(b, ", "...)
			}
			b, err = app.AppendQuery(fmter, b)
			if err != nil {
				return nil, err
			}
		}
		b = append(b, ") "...)
	} else if q.distinctOn != nil {
		b = append(b, "DISTINCT "...)
	}

	if count && !cteCount {
		b = append(b, "count(*)"...)
	} else {
		b, err = q.appendColumns(fmter, b)
		if err != nil {
			return nil, err
		}
	}

	if q.hasTables() {
		b, err = q.appendTables(fmter, b)
		if err != nil {
			return nil, err
		}
	}

	err = q.forEachHasOneJoin(func(j *join) error {
		b = append(b, ' ')
		b, err = j.appendHasOneJoin(fmter, b, q)
		return err
	})
	if err != nil {
		return nil, err
	}

	for _, j := range q.joins {
		b, err = j.AppendQuery(fmter, b)
		if err != nil {
			return nil, err
		}
	}

	b, err = q.appendWhere(fmter, b)
	if err != nil {
		return nil, err
	}

	if len(q.group) > 0 {
		b = append(b, " GROUP BY "...)
		for i, f := range q.group {
			if i > 0 {
				b = append(b, ", "...)
			}
			b, err = f.AppendQuery(fmter, b)
			if err != nil {
				return nil, err
			}
		}
	}

	if len(q.having) > 0 {
		b = append(b, " HAVING "...)
		for i, f := range q.having {
			if i > 0 {
				b = append(b, " AND "...)
			}
			b = append(b, '(')
			b, err = f.AppendQuery(fmter, b)
			if err != nil {
				return nil, err
			}
			b = append(b, ')')
		}
	}

	if !count {
		b, err = q.appendOrder(fmter, b)
		if err != nil {
			return nil, err
		}

		if q.limit != 0 {
			b = append(b, " LIMIT "...)
			b = strconv.AppendInt(b, int64(q.limit), 10)
		}

		if q.offset != 0 {
			b = append(b, " OFFSET "...)
			b = strconv.AppendInt(b, int64(q.offset), 10)
		}

		if !q.selFor.IsZero() {
			b = append(b, " FOR "...)
			b, err = q.selFor.AppendQuery(fmter, b)
			if err != nil {
				return nil, err
			}
		}
	}

	if len(q.union) > 0 {
		b = append(b, ')')

		for _, u := range q.union {
			b = append(b, u.expr...)
			b = append(b, '(')
			b, err = u.query.AppendQuery(fmter, b)
			if err != nil {
				return nil, err
			}
			b = append(b, ')')
		}
	}

	if cteCount {
		b = append(b, ") SELECT count(*) FROM _count_wrapper"...)
	}

	return b, nil
}

func (q SelectQuery) appendColumns(fmter sqlfmt.QueryFormatter, b []byte) (_ []byte, err error) {
	start := len(b)

	switch {
	case len(q.columns) > 0:
		for i, col := range q.columns {
			if i > 0 {
				b = append(b, ", "...)
			}

			if col.Args == nil {
				if field, ok := q.table.FieldMap[col.Query]; ok {
					b = append(b, q.table.Alias...)
					b = append(b, '.')
					b = append(b, field.SQLName...)
					continue
				}
			}

			b, err = col.AppendQuery(fmter, b)
			if err != nil {
				return nil, err
			}
		}
	case q.table != nil:
		if len(q.table.Fields) > 10 && sqlfmt.IsNopFormatter(fmter) {
			b = append(b, q.table.Alias...)
			b = append(b, '.')
			b = sqlfmt.AppendString(b, fmt.Sprintf("%d columns", len(q.table.Fields)))
		} else {
			b = appendColumns(b, q.table.Alias, q.table.Fields)
		}
	default:
		b = append(b, '*')
	}

	if err := q.forEachHasOneJoin(func(j *join) error {
		if len(b) != start {
			b = append(b, ", "...)
			start = len(b)
		}

		b, err = q.appendHasOneColumns(fmter, b, j)
		if err != nil {
			return err
		}

		return nil
	}); err != nil {
		return nil, err
	}

	b = bytes.TrimSuffix(b, []byte(", "))

	return b, nil
}

func (q *SelectQuery) appendHasOneColumns(
	fmter sqlfmt.QueryFormatter, b []byte, join *join,
) (_ []byte, err error) {
	join.applyQuery(q)

	if len(join.columns) > 0 {
		for i, col := range join.columns {
			if i > 0 {
				b = append(b, ", "...)
			}
			b, err = col.AppendQuery(fmter, b)
			if err != nil {
				return nil, err
			}
		}
		return b, nil
	}

	for i, f := range join.JoinModel.Table().Fields {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = join.appendAlias(fmter, b)
		b = append(b, '.')
		b = append(b, f.SQLName...)
		b = append(b, " AS "...)
		b = join.appendAliasColumn(fmter, b, f.Name)
	}
	return b, nil
}

func (q *SelectQuery) appendTables(fmter sqlfmt.QueryFormatter, b []byte) (_ []byte, err error) {
	b = append(b, " FROM "...)
	startLen := len(b)

	if q.modelHasTableName() {
		if !q.modelTable.IsZero() {
			b, err = q.modelTable.AppendQuery(fmter, b)
			if err != nil {
				return nil, err
			}
		} else {
			b = fmter.FormatQuery(b, string(q.table.SQLNameForSelects))
			if q.table.Alias != q.table.SQLNameForSelects {
				b = append(b, " AS "...)
				b = append(b, q.table.Alias...)
			}
		}
	}

	for _, table := range q.tables {
		if len(b) > startLen {
			b = append(b, ", "...)
		}
		b, err = table.AppendQuery(fmter, b)
		if err != nil {
			return nil, err
		}
	}

	return b, nil
}

func (q *SelectQuery) appendOrder(fmter sqlfmt.QueryFormatter, b []byte) (_ []byte, err error) {
	if len(q.order) > 0 {
		b = append(b, " ORDER BY "...)

		for i, f := range q.order {
			if i > 0 {
				b = append(b, ", "...)
			}
			b, err = f.AppendQuery(fmter, b)
			if err != nil {
				return nil, err
			}
		}

		return b, nil
	}
	return b, nil
}

//------------------------------------------------------------------------------

func (q *SelectQuery) Exec(ctx context.Context, dest ...interface{}) (res Result, err error) {
	queryBytes, err := q.AppendQuery(q.db.fmter, nil)
	if err != nil {
		return res, err
	}
	query := internal.String(queryBytes)

	res, err = q.exec(ctx, q, query)
	if err != nil {
		return res, err
	}

	if q.tableModel != nil {
		if err := q.tableModel.AfterSelect(ctx); err != nil {
			return res, err
		}
	}

	return res, nil
}

func (q *SelectQuery) Scan(ctx context.Context, dest ...interface{}) error {
	queryBytes, err := q.AppendQuery(q.db.fmter, nil)
	if err != nil {
		return err
	}
	query := internal.String(queryBytes)

	res, err := q.scan(ctx, q, query, dest)
	if err != nil {
		return err
	}

	if res.n > 0 && q.tableModel != nil {
		if err := q.selectJoins(ctx, q.tableModel.GetJoins()); err != nil {
			return err
		}
	}

	if q.tableModel != nil {
		if err := q.tableModel.AfterSelect(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (q *SelectQuery) Count(ctx context.Context) (int, error) {
	query, err := q.appendQuery(q.db.fmter, nil, true)
	if err != nil {
		return 0, err
	}

	var num int

	if err := q.db.QueryRowContext(ctx, internal.String(query)).Scan(&num); err != nil {
		return 0, err
	}

	return num, nil
}

func (q *SelectQuery) ScanAndCount(ctx context.Context, dest ...interface{}) (int, error) {
	var count int
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	if q.limit >= 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if err := q.Scan(ctx, dest...); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		var err error
		count, err = q.Count(ctx)
		if err != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}
	}()

	wg.Wait()
	return count, firstErr
}

//------------------------------------------------------------------------------

type joinQuery struct {
	join sqlfmt.QueryWithArgs
	on   []sqlfmt.QueryWithSep
}

func (j *joinQuery) AppendQuery(fmter sqlfmt.QueryFormatter, b []byte) (_ []byte, err error) {
	b = append(b, ' ')

	b, err = j.join.AppendQuery(fmter, b)
	if err != nil {
		return nil, err
	}

	if len(j.on) > 0 {
		b = append(b, " ON "...)
		for i, on := range j.on {
			if i > 0 {
				b = append(b, on.Sep...)
			}

			b = append(b, '(')
			b, err = on.AppendQuery(fmter, b)
			if err != nil {
				return nil, err
			}
			b = append(b, ')')
		}
	}

	return b, nil
}