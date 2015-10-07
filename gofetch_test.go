// This Source Code Form is subject to the terms of the Mozilla Public
// License, version 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

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

		_, err = io.Copy(w, file)
		//assert.Ok(t, err)
	}))
	defer ts.Close()

	progress := make(chan ProgressReport)
	done := make(chan bool)
	go func() {
		_, err := Fetch(Config{
			URL:      ts.URL,
			DestDir:  os.TempDir(),
			Progress: progress,
		})
		assert.Ok(t, err)
		done <- true
	}()

	var total int64
	for p := range progress {
		total += p.WrittenBytes
		assert.Equals(t, int64(-1), p.Total)
	}
	assert.Equals(t, int64(10485760), total)
	<-done
	// Now we can close the test server and let the deferred function to run.
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
	done := make(chan bool)
	go func() {
		_, err := Fetch(Config{
			URL:         ts.URL,
			DestDir:     os.TempDir(),
			Progress:    progress,
			Concurrency: 50,
		})
		assert.Ok(t, err)
		done <- true
	}()

	var total int64
	for p := range progress {
		//fmt.Printf("%d of %d\n", p.Progress, p.Total)
		total += p.WrittenBytes
	}
	assert.Equals(t, int64(10485760), total)
	<-done
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

	_, err = io.Copy(chunkFile, fixtureFile)
	assert.Ok(t, err)

	fixtureFile.Close()
	chunkFile.Close()

	done := make(chan bool)
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
		done <- true
	}()

	var total int64
	for p := range progress {
		//fmt.Printf("%d of %d\n", p.Progress, p.Total)
		total += p.WrittenBytes
	}
	// It should only write to disk the remaining bytes
	assert.Equals(t, int64(10276045), total)
	<-done
	// Fetch finished and we can now use the file without causing data races.

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
