package record

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/database/audit"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/pagination"
)

func Register(r chi.Router, s Service, actor audit.Actor) {
	r.Get("/records", respond.Adapter(func(_ http.ResponseWriter, r *http.Request) (any, error) {
		current, size, sorts, err := parseListQuery(r.URL.RawQuery)
		if err != nil {
			return nil, listHTTPError(err)
		}
		result, err := s.List(r.Context(), current, size, sorts)
		if err != nil {
			return nil, listHTTPError(err)
		}
		records := make([]recordResponse, len(result.Records))
		for i, record := range result.Records {
			records[i] = toRecordResponse(record)
		}
		return pagination.Slice[recordResponse]{Current: result.Current, Size: result.Size, HasMore: result.HasMore, Records: records}, nil
	}))
	r.Post("/records", respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) {
		input, err := request.DecodeJSON[struct {
			Title string `json:"title"`
		}](w, r)
		if err != nil {
			return nil, err
		}
		result, err := s.Create(r.Context(), input.Title, actor)
		if err != nil {
			return nil, httpError(err)
		}
		return toRecordResponse(result), nil
	}))
	r.Get("/records/{id}", respond.Adapter(func(_ http.ResponseWriter, r *http.Request) (any, error) {
		input, err := request.DecodePath[struct {
			ID int64 `path:"id"`
		}](map[string]string{"id": chi.URLParam(r, "id")})
		if err != nil {
			return nil, err
		}
		result, err := s.Find(r.Context(), input.ID)
		if err != nil {
			return nil, httpError(err)
		}
		return toRecordResponse(result), nil
	}))
}

// recordResponse owns the HTTP encoding; domain and SQL timestamps remain time.Time.
type recordResponse struct {
	ID        int64              `json:"id,string"`
	Title     string             `json:"title"`
	CreatedAt respond.MillisTime `json:"created_at"`
	UpdatedAt respond.MillisTime `json:"updated_at"`
	CreatedBy audit.Actor        `json:"created_by"`
	UpdatedBy audit.Actor        `json:"updated_by"`
}

func toRecordResponse(record Record) recordResponse {
	return recordResponse{
		ID: record.ID, Title: record.Title,
		CreatedAt: respond.MillisTime(record.CreatedAt), UpdatedAt: respond.MillisTime(record.UpdatedAt),
		CreatedBy: record.CreatedBy, UpdatedBy: record.UpdatedBy,
	}
}

func listHTTPError(err error) error {
	if errors.Is(err, ErrInvalid) {
		return &httperror.Error{Status: 400, Message: "invalid pagination or sort parameters", Err: err}
	}
	return err
}

// listQuery is a transport DTO; service policy still owns bounds and sort aliases.
type listQuery struct {
	Current int64  `query:"current"`
	Size    int64  `query:"size"`
	Sort    string `query:"sort"`
}

func parseListQuery(raw string) (int64, int64, []pagination.Sort, error) {
	input, err := request.DecodeQuery(raw, listQuery{Current: 1, Size: 20})
	if err != nil {
		return 0, 0, nil, err
	}
	var sorts []pagination.Sort
	if input.Sort != "" {
		for _, field := range strings.Split(input.Sort, ",") {
			desc := strings.HasPrefix(field, "-")
			if desc {
				field = field[1:]
			}
			sorts = append(sorts, pagination.Sort{Field: field, Desc: desc})
		}
	}
	return input.Current, input.Size, sorts, nil
}
func httpError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return &httperror.Error{Status: 404, Message: "record not found", Err: err}
	case errors.Is(err, ErrInvalid):
		return &httperror.Error{Status: 400, Message: "title must contain 1–200 characters without NUL; ID must be a positive decimal integer", Err: err}
	default:
		return err
	}
}
