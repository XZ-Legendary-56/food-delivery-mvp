package kitchen

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"venue-pasta-roma/internal/generated/partnerclient"
)

// fakePlatform is a minimal stand-in for the real platform's partner API,
// just enough for the handful of calls this service's own logic makes:
// accept/reject/advance an order, and push an availability update. Every
// route just answers 200 with an empty JSON object — nothing under test
// here reads the response body, only whether the call was made and what it
// was made with.
type fakePlatform struct {
	*httptest.Server
	acceptCalls  []string // orderId path segments
	rejectCalls  []string
	advanceCalls []string
	availUpdates int
	// menuResponse, when set, is returned verbatim (as JSON) by PUT /menu.
	menuResponse any

	// acceptResult/rejectResult drive what the next call to that endpoint
	// answers, so a test can simulate "fails, then later succeeds" across
	// two calls without needing a real second server.
	acceptResult fakeResult
	rejectResult fakeResult
}

// fakeResult picks one of three ways an endpoint can answer, mirroring the
// three cases acceptOrder/rejectOrder and their retries actually branch on.
type fakeResult int

const (
	fakeOK fakeResult = iota
	fakeConflict
	fakeServerError
)

func newFakePlatform(t *testing.T) *fakePlatform {
	t.Helper()
	f := &fakePlatform{}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders/{id}/accept", func(w http.ResponseWriter, r *http.Request) {
		f.acceptCalls = append(f.acceptCalls, r.PathValue("id"))
		writeResult(w, f.acceptResult)
	})
	mux.HandleFunc("POST /orders/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		f.rejectCalls = append(f.rejectCalls, r.PathValue("id"))
		writeResult(w, f.rejectResult)
	})
	mux.HandleFunc("POST /orders/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		f.advanceCalls = append(f.advanceCalls, r.PathValue("id"))
		writeEmptyJSON(w)
	})
	mux.HandleFunc("POST /menu/availability", func(w http.ResponseWriter, _ *http.Request) {
		f.availUpdates++
		writeEmptyJSON(w)
	})
	mux.HandleFunc("PUT /menu", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if f.menuResponse == nil {
			_, _ = w.Write([]byte("{}"))
			return
		}
		_ = json.NewEncoder(w).Encode(f.menuResponse)
	})

	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func writeEmptyJSON(w http.ResponseWriter) {
	writeResult(w, fakeOK)
}

func writeResult(w http.ResponseWriter, result fakeResult) {
	w.Header().Set("Content-Type", "application/json")
	switch result {
	case fakeConflict:
		w.WriteHeader(http.StatusConflict)
	case fakeServerError:
		w.WriteHeader(http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusOK)
	}
	_, _ = w.Write([]byte("{}"))
}

func (f *fakePlatform) client(t *testing.T) *partnerclient.ClientWithResponses {
	t.Helper()
	c, err := partnerclient.NewClientWithResponses(f.URL)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	return c
}
