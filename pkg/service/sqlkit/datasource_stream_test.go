package sqlkit

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/example/go-frame/pkg/class/const/sqlconst"
	"github.com/jmoiron/sqlx"
)

// QueryStreamMap 免 DB 单测：内存版 fake driver 返回固定结果集，
// 验证逐行回调顺序、提前终止与迭代错误上抛三条路径。

var (
	fakeRowsProvider func() driver.Rows
	fakeRegOnce      sync.Once
	errFakeIter      = errors.New("fake: iterator error")
)

func registerFakeDriver() {
	fakeRegOnce.Do(func() { sql.Register("sqlkit-fake", fakeDriver{}) })
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{}, nil }

type fakeConn struct{}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) { return &fakeStmt{}, nil }
func (c *fakeConn) Close() error                              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)                 { return nil, errors.New("tx not supported") }

type fakeStmt struct{}

func (s *fakeStmt) Close() error                               { return nil }
func (s *fakeStmt) NumInput() int                              { return -1 }
func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) { return driver.RowsAffected(0), nil }
func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	if fakeRowsProvider == nil {
		return nil, errors.New("no rows configured")
	}
	return fakeRowsProvider(), nil
}

// fakeRows 固定列 + 逐行数据，第 errAt 行（1-based，0 表示不注入错误）返回迭代错误
type fakeRows struct {
	cols  []string
	data  [][]driver.Value
	i     int
	errAt int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.errAt > 0 && r.i+1 == r.errAt {
		return errFakeIter
	}
	if r.i >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.i])
	r.i++
	return nil
}

func newFakeDS(t *testing.T) *DataSource {
	t.Helper()
	registerFakeDriver()
	db, err := sqlx.Open("sqlkit-fake", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &DataSource{Driver: sqlconst.Mysql, DBPool: db}
}

func streamTestRows() [][]driver.Value {
	return [][]driver.Value{
		{int64(1), "a"},
		{int64(2), "b"},
		{int64(3), "c"},
	}
}

func TestQueryStreamMapAllRows(t *testing.T) {
	ds := newFakeDS(t)
	fakeRowsProvider = func() driver.Rows {
		return &fakeRows{cols: []string{"id", "name"}, data: streamTestRows()}
	}
	var got [][]any
	err := ds.QueryStreamMap("select id, name from t", func(row map[string]any) error {
		got = append(got, []any{row["id"], row["name"]})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("行数错误: %d", len(got))
	}
	// 逐行顺序回调：id 必须按 1/2/3 到达（流式语义的核心断言）
	for i, want := range []int64{1, 2, 3} {
		if got[i][0] != want {
			t.Fatalf("第 %d 行 id = %v, want %d", i, got[i][0], want)
		}
	}
	if got[1][1] != "b" {
		t.Fatalf("第 2 行 name = %v", got[1][1])
	}
}

func TestQueryStreamMapEarlyStop(t *testing.T) {
	ds := newFakeDS(t)
	fakeRowsProvider = func() driver.Rows {
		return &fakeRows{cols: []string{"id", "name"}, data: streamTestRows()}
	}
	visited := 0
	errStop := errors.New("stop here")
	err := ds.QueryStreamMap("select id, name from t", func(row map[string]any) error {
		visited++
		if visited == 2 {
			return errStop
		}
		return nil
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("提前终止的 error 应原样返回: %v", err)
	}
	if visited != 2 {
		t.Fatalf("终止后不应继续回调: visited=%d", visited)
	}
}

func TestQueryStreamMapIteratorError(t *testing.T) {
	ds := newFakeDS(t)
	fakeRowsProvider = func() driver.Rows {
		return &fakeRows{cols: []string{"id"}, data: streamTestRows()[:1], errAt: 2}
	}
	visited := 0
	err := ds.QueryStreamMap("select id from t", func(row map[string]any) error {
		visited++
		return nil
	})
	if !errors.Is(err, errFakeIter) {
		t.Fatalf("迭代错误应上抛而不是静默成功: %v", err)
	}
	if visited != 1 {
		t.Fatalf("错误行之前的回调应已发生: visited=%d", visited)
	}
}
