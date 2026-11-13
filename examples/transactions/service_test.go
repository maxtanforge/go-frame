package transactionexample

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maxtanforge/go-frame/database"
	"github.com/stretchr/testify/require"
)

type mockConnector struct {
	driver driver.Driver
	dsn    string
}

func (c mockConnector) Connect(context.Context) (driver.Conn, error) { return c.driver.Open(c.dsn) }
func (c mockConnector) Driver() driver.Driver                        { return c.driver }
func serviceFixture(t *testing.T) (Service, sqlmock.Sqlmock) {
	t.Helper()
	dsn := fmt.Sprintf("transaction-example-%s", t.Name())
	pool, mock, err := sqlmock.NewWithDSN(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	db, err := database.Open(mockConnector{pool.Driver(), dsn}, database.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return Service{Transactions: SQLTransactions{DB: db}, Store: SQLRepository{}}, mock
}
func TestCreateCommitsBothWritesOrRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			svc, mock := serviceFixture(t)
			boom := errors.New("audit write failed")
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO products").WithArgs("SKU-1", "Tea").WillReturnResult(sqlmock.NewResult(0, 1))
			audit := mock.ExpectExec("INSERT INTO product_events").WithArgs("SKU-1", "created")
			if fail {
				audit.WillReturnError(boom)
				mock.ExpectRollback()
			} else {
				audit.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			err := svc.Create(context.Background(), CreateCommand{SKU: "SKU-1", Name: "Tea"})
			if fail {
				require.ErrorIs(t, err, boom)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
func TestBusinessValidationPrecedesTransaction(t *testing.T) {
	svc, mock := serviceFixture(t)
	require.ErrorIs(t, svc.Create(context.Background(), CreateCommand{SKU: "", Name: "Tea"}), ErrInvalidProduct)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestCreateHandlerMapsDTOAndBusinessError(t *testing.T) {
	svc, mock := serviceFixture(t)
	h, err := CreateHandler(svc)
	require.NoError(t, err)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO products").WithArgs("SKU-1", "Tea").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO product_events").WithArgs("SKU-1", "created").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	r := httptest.NewRequest("POST", "/products", strings.NewReader(`{"sku":"SKU-1","name":"Tea"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"sku":"SKU-1"`)
	require.NoError(t, mock.ExpectationsWereMet())
	r = httptest.NewRequest("POST", "/products", strings.NewReader(`{"sku":" ","name":"Tea"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 400, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestListHandlerMapsPathAndQuery(t *testing.T) {
	h, err := ListHandler(func(_ context.Context, q ListQuery) ([]Product, error) {
		require.Equal(t, ListQuery{Category: "tea", Limit: 2}, q)
		return []Product{{SKU: "SKU-1", Name: "Tea"}}, nil
	}, func(*http.Request) map[string]string { return map[string]string{"category": "tea"} })
	require.NoError(t, err)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/categories/tea/products?limit=2", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"sku":"SKU-1"`)
}

var _ Transactions = SQLTransactions{}
var _ database.Executor = (*sql.Tx)(nil)
