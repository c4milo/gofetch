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
	// Total length in bytes of the file being downloaded
	Total int64
	// Written bytes to disk on a write by write basis. It does not accumulate.
	WrittenBytes int64
}

// Config allows to configure the download process.
type Config struct {
	// File to download
	URL string
	// Destination directory where the file is going to be downloaded to
	DestDir string
	// Concurrency level for parallel downloads
	Concurrency int
	// If not nil, downloading progress is going to be reported through
	// this channel.
	Progress chan<- ProgressReport
}

// setDefaults sets default values to config struct.
func setDefaults(config *Config) {
	if config.Concurrency == 0 {
		config.Concurrency = 1
	}

	if config.DestDir == "" {
		config.DestDir = "./"
	}
}

// Fetch downloads content from the provided URL. It supports resuming and
// parallelizing downloads while being very memory efficient.
func Fetch(config Config) (*os.File, error) {
	setDefaults(&config)

	if config.URL == "" {
		return nil, errors.New("URL is required")
	}

	// We need to make a preflight request to get the size of the content.
	res, err := http.Head(config.URL)
	if err != nil {
		return nil, err
	}

	if !strings.HasPrefix(res.Status, "2") {
		return nil, errors.New("HTTP requests returned a non 2xx status code")
	}

	destFile := filepath.Join(config.DestDir, path.Base(config.URL))
	return parallelFetch(config, destFile, res.ContentLength)
}

// parallelFetch fetches using multiple goroutines, each piece is streamed down
// to disk which makes it very efficient in terms of memory usage.
func parallelFetch(config Config, destFile string, length int64) (*os.File, error) {
	if config.Progress != nil {
		defer close(config.Progress)
	}

	var wg sync.WaitGroup

	report := ProgressReport{Total: length}
	concurrency := int64(config.Concurrency)
	chunkSize := length / concurrency
	remainingSize := length % concurrency
	chunksDir := filepath.Join(config.DestDir, path.Base(config.URL)+".chunks")

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

			err := fetch(config, chunkFile, min, max, report)
			if err != nil {
				errs = append(errs, err)
			}
		}(min, max, int(i))
	}
	wg.Wait()

	if len(errs) > 0 {
		return nil, fmt.Errorf("Errors: \n %s", errs)
	}

	file, err := assembleChunks(config, destFile, chunksDir)
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
func assembleChunks(config Config, destFile, chunksDir string) (*os.File, error) {
	file, err := os.Create(destFile)
	if err != nil {
		return nil, err
	}

	for i := 0; i < config.Concurrency; i++ {
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
func fetch(config Config, destFile string, min, max int64, report ProgressReport) error {
	client := new(http.Client)
	req, err := http.NewRequest("GET", config.URL, nil)
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
		Writer: file,
		config: config,
		report: report,
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
	report ProgressReport
	config Config
}

func (fw *fetchWriter) Write(b []byte) (int, error) {
	n, err := fw.Writer.Write(b)

	if fw.config.Progress != nil {
		fw.report.WrittenBytes = int64(n)
		fw.config.Progress <- fw.report
	}

	return n, err
}
