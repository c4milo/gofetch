// This Source Code Form is subject to the terms of the Mozilla Public
// License, version 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package gofetch

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
)

// ProgressReport represents the current download progress of a given file
type ProgressReport struct {
	// Total length in bytes of the file being downloaded
	Total int64
	// Written bytes to disk on a write by write basis. It does not accumulate.
	WrittenBytes int64
}

// Fetcher represents an instance of gofetch, holding global configuration options.
type Fetcher struct {
	destDir     string
	etag        bool
	concurrency int
	algorithm   string
	checksum    string
	httpClient  *http.Client
}

// Option as explained in http://commandcenter.blogspot.com/2014/01/self-referential-functions-and-design.html
type Option func(*Fetcher)

// WithDestDir allows you to set the destination directory for the downloaded files.
// By default it is set to: ./
func WithDestDir(dir string) Option {
	return func(f *Fetcher) {
		f.destDir = dir
	}
}

// WithConcurrency allows you to set the number of goroutines used to download a specific
// file. By default it is set to 1.
func WithConcurrency(c int) Option {
	return func(f *Fetcher) {
		f.concurrency = c
	}
}

// WithETag enables ETag support, meaning that if an already downloaded file is currently on disk and matches the ETag value returned by the server,
// it will not be downloaded again. By default it is set to false. Be aware that different servers, serving the same file,
// are likely to return different ETag values, causing the file to be re-downloaded, even though it might already exist on disk.
func WithETag() Option {
	return func(f *Fetcher) {
		f.etag = true
	}
}

// WithTimeout allows to set a global timeout for the HTTP client. Defaults to 30 seconds. It is effectively a
// deadline as that's how Go's stdlib interprets it. It does not reset upon activity in the connection.
func WithTimeout(d time.Duration) Option {
	return func(f *Fetcher) {
		f.httpClient.Timeout = d
	}
}

// WithChecksum verifies the file once it is fully downloaded using the provided hash and expected value.
func WithChecksum(alg, value string) Option {
	return func(f *Fetcher) {
		f.algorithm = alg
		f.checksum = value
	}
}

var workDir string

func init() {
	dir, err := homedir.Dir()
	if err != nil {
		fmt.Printf(`Unable to get user home directory err=%s\n`, err)
	}
	workDir = filepath.Join(dir, ".gofetch")
}

// New creates a new instance of goFetch with the given options.
func New(opts ...Option) *Fetcher {
	// Creates instance and assigns defaults.
	gofetch := &Fetcher{
		concurrency: 1,
		destDir:     "./",
		httpClient:  &http.Client{Timeout: 0},
	}

	for _, opt := range opts {
		opt(gofetch)
	}
	return gofetch
}

// Fetch downloads content from the provided URL. It supports resuming and
// parallelizing downloads while being very memory efficient.
func (gf *Fetcher) Fetch(url string, progressCh chan<- ProgressReport) (*os.File, error) {
	if url == "" {
		return nil, errors.New("URL is required")
	}

	// We need to make a preflight request to get the size of the content and check if the server
	// supports requesting byte ranges.
	res, err := http.Head(url)
	if err != nil {
		return nil, err
	}

	if !strings.HasPrefix(res.Status, "2") {
		return nil, errors.New("HTTP requests returned a non 2xx status code")
	}

	if res.Header.Get("Accept-Ranges") != "bytes" {
		// Server does not support sending byte ranges, setting concurrency to 1
		gf.concurrency = 1
	}

	fileName := path.Base(url)
	destFilePath := filepath.Join(gf.destDir, fileName)

	var etag string
	if gf.etag {
		// Go's stdlib returns header value enclosed in double quotes.
		etag = strings.Trim(res.Header.Get("ETag"), `"`)

		if etag == "" {
			goto FETCH
		}

		// Create directory if it doesn't exist, we ignore errors if it already exists.
		cacheDir := filepath.Join(workDir, fileName)
		etagPath := filepath.Join(cacheDir, etag)
		os.MkdirAll(cacheDir, 0700)

		if _, err := os.Stat(etagPath); err == nil {
			// Our file has been already fully downloaded, return a file
			// descriptor to it and skip fetching altogether.
			fi, err := os.Stat(destFilePath)
			if err == nil && fi.Size() == res.ContentLength {
				if progressCh != nil {
					close(progressCh)
				}
				return os.Open(destFilePath)
			}
		} else {
			f, err := os.Create(etagPath)
			if err != nil {
				return nil, err
			}
			f.Close()
		}
	}

FETCH:
	f, err := gf.parallelFetch(url, destFilePath, res.ContentLength, progressCh)
	if err != nil {
		return nil, err
	}

	if gf.algorithm != "" {
		if err := gf.verify(f, gf.algorithm, gf.checksum); err != nil {
			return nil, errors.Wrap(err, "failed veryfing file integrity")
		}

		// We need to make sure we return the file descriptor ready to be read by the user again
		f.Seek(0, io.SeekStart)
	}

	return f, nil
}

