package main

import (
	"fmt"
	"os"

	"github.com/c4milo/gofetch"
)

func main() {
	gf := gofetch.New(
		gofetch.WithDestDir("/tmp"),
		gofetch.WithConcurrency(100),
		gofetch.WithETag(),
		gofetch.WithChecksum("sha256", "29a8b9009509b39d542ecb229787cdf48f05e739a932289de9e9858d7c487c80"),
	)

	progressCh := make(chan gofetch.ProgressReport)
	doneCh := make(chan struct{})

	var myFile *os.File
	go func() {
		defer close(doneCh)
		var err error
		myFile, err = gf.Fetch(
			"http://releases.ubuntu.com/16.04.1/ubuntu-16.04.1-server-amd64.iso",
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

	<-doneCh
	fmt.Printf("\nFile saved at %q\n", myFile.Name())
}
