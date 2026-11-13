package record

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/database/audit"
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/stretchr/testify/require"
)

type fakeStore struct {
	findID   int64
	saved    Record
	audited  Record
	events   []string
	auditErr error
}

func (f *fakeStore) List(context.Context, pagination.Window, []pagination.Sort) ([]Record, error) {
	return nil, nil
}

func (f *fakeStore) Find(_ context.Context, id int64) (Record, error) {
	f.findID = id
	if f.saved.ID == 0 {
		return Record{}, ErrNotFound
	}
	return f.saved, nil
}
func (f *fakeStore) WithinTransaction(_ context.Context, fn func(Writer) error) error {
	f.events = append(f.events, "begin")
	err := fn(f)
	if err != nil {
		f.saved = Record{}
		f.events = append(f.events, "rollback")
		return err
	}
	f.events = append(f.events, "commit")
	return nil
}
func (f *fakeStore) Insert(_ context.Context, r Record) error {
	f.events = append(f.events, "insert")
	f.saved = r
	return nil
}
func (f *fakeStore) AuditCreated(_ context.Context, r Record) error {
	f.audited = r
	f.events = append(f.events, "audit")
	return f.auditErr
}

func TestCreateCommitsRecordAndAudit(t *testing.T) {
	f := &fakeStore{}
	s := Service{IDs: testIDs, Repository: f, Transactions: f}
	got, err := s.Create(context.Background(), "  hello  ", testActor)
	require.NoError(t, err)
	require.Equal(t, testIDs.value, got.ID)
	require.Equal(t, "hello", got.Title)
	require.Equal(t, []string{"begin", "insert", "audit", "commit"}, f.events)
	found, err := s.Find(context.Background(), got.ID)
	require.NoError(t, err)
	require.Equal(t, got, found)
}
func TestAuditFailureRollsBack(t *testing.T) {
	failure := errors.New("audit unavailable")
	f := &fakeStore{auditErr: failure}
	s := Service{IDs: testIDs, Repository: f, Transactions: f}
	got, err := s.Create(context.Background(), "hello", testActor)
	require.ErrorIs(t, err, failure)
	require.Empty(t, got.ID)
	require.Empty(t, f.saved.ID)
	require.Equal(t, []string{"begin", "insert", "audit", "rollback"}, f.events)
}
func TestValidationAndCancellationAvoidDatabase(t *testing.T) {
	f := &fakeStore{}
	s := Service{IDs: testIDs, Repository: f, Transactions: f}
	_, err := s.Create(context.Background(), " ", testActor)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = s.Find(context.Background(), 0)
	require.ErrorIs(t, err, ErrInvalid)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Create(ctx, "hello", testActor)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, f.events)
}

func TestCreateRejectsNULBeforeTransaction(t *testing.T) {
	f := &fakeStore{}
	s := Service{IDs: testIDs, Repository: f, Transactions: f}
	_, err := s.Create(context.Background(), "hello\x00world", testActor)
	require.ErrorIs(t, err, ErrInvalid)
	require.Empty(t, f.events)
}

var testActor = audit.Actor{Kind: audit.System, ID: "records-test"}

func TestAuditClockAndActor(t *testing.T) {
	f := &fakeStore{}
	calls := 0
	at := time.Date(2026, 9, 28, 1, 2, 3, 123456789, time.FixedZone("offset", 3600))
	s := Service{IDs: testIDs, Transactions: f, Now: func() time.Time { calls++; require.Equal(t, []string{"begin"}, f.events); return at }}
	got, err := s.Create(context.Background(), "hello", testActor)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, at.UTC().Truncate(time.Millisecond), got.CreatedAt)
	require.Equal(t, got.CreatedAt, got.UpdatedAt)
	require.Equal(t, testActor, got.CreatedBy)
	require.Equal(t, testActor, got.UpdatedBy)
	require.Equal(t, got, f.audited)
	require.Equal(t, got, f.saved)
	f.events = nil
	_, err = s.Create(context.Background(), "hello", audit.Actor{})
	require.ErrorIs(t, err, audit.ErrInvalid)
	require.Empty(t, f.events)
	require.Equal(t, 1, calls)
}
func TestInvalidClockRollsBackWithoutRecord(t *testing.T) {
	f := &fakeStore{}
	got, err := (Service{IDs: testIDs, Transactions: f, Now: func() time.Time { return time.Time{} }}).Create(context.Background(), "hello", testActor)
	require.ErrorIs(t, err, audit.ErrInvalid)
	require.Equal(t, Record{}, got)
	require.Equal(t, []string{"begin", "rollback"}, f.events)
}