func (gf *Fetcher) verify(f *os.File, algorithm string, checksum string) error {
	var hasher hash.Hash
	switch algorithm {
	case "md5":
		hasher = md5.New()
	case "sha1":
		hasher = sha1.New()
	case "sha256":
		hasher = sha256.New()
	case "sha512":
		hasher = sha512.New()
	default:
		return fmt.Errorf("unsupported hashing algorithm: %s", algorithm)
	}

	// Makes sure file cursor is positioned at the beginning
	_, err := f.Seek(0, 0)
	if err != nil {
		return err
	}

	_, err = io.Copy(hasher, f)
	if err != nil {
		return err
	}

	result := fmt.Sprintf("%x", hasher.Sum(nil))

	if result != checksum {
		return fmt.Errorf("checksum does not match\n found: %s\n expected: %s", result, checksum)
	}

	return nil
}

// parallelFetch fetches using multiple goroutines, each piece is streamed down
// to disk which makes it very efficient in terms of memory usage.
func (gf *Fetcher) parallelFetch(url, destFilePath string, length int64, progressCh chan<- ProgressReport) (*os.File, error) {
	if progressCh != nil {
		defer close(progressCh)
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

			err := gf.fetch(url, chunkFile, min, max, report, progressCh)
			if err != nil {
				errs = append(errs, err)
			}
		}(min, max, int(i))
	}
	wg.Wait()

	if len(errs) > 0 {
		return nil, fmt.Errorf("errors: \n %s", errs)
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
func (gf *Fetcher) assembleChunks(destFile, chunksDir string) (*os.File, error) {
	file, err := os.Create(destFile)
	if err != nil {
		return nil, err
	}

	for i := 0; i < gf.concurrency; i++ {
		chunkFile, err := os.Open(filepath.Join(chunksDir, strconv.Itoa(i)))
		if err != nil {
			return nil, err
		}
		// Deferring within a loop is not ideal but we expect not too many chunks to exists.
		defer chunkFile.Close()

		if _, err := io.Copy(file, chunkFile); err != nil {
			return nil, err
		}
	}
	return file, nil
}

// fetch downloads files using one unbuffered HTTP connection and supports
// resuming downloads if interrupted.
func (gf *Fetcher) fetch(url, destFile string, min, max int64,
	report ProgressReport, progressCh chan<- ProgressReport) error {

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

	currFileSize := fi.Size()
	currChunkSize := (max - min)

	// There is nothing to do if chunk data file was fully downloaded.
	if currFileSize > 0 && currFileSize == currChunkSize {
		return nil
	}

	// Report bytes written already into the file
	if progressCh != nil {
		report.WrittenBytes = currFileSize
		progressCh <- report
	}

	// Adjusts min to resume file download from where it was left off.
	if currFileSize > 0 {
		min = min + currFileSize
	}

	// Prepares writer to report download progress.
	writer := fetchWriter{
		Writer:         file,
		progressCh:     progressCh,
		progressReport: report,
	}

	brange := fmt.Sprintf("bytes=%d-%d", min, max-1)
	if max == -1 {
		brange = fmt.Sprintf("bytes=%d-", min)
	}

	req.Header.Add("Range", brange)
	//fmt.Printf("range %s\n", brange)
	res, err := gf.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if !strings.HasPrefix(res.Status, "2") {
		return errors.New("HTTP requests returned a non 2xx status code")
	}

	reader := res.Body.(io.Reader)
	if max > 0 {
		// Known content-length, so we only read from body the amount of bytes of requested chunk.
		reader = io.LimitReader(res.Body, currChunkSize)
	}
	_, err = io.Copy(&writer, reader)
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
