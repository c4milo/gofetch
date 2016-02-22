// This Source Code Form is subject to the terms of the Mozilla Public
// License, version 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package gofetch

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ProgressReport represents the current download progress of a given file
type ProgressReport struct {
	URL string
	// Total length in bytes of the file being downloaded
	Total int64
	// Written bytes to disk on a write by write basis. It does not accumulate.
	WrittenBytes int64
}

// goFetch represents an instance of gofetch, holding global configuration options.
type goFetch struct {
	destDir     string
	etag        bool
	progressCh  chan<- ProgressReport
	concurrency int
}

// Option as explained in http://commandcenter.blogspot.com/2014/01/self-referential-functions-and-design.html
type Option func(*goFetch)

// DestDir allows you to set the destination directory for the downloaded files.
func DestDir(dir string) Option {
	return func(f *goFetch) {
		f.destDir = dir
	}
}

// Progress allows you to set a progress reporting channel for downloads.
func Progress(progress chan<- ProgressReport) Option {
	return func(f *goFetch) {
		f.progressCh = progress
	}
}

// Concurrency allows you to set the number of goroutines used to download a specific
// file.
func Concurrency(c int) Option {
	return func(f *goFetch) {
		f.concurrency = c
	}
}

// ETag allows you to disable or enable ETag support, meaning that if an already
// downloaded file is currently on disk and matches the ETag returned by the server,
// it will not be downloaded again.
func ETag(enable bool) Option {
	return func(f *goFetch) {
		f.etag = enable
	}
}

// New creates a new instance of goFetch with the given options.
func New(opts ...Option) *goFetch {
	// Creates instance and assigns defaults.
	gofetch := &goFetch{
		concurrency: 1,
		destDir:     "./",
		etag:        true,
	}

	for _, opt := range opts {
		opt(gofetch)
	}
	return gofetch
}

// Fetch downloads content from the provided URL. It supports resuming and
// parallelizing downloads while being very memory efficient.
func (gf *goFetch) Fetch(url string, opts ...Option) (*os.File, error) {
	if url == "" {
		return nil, errors.New("URL is required")
	}

	for _, opt := range opts {
		opt(gf)
	}

	// We need to make a preflight request to get the size of the content.
	res, err := http.Head(url)
	if err != nil {
		return nil, err
	}

	if !strings.HasPrefix(res.Status, "2") {
		return nil, errors.New("HTTP requests returned a non 2xx status code")
	}

	destFilePath := filepath.Join(gf.destDir, path.Base(url))
	return gf.parallelFetch(url, destFilePath, res.ContentLength)
}

// parallelFetch fetches using multiple goroutines, each piece is streamed down
// to disk which makes it very efficient in terms of memory usage.
func (gf *goFetch) parallelFetch(url, destFilePath string, length int64) (*os.File, error) {
	// If a progress channel was passed we need to close it once a download
	// finishes to unblock any consumers of it.
	// FIXME: If users decide to use gf.Fetch() in parallel, the channel will be
	// closed once the first download finishes.
	if gf.progressCh != nil {
		defer close(gf.progressCh)
	}

	var wg sync.WaitGroup

	report := ProgressReport{Total: length}
	concurrency := int64(gf.concurrency)
	chunkSize := length / concurrency
	remainingSize := length % concurrency
	chunksDir := filepath.Join(gf.destDir, path.Base(url)+".chunks")

	if err := os.MkdirAll(chunksDir, 0760); err != nil {
		return nil, err
	}

	var errs []error
	for i := int64(0); i < concurrency; i++ {
		min := chunkSize * i
		max := chunkSize * (i + 1)

		if i == (concurrency - 1) {
			// Add the remaining bytes in the last request
			max += remainingSize
		}

		wg.Add(1)
		go func(min, max int64, chunkNumber int) {
			defer wg.Done()
			chunkFile := filepath.Join(chunksDir, strconv.Itoa(chunkNumber))

			err := gf.fetch(url, chunkFile, min, max, report)
			if err != nil {
				errs = append(errs, err)
			}
		}(min, max, int(i))
	}
	wg.Wait()

	if len(errs) > 0 {
		return nil, fmt.Errorf("Errors: \n %s", errs)
	}

	file, err := gf.assembleChunks(destFilePath, chunksDir)
	if err != nil {
		return nil, err
	}

	os.RemoveAll(chunksDir)

	// Makes sure to return the file on the correct offset so it can be
	// consumed by users.
	_, err = file.Seek(0, 0)
	if err != nil {
		return nil, err
	}

	return file, err
}

// assembleChunks join all the data pieces together
func (gf *goFetch) assembleChunks(destFile, chunksDir string) (*os.File, error) {
	file, err := os.Create(destFile)
	if err != nil {
		return nil, err
	}

	for i := 0; i < gf.concurrency; i++ {
		chunkFile, err := os.Open(filepath.Join(chunksDir, strconv.Itoa(i)))
		if err != nil {
			return nil, err
		}

		if _, err := io.Copy(file, chunkFile); err != nil {
			return nil, err
		}
		chunkFile.Close()
	}
	return file, nil
}

// fetch downloads files using one unbuffered HTTP connection and supports
// resuming downloads if interrupted.
func (gf *goFetch) fetch(url, destFile string, min, max int64, report ProgressReport) error {
	client := new(http.Client)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	// In order to resume previous interrupted downloads we need to open the file
	// in append mode.
	file, err := os.OpenFile(destFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0660)
	if err != nil {
		return err
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return err
	}
	currSize := fi.Size()

	// There is nothing to do if file exists and was fully downloaded.
	// We do substraction between max and min to account for the last chunk
	// size, which may be of different size if division between res.ContentLength and config.SizeLimit
	// is not exact.
	if currSize == (max - min) {
		return nil
	}

	// Adjusts min to resume file download from where it was left off.
	if currSize > 0 {
		min = min + currSize
		//fmt.Printf("File part exists, resuming at %d\n", min)
	}

	// Prepares writer to report download progress.
	writer := fetchWriter{
		Writer:         file,
		progressCh:     gf.progressCh,
		progressReport: report,
	}

	brange := fmt.Sprintf("bytes=%d-%d", min, max-1)
	if max == -1 {
		brange = fmt.Sprintf("bytes=%d-", min)
	}

	//fmt.Printf("Downloading chunk: %s\n", brange)
	req.Header.Add("Range", brange)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if !strings.HasPrefix(res.Status, "2") {
		return errors.New("HTTP requests returned a non 2xx status code")
	}

	_, err = io.Copy(&writer, res.Body)
	return err
}

// fetchWriter implements a custom io.Writer so we can send granular
// progress reports when streaming down content.
type fetchWriter struct {
	io.Writer
	//progressCh is the channel sent by the user to get download updates.
	progressCh chan<- ProgressReport
	// report is the structure sent through the progress channel.
	progressReport ProgressReport
}

func (fw *fetchWriter) Write(b []byte) (int, error) {
	n, err := fw.Writer.Write(b)

	if fw.progressCh != nil {
		fw.progressReport.WrittenBytes = int64(n)
		fw.progressCh <- fw.progressReport
	}

	return n, err
}
