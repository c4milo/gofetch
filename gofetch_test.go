// This Source Code Form is subject to the terms of the Mozilla Public
// License, version 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package gofetch

import (
	"crypto/sha512"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/hooklift/assert"
	"github.com/mitchellh/go-homedir"
)

func TestFetchWithoutContentLength(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			return
		}
		file, err := os.Open("./fixtures/test")
		assert.Ok(t, err)
		assert.Cond(t, file != nil, "Failed loading fixture file")
		defer file.Close()

		_, err = io.Copy(w, file)
		assert.Ok(t, err)
	}))
	defer ts.Close()

	progressCh := make(chan ProgressReport)
	done := make(chan bool)

	destDir, err := ioutil.TempDir(os.TempDir(), "no-content-length")
	assert.Ok(t, err)
	defer os.RemoveAll(destDir)

	gf := New(DestDir(destDir))
	go func() {
		_, err := gf.Fetch(ts.URL, progressCh)
		assert.Ok(t, err)
		done <- true
	}()

	var total int64
	for p := range progressCh {
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

	progressCh := make(chan ProgressReport)
	done := make(chan bool)

	destDir, err := ioutil.TempDir(os.TempDir(), "content-length")
	assert.Ok(t, err)
	defer os.RemoveAll(destDir)

	gf := New(DestDir(destDir), Concurrency(50))
	go func() {
		_, err := gf.Fetch(ts.URL, progressCh)
		assert.Ok(t, err)
		done <- true
	}()

	var total int64
	for p := range progressCh {
		//fmt.Printf("%d of %d\n", p.WrittenBytes, p.Total)
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

	destDir, err := ioutil.TempDir(os.TempDir(), "resume")
	assert.Ok(t, err)
	defer os.RemoveAll(destDir)

	chunksDir := filepath.Join(destDir, path.Base(ts.URL)+".chunks")
	err = os.MkdirAll(chunksDir, 0760)
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
	progressCh := make(chan ProgressReport)
	var file *os.File
	gf := New(DestDir(destDir), Concurrency(1))
	go func() {
		var err error
		file, err = gf.Fetch(ts.URL, progressCh)
		assert.Ok(t, err)
		done <- true
	}()

	var total int64
	for p := range progressCh {
		//fmt.Printf("%d of %d\n", p.WrittenBytes, p.Total)
		total += p.WrittenBytes
	}
	// It should report the complete file size through the channel
	assert.Equals(t, int64(10485760), total)
	<-done
	// Fetch finished and we can now use the file without causing data races.

	// Checks that the downloaded file has the same size as the test fixture
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

func TestEtagSupport(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, err := os.Open("./fixtures/test")
		assert.Ok(t, err)
		assert.Cond(t, file != nil, "Failed loading fixture file")
		defer file.Close()

		w.Header().Add("Etag", "7h153746154w350m3")
		http.ServeContent(w, r, file.Name(), time.Time{}, file)
	}))
	defer ts.Close()

	destDir, err := ioutil.TempDir(os.TempDir(), "etag")
	assert.Ok(t, err)
	defer os.RemoveAll(destDir)

	gf := New(DestDir(destDir), Concurrency(1))

	// Fetches file for the first tim
	_, err = gf.Fetch(ts.URL+"/test", nil)
	assert.Ok(t, err)

	progressCh := make(chan ProgressReport)
	go func() {
		// Attempts to fetch file once again.
		_, err := gf.Fetch(ts.URL+"/test", progressCh)
		assert.Ok(t, err)
	}()

	var progressCount int
	for range progressCh {
		progressCount++
	}

	// Since the file was already downloaded, there shouldn't be any download
	// progress reported.
	assert.Equals(t, 0, progressCount)

	// Cleans up ~/.gofetch work dir
	dir, err := homedir.Dir()
	assert.Ok(t, err)
	err = os.RemoveAll(filepath.Join(dir, ".gofetch"))
	assert.Ok(t, err)
}
