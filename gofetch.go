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
	"time"
)

// ProgressReport represents the current download progress of a given file
type ProgressReport struct {
	// Total length in bytes of the file being downloaded
	Total int64
	// Current progress in bytes
	Progress int64
}

// Config allows to configure the download process.
type Config struct {
	// File to download
	URL string
	// Destination directory where the file is going to be downloaded to
	DestDir string
	// Size limit in bytes upon which a parallel download will be used.
	SizeLimit int64
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

	if config.SizeLimit == 0 {
		config.SizeLimit = 1048576 // 1MB
	}
}

// Fetch downloads content from the provided URL. It supports resuming and
// parallelizing downloads while being very memory efficient.
func Fetch(config Config) error {
	setDefaults(&config)

	if config.URL == "" {
		return errors.New("URL is required")
	}

	// We need to make a preflight request to get the size of the content.
	res, err := http.Head(config.URL)
	if err != nil {
		return err
	}

	if !strings.HasPrefix(res.Status, "2") {
		return errors.New("HTTP requests returned a non 2xx status code")
	}

	destFile := filepath.Join(config.DestDir, path.Base(config.URL))

	if res.ContentLength > config.SizeLimit && res.Header.Get("Accept-Ranges") == "bytes" {
		return parallelFetch(config, destFile, res.ContentLength)
	}

	report := ProgressReport{Total: res.ContentLength}
	return fetch(config, destFile, int64(0), res.ContentLength, &report)
}

// parallelFetch fetches using multiple goroutines, each piece is streamed down
// to disk which makes it very efficient in terms of memory usage.
func parallelFetch(config Config, destFile string, length int64) error {
	fmt.Println("Going parallel...")
	var wg sync.WaitGroup

	report := ProgressReport{Total: length}
	chunkSize := length / int64(config.Concurrency)
	remainingSize := length % int64(config.Concurrency)
	chunksDir := filepath.Join(config.DestDir, path.Base(config.URL)+".chunks")
	os.MkdirAll(chunksDir, 0760)

	var errs []error
	for i := 0; i < config.Concurrency; i++ {
		min := chunkSize * int64(i)
		max := chunkSize * int64(i+1)

		if i == config.Concurrency {
			// Add the remaining bytes in the last request
			max += remainingSize
		}

		wg.Add(1)
		go func(min, max int64, chunkNumber int) {
			defer wg.Done()
			chunkFile := filepath.Join(chunksDir, strconv.Itoa(chunkNumber))

			err := fetch(config, chunkFile, min, max, &report)
			if err != nil {
				errs = append(errs, err)
			}
		}(min, max, i)
	}
	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("Errors: \n %s", errs)
	}

	if err := assembleChunks(config, destFile, chunksDir); err != nil {
		return err
	}
	return nil
	// TODO(c4milo): Uncomment after testing
	// return os.RemoveAll(chunksDir)
}

// assembleChunks join all the data pieces together
func assembleChunks(config Config, destFile, chunksDir string) error {
	file, err := os.Create(destFile)
	if err != nil {
		return err
	}
	defer file.Close()

	for i := 0; i < config.Concurrency; i++ {
		chunkFile, err := os.Open(filepath.Join(chunksDir, strconv.Itoa(i)))
		if err != nil {
			return err
		}
		io.Copy(file, chunkFile)
		chunkFile.Close()
	}
	return nil
}

// fetch downloads files using only one unbuffered HTTP connection, supports
// resuming downloads if interrupted as well.
// Even though this function is being called concurrently there is no need to
// sincronize "report" as we are only adding up to the progress value, in other
// words, the operation is commutative no matter the concurrency level.
func fetch(config Config, destFile string, min, max int64, report *ProgressReport) error {
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
		min = min + currSize + 1
	}

	// Prepares writer to report download progress.
	writer := fetchWriter{
		Writer: file,
		config: config,
		report: report,
	}

	brange := fmt.Sprintf("bytes=%d-%d", min, max)
	if max == -1 {
		brange = fmt.Sprintf("bytes=%d-", min)

		// We need this timer to close the progress channel for servers that do
		// not return a content-length header. Without this, the user's code
		// is going to block forever reading from the progress channel.
		writer.timer = time.AfterFunc(1*time.Second, func() {
			if config.Progress != nil {
				close(config.Progress)
			}
		})
	}

	req.Header.Add("Range", brange)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if !strings.HasPrefix(res.Status, "2") {
		return errors.New("HTTP requests returned a non 2xx status code")
	}

	io.Copy(&writer, res.Body)
	return nil
}

// fetchWriter implements a custom io.Writer so we can send granular progress reports
type fetchWriter struct {
	io.Writer
	report *ProgressReport
	config Config
	timer  *time.Timer
}

func (fw *fetchWriter) Write(b []byte) (int, error) {
	if fw.timer != nil {
		fw.timer.Reset(1 * time.Second)
	}

	n, err := fw.Writer.Write(b)
	fw.report.Progress += int64(n)

	fmt.Printf("%+v\n", fw.report)
	if fw.config.Progress != nil {
		sendProgress(fw.config, *fw.report)
	}
	return n, err
}

// sendProgress sends download progress to user provided channel.
func sendProgress(c Config, report ProgressReport) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("channel was closed and we are still reporting download progress? Something strange happened")
		}
	}()

	//fmt.Println(report)
	c.Progress <- report

	if report.Progress >= report.Total {
		close(c.Progress)
	}
}
