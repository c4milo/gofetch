# Gofetch
[![Build Status](https://travis-ci.org/c4milo/gofetch.svg?branch=master)](https://travis-ci.org/c4milo/gofetch)
[![GoDoc](https://godoc.org/github.com/c4milo/gofetch?status.svg)](https://godoc.org/github.com/c4milo/gofetch)

Go library to download files from the internerds.

## Features
* Resumes downloads if interrupted.
* Allows parallel downloading of a single file by requesting multiple data chunks at once over HTTP.
* Reports download progress through a Go channel if indicated to do so.
* Supports ETags, skipping downloading a file if it hasn't changed on the server.
* Supports checking downloads integrity if file checksums are provided.
* Can be combined with https://github.com/cenkalti/backoff to support retries with exponential back-off


## Example

```go
import (
	"github.com/c4milo/gofetch"
	"io"
	"os"
)

func main() {
	progressCh := make(chan ProgressReport)
	gf := gofetch.New(
		gofetch.DestDir("/tmp"),
		gofetch.Concurrency(10),
		gofetch.ETag(true),

	)

	progressCh := make(chan gofetch.ProgressReport)

	var myFile *os.File
	go func() {
		var err error
		myFile, err = gf.Fetch(
		"http://releases.ubuntu.com/15.10/ubuntu-15.10-server-amd64.iso",
		progressCh)
		if err != nil {
			panic(err)
		}
	}()

	// pogressCh is close by gofetch once a download finishes
	for p := range progressCh {
		// p.WrittenBytes does not accumulate, it represents the chunk size written
		// in the current operation.
		fmt.Printf("%d of %d\n", p.WrittenBytes, p.Total)
	}

	destFile, err := os.Create("/tmp/myfile")
	if err != nil {
		panic(err)
	}

	if _, err := io.Copy(destFile, myFile); err != nil {
		panic(err)
	}

	// Another example: changes concurrency to 5 goroutines
	gf.Concurrency(5)
	_, err := gf.Fetch("http://releases.ubuntu.com/15.10/ubuntu-15.10-desktop-amd64.iso")
	if err != nil {
		panic(err)
	}
}
```
