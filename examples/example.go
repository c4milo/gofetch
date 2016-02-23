package main

import (
	"fmt"
	"os"

	"github.com/c4milo/gofetch"
)

func main() {
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
	var totalWritten int64
	for p := range progressCh {
		// p.WrittenBytes does not accumulate, it represents the chunk size written
		// in the current operation.
		totalWritten += p.WrittenBytes
		fmt.Printf("\r%d of %d bytes", totalWritten, p.Total)
	}
	fmt.Printf("\nFile saved at %q\n", myFile.Name())
}
