# Gofetch
[![Build Status](https://travis-ci.org/c4milo/gofetch.svg?branch=master)](https://travis-ci.org/c4milo/gofetch)
[![GoDoc](https://godoc.org/github.com/c4milo/gofetch?status.svg)](https://godoc.org/github.com/c4milo/gofetch)

Go library to download files from the internerds.

## Features
* Resumes downloads if interrupted
* Allows parallel downloading of a single file by requesting multiple data chunks at once over HTTP
* Reports download progress through a Go channel if indicated to do so
* Can be combined with https://github.com/cenkalti/backoff to support retries with exponential back-off
