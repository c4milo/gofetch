# Gofetch
[![Build Status](https://travis-ci.org/c4milo/gofetch.svg?branch=master)](https://travis-ci.org/c4milo/gofetch)
[![GoDoc](https://godoc.org/github.com/c4milo/gofetch?status.svg)](https://godoc.org/github.com/c4milo/gofetch)

Go library to download files from the internerds using Go 1.7 or greater.

## Features

* Resumes downloads if interrupted.
* Allows parallel downloading of a single file by requesting multiple data chunks at once over HTTP.
* Reports download progress through a Go channel if indicated to do so.
* Supports file integrity verification if a checksum is provided.
* Supports ETags, skipping downloading a file if it hasn't changed on the server.
* Can be combined with https://github.com/cenkalti/backoff to support retrying with exponential back-off

## Gotchas
When downloading file chunks concurrently, you may encounter some issues:

* Servers may limit the number of concurrent connections you have open against them
* Servers may accept the connections but will not send anything, in this case the default HTTP client will timeout for you.
If you provide your own client, make sure it has proper timeouts or your connection will block for several seconds, depending
on your operating system network timeouts.

## Example

```go
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/c4milo/gofetch"
)

func main() {
	gf := gofetch.New(
		gofetch.WithDestDir(os.TempDir()),
		gofetch.WithConcurrency(10),
		gofetch.WithETag(),
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
	var totalWritten int64
	for p := range progressCh {
		// p.WrittenBytes does not accumulate, it represents the chunk size written
		// in the current operation.
		totalWritten += p.WrittenBytes
		fmt.Printf("%d of %d\n", totalWritten, p.Total)
	}

	destFile, err := os.Create("/tmp/ubuntu-15.10-server-amd64.iso")
	if err != nil {
		panic(err)
	}

	defer func() {
		if err := destFile.Close(); err != nil {
			panic(err)
		}
	}()

	if _, err := io.Copy(destFile, myFile); err != nil {
		panic(err)
	}
}
```
