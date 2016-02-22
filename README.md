# Gofetch
[![Build Status](https://travis-ci.org/c4milo/gofetch.svg?branch=master)](https://travis-ci.org/c4milo/gofetch)
[![GoDoc](https://godoc.org/github.com/c4milo/gofetch?status.svg)](https://godoc.org/github.com/c4milo/gofetch)

Go library to download files from the internerds.

## Features
* Resumes downloads if interrupted.
* Allows parallel downloading of a single file by requesting multiple data chunks at once over HTTP.
* Reports download progress through a Go channel if indicated to do so.
* Supports ETags, skipping downloading a file if it hasn't changed on the server.
* Support download integrity checks if file checksums are provided.
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
		gf.DestDir("/tmp"),
		gf.Progress(progressCh),
		gf.Concurrency(10),
		gf.ETag(true),

	)

	f, err := gf.Fetch(
	"http://releases.ubuntu.com/15.10/ubuntu-15.10-server-amd64.iso",
	gofetch.CheckIntegrity(gofetch.SHA256, "adf1234adf13243"))
	if err != nil {
		panic(err)
	}

	destFile, err := os.Create("/tmp/myfile")
	if err != nil {
		panic(err)
	}

	if _, err := io.Copy(destFile, f); err != nil {
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
