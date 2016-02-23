package gofetch_test

import (
	"fmt"
	"io"
	"os"

	"github.com/c4milo/gofetch"
)

func Example() {
	gf := gofetch.New(
		gofetch.DestDir(os.TempDir()),
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

	if _, err := io.Copy(destFile, myFile); err != nil {
		panic(err)
	}
}
