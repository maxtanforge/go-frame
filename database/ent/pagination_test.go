package entdb_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/stretchr/testify/require"
)

type queryCall struct {
	filter        string
	offset, limit int
	count         bool
}
type pageQuery struct {
	filter           string
	offset, limit    int
	calls            *[]queryCall
	rows             []int
	total            int
	countErr, allErr error
	afterCount       func()
}

func (q *pageQuery) Clone() *pageQuery       { c := *q; return &c }
func (q *pageQuery) Offset(n int) *pageQuery { q.offset = n; return q }
func (q *pageQuery) Limit(n int) *pageQuery  { q.limit = n; return q }
func (q *pageQuery) Count(context.Context) (int, error) {
	*q.calls = append(*q.calls, queryCall{filter: q.filter, offset: q.offset, limit: q.limit, count: true})
	if q.afterCount != nil {
		q.afterCount()
	}
	return q.total, q.countErr
}
func (q *pageQuery) All(context.Context) ([]int, error) {
	*q.calls = append(*q.calls, queryCall{filter: q.filter, offset: q.offset, limit: q.limit})
	return q.rows, q.allErr
}
func pageWindow(t *testing.T) pagination.Window {
	t.Helper()
	w, e := pagination.NewWindow(2, 2, pagination.DefaultLimits())
	require.NoError(t, e)
	return w
}

func TestQueryPageClonesSameFilter(t *testing.T) {
	var calls []queryCall
	q := &pageQuery{filter: "active", calls: &calls, total: 5, rows: []int{3, 4}}
	page, err := entdb.QueryPage(context.Background(), q, pageWindow(t))
	require.NoError(t, err)
	require.Equal(t, int64(5), page.Total)
	require.Equal(t, int64(3), page.Pages)
	require.Equal(t, []int{3, 4}, page.Records)
	require.Equal(t, []queryCall{{filter: "active", count: true}, {filter: "active", offset: 2, limit: 2}}, calls)
	require.Zero(t, q.offset)
	require.Zero(t, q.limit)
	page.Records[0] = 99
	require.Equal(t, 3, q.rows[0])
}
func TestQueryPageStopsOnFailure(t *testing.T) {
	var calls []queryCall
	failure := errors.New("count failed")
	q := &pageQuery{calls: &calls, countErr: failure}
	_, err := entdb.QueryPage(context.Background(), q, pageWindow(t))
	require.ErrorIs(t, err, failure)
	require.Len(t, calls, 1)
	calls = nil
	q.countErr = nil
	q.total = -1
	_, err = entdb.QueryPage(context.Background(), q, pageWindow(t))
	require.Error(t, err)
	require.Len(t, calls, 1)
	calls = nil
	q.total = 0
	q.allErr = failure
	_, err = entdb.QueryPage(context.Background(), q, pageWindow(t))
	require.ErrorIs(t, err, failure)
	require.Len(t, calls, 2)
}
func TestQueryPageCancellationAndInvalidInput(t *testing.T) {
	var calls []queryCall
	ctx, cancel := context.WithCancel(context.Background())
	q := &pageQuery{calls: &calls, afterCount: cancel}
	_, err := entdb.QueryPage(ctx, q, pageWindow(t))
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, calls, 1)
	calls = nil
	_, err = entdb.QueryPage(ctx, q, pageWindow(t))
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, calls)
	_, err = entdb.QueryPage(context.Background(), q, pagination.Window{})
	require.ErrorIs(t, err, pagination.ErrInvalid)
	require.Empty(t, calls)
	_, err = entdb.QueryPage(context.Background(), (*pageQuery)(nil), pageWindow(t))
	require.Error(t, err)
}
func TestQuerySliceDoesNotCount(t *testing.T) {
	var calls []queryCall
	q := &pageQuery{calls: &calls, rows: []int{3, 4, 5}}
	slice, err := entdb.QuerySlice(context.Background(), q, pageWindow(t))
	require.NoError(t, err)
	require.True(t, slice.HasMore)
	require.Equal(t, []int{3, 4}, slice.Records)
	require.Equal(t, []queryCall{{offset: 2, limit: 3}}, calls)
	require.Zero(t, q.limit)
	q.rows = nil
	page, err := entdb.QueryPage(context.Background(), q, pageWindow(t))
	require.NoError(t, err)
	require.NotNil(t, page.Records)
}

func TestQueryPagePlatformIntegerBounds(t *testing.T) {
	var calls []queryCall
	q := &pageQuery{calls: &calls}
	w, err := pagination.NewWindow(2, 1<<31, pagination.Limits{MaxSize: 1 << 31, MaxOffset: 1 << 32})
	require.NoError(t, err)
	_, err = entdb.QueryPage(context.Background(), q, w)
	if strconv.IntSize == 32 {
		require.ErrorIs(t, err, pagination.ErrInvalid)
		require.Empty(t, calls)
	} else {
		require.NoError(t, err)
		require.Len(t, calls, 2)
		require.Equal(t, int64(1<<31), int64(calls[1].offset))
	}
	calls = nil
	w, err = pagination.NewWindow(1, (1<<31)-1, pagination.Limits{MaxSize: (1 << 31) - 1, MaxOffset: 0})
	require.NoError(t, err)
	_, err = entdb.QuerySlice(context.Background(), q, w)
	if strconv.IntSize == 32 {
		require.ErrorIs(t, err, pagination.ErrInvalid)
		require.Empty(t, calls)
	} else {
		require.NoError(t, err)
		require.Len(t, calls, 1)
		require.Equal(t, int64(1<<31), int64(calls[0].limit))
	}
}
