package gofetch

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/hooklift/assert"
)

func TestFetchWithoutContentLength(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, err := os.Open("./fixtures/test")
		assert.Ok(t, err)
		assert.Cond(t, file != nil, "Failed loading fixture file")
		defer file.Close()

		io.Copy(w, file)
	}))
	defer ts.Close()

	progress := make(chan ProgressReport)
	go func() {
		err := Fetch(Config{
			URL:      ts.URL,
			DestDir:  os.TempDir(),
			Progress: progress,
		})
		assert.Ok(t, err)
	}()

	var total int64
	for p := range progress {
		total = p.Progress
		assert.Equals(t, int64(-1), p.Total)
	}
	assert.Equals(t, int64(10485760), total)
}

func TestFetchWithContentLength(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, err := os.Open("./fixtures/test")
		assert.Ok(t, err)
		assert.Cond(t, file != nil, "Failed loading fixture file")
		defer file.Close()

		http.ServeContent(w, r, file.Name(), time.Time{}, file)
	}))
	defer ts.Close()

	progress := make(chan ProgressReport)
	go func() {
		err := Fetch(Config{
			URL: ts.URL,
			//DestDir:  os.TempDir(),
			Progress:    progress,
			Concurrency: 2,
		})
		assert.Ok(t, err)
	}()

	var total int64
	for p := range progress {
		total = p.Progress
	}
	assert.Equals(t, int64(10485760), total)
}

func TestResume(t *testing.T) {

}

func TestResumeParallel(t *testing.T) {

}
