package statement

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func upload(t *testing.T, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(data)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/dry-run", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	NewHandler().Routes().ServeHTTP(rec, req)
	return rec
}

func TestDryRunReportsTheBanksBalances(t *testing.T) {
	rec := upload(t, "estado.csv", bacFile("0.00", "250.75", bacDetail...))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var got FileView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	s := got.Statements[0]
	if got.Source != SourceBACAccount || s.ExternalNumber != "100000001" || s.LineCount != 2 {
		t.Errorf("report = %+v", got)
	}
	if s.Opening == nil || s.Opening.Formatted != "100.00" || s.Closing.Formatted != "250.75" {
		t.Errorf("opening/closing = %+v / %+v", s.Opening, s.Closing)
	}
	if s.PeriodStart.String() != "2026-06-15" || s.Warnings == nil {
		t.Errorf("period start %s, warnings %v", s.PeriodStart, s.Warnings)
	}
}

func TestDryRunRejectsAnUnknownFormat(t *testing.T) {
	rec := upload(t, "otro.csv", []byte("a,b\n1,2"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}
