package gofetch

import (
	"crypto/sha512"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
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
		_, err := Fetch(Config{
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
		_, err := Fetch(Config{
			URL:         ts.URL,
			DestDir:     os.TempDir(),
			Progress:    progress,
			Concurrency: 50,
		})
		assert.Ok(t, err)
	}()

	var total int64
	for p := range progress {
		//fmt.Printf("%d of %d\n", p.Progress, p.Total)
		total = p.Progress
	}
	assert.Equals(t, int64(10485760), total)
}

func TestResume(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, err := os.Open("./fixtures/test")
		assert.Ok(t, err)
		assert.Cond(t, file != nil, "Failed loading fixture file")
		defer file.Close()

		http.ServeContent(w, r, file.Name(), time.Time{}, file)
	}))
	defer ts.Close()

	destDir := os.TempDir()
	chunksDir := filepath.Join(destDir, path.Base(ts.URL)+".chunks")
	err := os.MkdirAll(chunksDir, 0760)
	assert.Ok(t, err)

	fixtureFile, err := os.Open("./fixtures/test-resume")
	assert.Ok(t, err)

	chunkFile, err := os.Create(filepath.Join(chunksDir, "0"))
	assert.Ok(t, err)

	io.Copy(chunkFile, fixtureFile)
	fixtureFile.Close()
	chunkFile.Close()

	progress := make(chan ProgressReport)
	var file *os.File
	go func() {
		var err error
		file, err = Fetch(Config{
			URL:         ts.URL,
			DestDir:     destDir,
			Progress:    progress,
			Concurrency: 1,
		})
		assert.Ok(t, err)
	}()

	var total int64
	for p := range progress {
		//fmt.Printf("%d of %d\n", p.Progress, p.Total)
		total = p.Progress
	}
	// It should only write to disk the remaining bytes
	assert.Equals(t, int64(10276045), total)

	// Checks that the donwloaded file has the same size as the test fixture
	fi, err := file.Stat()
	assert.Ok(t, err)
	defer file.Close()
	assert.Equals(t, int64(10485760), fi.Size())

	// Checks file integrity
	hasher := sha512.New()
	_, err = io.Copy(hasher, file)
	assert.Ok(t, err)

	result := fmt.Sprintf("%x", hasher.Sum(nil))
	assert.Equals(t, "4ff6e159db38d46a665f26e9f82b98134238c0457cc82727a5258b7184773e4967068cc0eecf3928ecd079f3aea6e22aac024847c6d76c0329c4635c4b6ae327", result)
	file.Close()
}
